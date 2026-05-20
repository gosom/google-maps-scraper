package gmaps

// Enhanced Image Extraction Support
// This package now supports multi-image extraction when ExtractImages is enabled.
// Features:
// - Multiple images per business (business, menu, user, street categories)
// - Enhanced metadata (dimensions, alt text, attribution)
// - Image categorization and organization
// - Backward compatibility with legacy image format
// - Performance optimized with concurrent processing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps/images"
)

// reviewFetchBudget caps how long an individual place's review-fetch is
// allowed to run after detaching from the parent context. The parent is
// detached so that ExitMonitor cancellations (post-max_results) don't kill
// in-flight review HTTP requests on sibling workers; the cap ensures we
// can't run forever if the request itself hangs. 25 s comfortably covers
// the multi-page review pagination for places up to ~500 reviews.
const reviewFetchBudget = 25 * time.Second

// reviewCircuitBreaker tracks consecutive empty review API responses.
// When it reaches the threshold, review extraction is skipped for remaining places.
// Reset to 0 at the start of each scraping job (via ResetReviewCircuitBreaker).
var (
	reviewEmptyCount              atomic.Int32
	reviewCircuitBreakerThreshold int32 = 3
)

// ResetReviewCircuitBreaker resets the counter. Call at job start.
func ResetReviewCircuitBreaker() {
	reviewEmptyCount.Store(0)
}

type PlaceJobOptions func(*PlaceJob)

type PlaceJob struct {
	scrapemate.Job

	UsageInResults      bool
	ExtractEmail        bool
	ExtractImages       bool
	ExitMonitor         exiter.Exiter
	ExtractExtraReviews bool
	ReviewsMax          int // Maximum number of reviews to extract
	// ImagesPerPlace is the hard per-place cap on stored/charged images.
	// 0 means "skip images entirely" — even the free JSON-payload images
	// that arrive with the page load are dropped, so the user is never
	// billed for images they didn't ask for. A positive N means "take up
	// to N images for this place": the cheap JSON images are used first,
	// and the browser extractor is only invoked when JSON has fewer than
	// N. After this PlaceJob completes, len(entry.Images) ≤ ImagesPerPlace
	// always holds — the downstream billing query trusts this invariant.
	ImagesPerPlace int

	// UserID is the Clerk user identifier (e.g., "user_36X..."). Populated by
	// the webrunner via WithPlaceJobUserContext, propagated from the GmapJob.
	// Empty in CLI/standalone scrapes — emit helpers (see userArgs) omit
	// the "user_id" field entirely in that case to avoid polluting per-user
	// Grafana queries with empty-string buckets.
	UserID string
	// UserJobID is the user-facing jobs.id from the DB (what shows in the
	// dashboard). NOT the same as PlaceJob.ID (per-place UUID) or
	// PlaceJob.ParentID (GmapJob.ID — internal). Used as the "job_id" field
	// in emitted logs so operators can correlate with webrunner lifecycle logs.
	// Empty in CLI/standalone scrapes — emit helpers (see userArgs) omit
	// the "job_id" field entirely in that case.
	UserJobID string

	// ProxyURL is the upstream HTTP proxy URL this PlaceJob's
	// cookie-authenticated review-RPC requests should egress through. Empty
	// means direct egress (the prior, default behavior). Set by the webrunner
	// via WithPlaceJobProxyURL so path A (fetchWithCookies in reviews.go)
	// uses the same per-scrape rotated proxy already applied to browser
	// navigation via scrapemate. Without this, prod requests bypassed the
	// proxy entirely and Google soft-rejected them on the datacenter IP —
	// see fetchReviewsParams.proxyURL for the byte-level reproduction.
	ProxyURL string
}

func NewPlaceJob(parentID, langCode, u string, extractEmail, extractImages bool, reviewsMax int, opts ...PlaceJobOptions) *PlaceJob {
	const (
		defaultPrio       = scrapemate.PriorityMedium
		defaultMaxRetries = 3
	)

	job := PlaceJob{
		Job: scrapemate.Job{
			ID:         uuid.Must(uuid.NewV7()).String(),
			ParentID:   parentID,
			Method:     "GET",
			URL:        u,
			URLParams:  map[string]string{"hl": langCode},
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.UsageInResults = true
	job.ExtractEmail = extractEmail
	job.ExtractImages = extractImages
	job.ExtractExtraReviews = reviewsMax > 0
	job.ReviewsMax = reviewsMax

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithPlaceJobExitMonitor(exitMonitor exiter.Exiter) PlaceJobOptions {
	return func(j *PlaceJob) {
		j.ExitMonitor = exitMonitor
	}
}

// WithPlaceJobImagesPerPlace sets the per-place image cap on the PlaceJob.
// 0 means "no images at all" (the toggle-off case); a positive N means
// "store at most N images for this place".
func WithPlaceJobImagesPerPlace(n int) PlaceJobOptions {
	return func(j *PlaceJob) {
		j.ImagesPerPlace = n
	}
}

// WithPlaceJobUserContext propagates the user-facing job identifiers
// (user_id and the user-facing job_id) to a PlaceJob so its log lines can
// be correlated with webrunner lifecycle events in Grafana/Loki.
func WithPlaceJobUserContext(userID, userJobID string) PlaceJobOptions {
	return func(j *PlaceJob) {
		j.UserID = userID
		j.UserJobID = userJobID
	}
}

// WithPlaceJobProxyURL sets the upstream HTTP proxy URL used by the
// cookie-authenticated review-RPC fetch (fetchWithCookies). Empty string is
// equivalent to omitting the option and preserves the prior direct-egress
// behavior. See PlaceJob.ProxyURL for why this matters.
func WithPlaceJobProxyURL(proxyURL string) PlaceJobOptions {
	return func(j *PlaceJob) {
		j.ProxyURL = proxyURL
	}
}

// applyPerPlaceImageCap enforces the per-place image invariant on a parsed
// Entry. With limit == 0 the JSON-payload images are dropped entirely
// (toggle off); with limit > 0 the slice is truncated and CLONED so the
// original (up-to-80-entry) backing array — including the discarded URL
// strings in its tail — can be garbage-collected immediately rather than
// being held by the trimmed slice header. Idempotent.
func applyPerPlaceImageCap(entry *Entry, limit int) {
	if entry == nil {
		return
	}
	if limit <= 0 {
		entry.Images = nil
		return
	}
	if len(entry.Images) > limit {
		// slices.Clone copies into a new backing array so the discarded
		// tail (and its URL strings) becomes unreachable. slices.Clip
		// alone would only adjust the slice's cap on the SAME array,
		// keeping the tail strings alive until the entry is GC'd.
		entry.Images = slices.Clone(entry.Images[:limit])
	}
}

// remainingImageBudget returns 0 when the JSON-payload images have already
// met or overshot the per-place cap; otherwise returns the number of slots
// left for the browser extractor to fill. Saturating subtraction; never
// returns negative.
func remainingImageBudget(have, limit int) int {
	if limit <= 0 {
		return 0
	}
	if have >= limit {
		return 0
	}
	return limit - have
}

// processExtractedImages converts and integrates extracted image data into the entry
func (j *PlaceJob) processExtractedImages(ctx context.Context, entry *Entry, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(ctx)

	originalImageCount := len(entry.Images)
	log.Info("processing_images", "title", entry.Title, "json_image_count", originalImageCount)

	imageResult, imgOk := resp.Meta["images_data"].([]images.BusinessImage)
	if !imgOk || len(imageResult) == 0 {
		log.Info("no_enhanced_images", "title", entry.Title, "json_image_count", originalImageCount)
		return
	}

	log.Info("enhanced_images_found", "title", entry.Title, "image_count", len(imageResult))

	// Convert images package types to gmaps package types
	enhancedImages := make([]BusinessImage, len(imageResult))
	for i, img := range imageResult {
		enhancedImages[i] = BusinessImage{
			URL:          img.URL,
			ThumbnailURL: img.ThumbnailURL,
			AltText:      img.AltText,
			Category:     img.Category,
			Index:        img.Index,
			Dimensions: ImageDimensions{
				Width:  img.Dimensions.Width,
				Height: img.Dimensions.Height,
			},
			Attribution: img.Attribution,
		}
	}
	entry.EnhancedImages = enhancedImages

	// Convert metadata if available
	if imageMetadata, metaOk := resp.Meta["images_metadata"].(*images.ScrapingMetadata); metaOk {
		entry.ImageExtractionMetadata = &ScrapingMetadata{
			ScrapedAt:     imageMetadata.ScrapedAt,
			ImageCount:    imageMetadata.ImageCount,
			LoadTime:      imageMetadata.LoadTime,
			ScrollActions: imageMetadata.ScrollActions,
		}
		log.Info("enhanced_metadata", "load_time_ms", imageMetadata.LoadTime, "scroll_actions", imageMetadata.ScrollActions)
	}

	// Merge enhanced images with JSON images instead of overwriting
	// Create a map to avoid duplicates
	imageURLMap := make(map[string]Image)

	// First, add all existing JSON images
	for _, img := range entry.Images {
		imageURLMap[img.Image] = img
	}

	// Then add enhanced images, avoiding duplicates
	addedCount := 0
	skippedCount := 0
	for _, img := range enhancedImages {
		if _, exists := imageURLMap[img.URL]; !exists {
			imageURLMap[img.URL] = Image{
				Title: img.Category,
				Image: img.URL,
			}
			addedCount++
		} else {
			skippedCount++
		}
	}

	if skippedCount > 0 {
		log.Info("duplicate_images_skipped", "skipped_count", skippedCount)
	}

	// Convert back to slice
	mergedImages := make([]Image, 0, len(imageURLMap))
	for _, img := range imageURLMap {
		mergedImages = append(mergedImages, img)
	}
	entry.Images = mergedImages

	log.Info("image_merge_complete", "total_images", len(entry.Images), "added_from_enhanced", addedCount)

	// Organize images by category for the ImageCategories field
	categoryMap := make(map[string][]BusinessImage)
	for _, img := range enhancedImages {
		categoryMap[img.Category] = append(categoryMap[img.Category], img)
	}

	for category, imgs := range categoryMap {
		entry.ImageCategories = append(entry.ImageCategories, ImageCategory{
			Title:  category,
			Images: imgs,
		})
	}
}

func (j *PlaceJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	log := scrapemate.GetLoggerFromContext(ctx)

	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil
	}()

	// CRITICAL FIX: Always create an entry with at least the URL, even if JSON extraction fails
	// This ensures all visited places are written to PostgreSQL and CSV
	var entry Entry

	raw, ok := resp.Meta["json"].([]byte)
	if !ok {
		// JSON extraction failed - create minimal fallback entry
		emitJSONExtractionFallback(ctx, j)
		entry = Entry{
			ID:          j.ParentID,
			Link:        j.GetURL(),
			Title:       fmt.Sprintf("EXTRACTION_FAILED_%s", j.ID[:8]), // Unique identifier
			Status:      "extraction_failed",
			Description: "JSON extraction failed - partial data only",
		}
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		// Return the fallback entry so it gets written to database
		return &entry, nil, nil
	}

	parsedEntry, err := EntryFromJSON(raw)
	if err != nil {
		// JSON parsing failed - create minimal fallback entry
		emitJSONParsingFallback(ctx, j, err)
		entry = Entry{
			ID:          j.ParentID,
			Link:        j.GetURL(),
			Title:       fmt.Sprintf("PARSING_FAILED_%s", j.ID[:8]), // Unique identifier
			Status:      "parsing_failed",
			Description: fmt.Sprintf("JSON parsing error: %v", err),
		}
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		// Return the fallback entry so it gets written to database
		return &entry, nil, nil
	}

	// Successful JSON extraction and parsing
	entry = parsedEntry
	checkPlacePayloadInvariants(ctx, j, &entry)

	// Per-place image cap enforcement (post-EntryFromJSON).
	//
	// The Google Maps JSON page payload ships an unbounded `images` array
	// (8–80+ items for a typical café). Before May 2026 these flowed
	// straight to the database and got billed even when image enrichment
	// was disabled (the Cafe Schöneberg bug — 504 images charged on a job
	// configured for "10 photos per place"). The cap below clamps the
	// JSON-payload images BEFORE they reach processExtractedImages, which
	// then knows how much room is left for browser-extracted enhancements.
	applyPerPlaceImageCap(&entry, j.ImagesPerPlace)

	// Integrate enhanced image data if available
	j.processExtractedImages(ctx, &entry, resp)

	// Defence-in-depth: processExtractedImages merges enhanced images on
	// top of the (already-capped) JSON images. Re-apply the cap so the
	// final stored slice still respects the per-place invariant even when
	// a browser-side extractor overshoots (race conditions, late merges).
	applyPerPlaceImageCap(&entry, j.ImagesPerPlace)

	entry.ID = j.ParentID

	if entry.Link == "" {
		entry.Link = j.GetURL()
	}

	// Parse reviews in an isolated block — panics don't lose the place entry.
	func() {
		defer func() {
			if r := recover(); r != nil {
				args := userArgs(j)
				args = append(args,
					"place_job_id", j.ID,
					"search_job_id", j.ParentID,
					"place_url", j.GetURL(),
					"entry_title", entry.Title,
					"panic", r,
					"stack", string(debug.Stack()),
				)
				scrapemate.GetLoggerFromContext(ctx).Error("add_extra_reviews_panic", args...)
			}
		}()
		allReviewsRaw, ok := resp.Meta["reviews_raw"].(fetchReviewsResponse)
		if ok && len(allReviewsRaw.pages) > 0 {
			entry.AddExtraReviews(ctx, j, allReviewsRaw.pages)
		}
	}()

	// CRITICAL FIX: Always write the place entry to database FIRST, even if we're going to extract emails
	// This ensures we don't lose place data if email extraction fails
	if j.ExtractEmail && entry.IsWebsiteValidForEmail() {
		log.Info("email_extraction_queued", "job_id", j.ID, "website", entry.WebSite)

		// Count this place as completed since we have the data
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}

		// Create email extraction job for later processing
		opts := []EmailExtractJobOptions{}
		if j.ExitMonitor != nil {
			opts = append(opts, WithEmailJobExitMonitor(j.ExitMonitor))
		}
		emailJob := NewEmailJob(j.ID, &entry, opts...)

		// IMPORTANT: Return the entry to be written, AND the email job for additional processing
		// This ensures place data is saved even if email extraction fails
		return &entry, []scrapemate.IJob{emailJob}, nil
	}

	// No email extraction needed - just save the place entry
	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesCompleted(1)
	}

	return &entry, nil, nil
}

// extractImages performs enhanced (browser-based) image extraction when
// the per-place cap still has room left over after the free JSON-payload
// images have been counted.
//
// Per-place semantics (locked May 2026 after the Cafe Schöneberg bug):
//
//   - ImagesPerPlace == 0 → toggle off; do nothing.
//   - JSON payload already has ≥ N images for this place → do nothing
//     (the cheaper path covered the cap; running the browser extractor
//     would burn ~30s per place for results we'd immediately discard).
//   - JSON has < N → run the browser extractor and TRUNCATE its result
//     to the remaining budget so the merged entry.Images can never
//     exceed N.
//
// The truncation here is the primary enforcement; PlaceJob.Process
// re-applies applyPerPlaceImageCap after the merge as defence in depth.
func (j *PlaceJob) extractImages(ctx context.Context, page playwright.Page, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(ctx)

	if !j.ExtractImages || j.ImagesPerPlace <= 0 {
		return
	}

	// Count the JSON-payload images we already kept after applyPerPlaceImageCap
	// in Process(). resp.Meta["json_image_count"] is populated by
	// BrowserActions' peek-parse; if absent we fall back to 0, which makes
	// the browser extractor request the full per-place budget — correctness
	// is still preserved because PlaceJob.Process re-applies the cap after
	// the merge. Log a debug line on miss so a future refactor that changes
	// the meta-key type (e.g. int → int64) doesn't go silently undetected.
	have := 0
	if v, ok := resp.Meta["json_image_count"].(int); ok {
		have = v
	} else {
		log.Debug("json_image_count_unavailable",
			"job_id", j.ID,
			"reason", "meta key absent or wrong type — extractor will receive full per-place budget; post-merge cap will rescue correctness")
	}

	// Clamp `have` to the cap before logging so the "have"/"remaining"
	// numbers in subsequent log lines reflect the post-cap reality, not
	// the raw peek count. Without this, a JSON payload of 80 with a cap
	// of 10 would log have=80 even though only 10 are kept.
	if have > j.ImagesPerPlace {
		have = j.ImagesPerPlace
	}

	remaining := remainingImageBudget(have, j.ImagesPerPlace)
	if remaining == 0 {
		log.Info("image_budget_filled_from_json",
			"job_id", j.ID, "have", have, "cap", j.ImagesPerPlace)
		return
	}

	log.Info("image_extraction_started",
		"job_id", j.ID, "have", have, "cap", j.ImagesPerPlace, "remaining", remaining)

	// Create a separate context for image extraction with optimized timeout
	imageCtx, imageCancel := context.WithTimeout(ctx, 30*time.Second) // Fast extraction should complete quickly
	defer imageCancel()

	imageExtractor := images.NewImageExtractor(page)
	imageResult, err := imageExtractor.ExtractAllImages(imageCtx)
	if err != nil {
		// Log error but don't fail the entire operation
		log.Warn("image_extraction_failed", "job_id", j.ID, "error", err)
		return
	}

	// Truncate to the remaining budget so the merge in
	// processExtractedImages cannot push entry.Images above the per-place
	// cap. The extractor returns images in deterministic order from the
	// gallery, so taking the first `remaining` is stable across re-runs.
	if len(imageResult) > remaining {
		imageResult = imageResult[:remaining]
	}

	// Store images data and metadata for processing in Process method
	resp.Meta["images_data"] = imageResult
	resp.Meta["images_metadata"] = imageExtractor.GetMetadata()
	log.Info("image_extraction_completed", "job_id", j.ID, "image_count", len(imageResult))
}

func (j *PlaceJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response

	pageResponse, err := page.Goto(j.GetURL(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		resp.Error = err
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		return resp
	}

	if err = clickRejectCookiesIfRequired(page); err != nil {
		resp.Error = err
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		return resp
	}

	const defaultTimeout = 5000

	err = page.WaitForURL(page.URL(), playwright.PageWaitForURLOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(defaultTimeout),
	})
	if err != nil {
		resp.Error = err
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		return resp
	}

	resp.URL = pageResponse.URL()
	resp.StatusCode = pageResponse.Status()
	resp.Headers = make(http.Header, len(pageResponse.Headers()))

	for k, v := range pageResponse.Headers() {
		resp.Headers.Add(k, v)
	}

	raw, err := j.extractJSON(ctx, page)
	if err != nil {
		resp.Error = err
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		return resp
	}

	if resp.Meta == nil {
		resp.Meta = make(map[string]any)
	}

	resp.Meta["json"] = raw

	// Peek at the JSON-payload image count before deciding whether to run
	// the slow browser extractor. EntryFromJSON is the source of truth for
	// images.length (it filters invalid URLs), and a second parse in
	// Process() rebuilds the full entry — so this is a small duplicate
	// parse that saves up to ~30s per place when JSON already covers the
	// per-place cap. See PlaceJob.extractImages for how this is consumed.
	if peekEntry, peekErr := EntryFromJSON(raw); peekErr == nil {
		resp.Meta["json_image_count"] = len(peekEntry.Images)
	} else {
		// Process() will surface the real parsing error with full context
		// via the "parsing_failed" fallback path — this debug line is
		// purely so JSON-shape regressions are visible at the peek site.
		scrapemate.GetLoggerFromContext(ctx).Debug("json_image_peek_failed",
			"job_id", j.ID, "error", peekErr)
	}

	// Extract images using the enhanced approach (only if ExtractImages is enabled)
	j.extractImages(ctx, page, &resp)

	// Extract reviews in an isolated block — panics here don't crash the PlaceJob.
	func() {
		defer func() {
			if r := recover(); r != nil {
				args := userArgs(j)
				args = append(args,
					"place_job_id", j.ID,
					"search_job_id", j.ParentID,
					"place_url", j.GetURL(),
					"panic", r,
					"stack", string(debug.Stack()),
				)
				scrapemate.GetLoggerFromContext(ctx).Error("review_extraction_panic", args...)
			}
		}()

		if !j.ExtractExtraReviews {
			return
		}

		// Circuit breaker: skip reviews if too many consecutive empty responses
		if reviewEmptyCount.Load() >= reviewCircuitBreakerThreshold {
			emitReviewCircuitBreakerOpen(ctx, j)
			return
		}

		reviewCount := j.getReviewCount(raw)
		if reviewCount > 8 {
			params := fetchReviewsParams{
				page:        page,
				mapURL:      j.GetURL(),
				reviewCount: reviewCount,
				maxReviews:  j.ReviewsMax,
				langCode:    j.URLParams["hl"],
				placeJobID:  j.ID,
				searchJobID: j.ParentID,
				placeName:   "", // entry.Title is set later in Process — left empty
				userID:      j.UserID,
				userJobID:   j.UserJobID,
				proxyURL:    j.ProxyURL,
			}

			reviewFetcher, err := newReviewFetcher(params)
			if err != nil {
				// Bad proxy URL or other init failure. Skip reviews for this
				// place but let the rest of the place data persist — matches
				// the existing "fail open" stance of the review pipeline.
				emitReviewExtractionFailed(ctx, j, err)
				return
			}

			// Bug B fix: detach from parent ctx cancellation but cap with
			// a local budget. When the ExitMonitor cancels mateCtx after
			// max_results is hit, sibling workers still in BrowserActions
			// would otherwise have their in-flight review HTTP requests
			// die (azuretls surfaces ctx.Canceled as "timeout"). The local
			// budget lets the fetch complete; the outer BrowserActions is
			// already bounded by allowedSeconds and the webrunner's 30s
			// forced-completion grace, so this can't run indefinitely.
			fetchCtx, fetchCancel := context.WithTimeout(context.WithoutCancel(ctx), reviewFetchBudget)
			defer fetchCancel()

			reviewData, err := reviewFetcher.fetch(fetchCtx)
			if err != nil {
				emitReviewExtractionFailed(ctx, j, err)
				return
			}

			// Detect "silent empty" responses — Google returns HTTP 200 with empty data
			// when cookies are expired, IP is blocked, or proxy/upstream returns a
			// stub. The 33-byte case we hit in May 2026 was Google's
			// `)]}'\n[null,null,null,null,null,1]` unauthenticated stub returned
			// from prod's datacenter IP — see PlaceJob.ProxyURL.
			if len(reviewData.pages) == 0 || (len(reviewData.pages) == 1 && len(reviewData.pages[0]) < 100) {
				responseBytes := 0
				var sample []byte
				if len(reviewData.pages) > 0 {
					responseBytes = len(reviewData.pages[0])
					sample = reviewData.pages[0]
				}
				count := reviewEmptyCount.Add(1)
				emitReviewAPIEmptyResponse(ctx, j, reviewCount, responseBytes, int(count), sample)
				return
			}

			// Success — reset the circuit breaker
			reviewEmptyCount.Store(0)
			resp.Meta["reviews_raw"] = reviewData
		}
	}()

	return resp
}

func (j *PlaceJob) extractJSON(ctx context.Context, page playwright.Page) ([]byte, error) {
	const (
		maxAttempts   = 15
		retryInterval = 200 * time.Millisecond
	)

	var (
		lastErr     error
		lastPartial []byte // best-effort fallback if no full payload arrives
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		rawI, err := page.Evaluate(js)
		if err != nil {
			lastErr = err
			time.Sleep(retryInterval)
			continue
		}

		var candidate []byte
		switch v := rawI.(type) {
		case string:
			const prefix = ")]}'"
			v = strings.TrimSpace(strings.TrimPrefix(v, prefix))
			if v != "" && (strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{")) {
				candidate = []byte(v)
			}
		case []byte:
			if len(v) > 0 {
				candidate = v
			}
		case nil:
			// keep retrying
		default:
			if b, mErr := json.Marshal(v); mErr == nil && len(b) > 0 && string(b) != "null" {
				candidate = b
			}
		}

		if candidate != nil {
			if isCompletePlacePayload(candidate) {
				return candidate, nil
			}
			// Partial preview payload — APP_INITIALIZATION_STATE has hydrated
			// the search-preview entry but not the place-detail entry yet.
			// Keep polling; if the detail never arrives we'll fall back to
			// this so downstream still gets a usable Entry.
			lastPartial = candidate
		}

		time.Sleep(retryInterval)
	}

	if lastPartial != nil {
		emitPartialPayloadAcceptedWarning(ctx, j, len(lastPartial))
		return lastPartial, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("empty app state after extraction")
}

// checkPlacePayloadInvariants emits warning canaries when EntryFromJSON
// returns an entry whose populated fields are mutually inconsistent — the
// strongest available signal that Google has changed the JSON shape such
// that one field still parses but a related field has moved.
//
// Today the only invariant we check is "rating > 0 implies review_count > 0",
// because that's the exact corruption pattern that motivated Fix A
// (isCompletePlacePayload). A future Google shape change that moves
// review_count to a new index inside darray[4] (without changing the array
// length) would slip past Fix A — this canary catches it.
//
// We deliberately do NOT check the reverse direction (review_count > 0,
// rating == 0): that legitimately occurs on places mid-moderation, where
// the displayed average is suppressed while review records remain.
func checkPlacePayloadInvariants(ctx context.Context, j *PlaceJob, entry *Entry) {
	if entry.ReviewRating > 0 && entry.ReviewCount == 0 {
		args := userArgs(j)
		args = append(args,
			"place_job_id", j.ID,
			"search_job_id", j.ParentID,
			"place_url", j.GetURL(),
			"place_name", entry.Title,
			"rating", entry.ReviewRating,
			"detail", "rating > 0 but review_count == 0 — likely Google JSON shape change (review_count missing) OR an extractJSON race that Fix A did not catch",
		)
		scrapemate.GetLoggerFromContext(ctx).Warn("place_payload_inconsistent_review_count", args...)
	}
}

// isCompletePlacePayload reports whether the extracted raw JSON contains the
// full place-detail payload (darray[4] holds rating + review_count + more)
// rather than the partial search-preview payload (darray[4] holds rating
// only). Empirically verified against captured dumps: complete payloads have
// len(darray[4]) >= 9; partial previews have len(darray[4]) == 8.
func isCompletePlacePayload(raw []byte) bool {
	var jd []any
	if err := json.Unmarshal(raw, &jd); err != nil {
		return false
	}
	if len(jd) <= 6 {
		return false
	}
	darray, ok := jd[6].([]any)
	if !ok || len(darray) <= 4 {
		return false
	}
	four, ok := darray[4].([]any)
	if !ok {
		return false
	}
	return len(four) >= 9
}

// userArgs returns the user_id/job_id args for a PlaceJob, omitting any
// empty values. Web-mode scrapes populate both; CLI/standalone scrapes
// leave them empty and we skip them to avoid polluting per-user Grafana
// alert buckets with empty-string keys.
func userArgs(j *PlaceJob) []any {
	args := make([]any, 0, 4)
	if j.UserJobID != "" {
		args = append(args, "job_id", j.UserJobID)
	}
	if j.UserID != "" {
		args = append(args, "user_id", j.UserID)
	}
	return args
}

// emitJSONExtractionFallback fires when BrowserActions returned a response
// with no raw JSON payload at all (resp.Meta["json"] missing). The PlaceJob
// has already been written as a minimal Entry with just the URL.
func emitJSONExtractionFallback(ctx context.Context, j *PlaceJob) {
	args := userArgs(j)
	args = append(args,
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"reason", "creating minimal entry with URL only",
	)
	scrapemate.GetLoggerFromContext(ctx).Warn("json_extraction_fallback", args...)
}

// emitJSONParsingFallback fires when raw JSON was present but
// EntryFromJSON failed to parse it. Minimal Entry persists with the parse
// error in Description.
func emitJSONParsingFallback(ctx context.Context, j *PlaceJob, err error) {
	args := userArgs(j)
	args = append(args,
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"error", err,
		"reason", "creating minimal entry with URL only",
	)
	scrapemate.GetLoggerFromContext(ctx).Warn("json_parsing_fallback", args...)
}

// emitPartialPayloadAcceptedWarning is called by extractJSON when the
// 15×200ms polling budget exhausts without ever seeing a complete payload
// (jd[6][4] of length >= 9). Logged at WARN because it's a fallback —
// we still return a usable payload, but review_count and reviews_per_rating
// will be empty.
func emitPartialPayloadAcceptedWarning(ctx context.Context, j *PlaceJob, bytes int) {
	args := userArgs(j)
	args = append(args,
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"bytes", bytes,
		"detail", "APP_INITIALIZATION_STATE never fully hydrated within 15×200ms; review_count and reviews_per_rating will be empty",
	)
	scrapemate.GetLoggerFromContext(ctx).Warn("extract_json_partial_payload_accepted", args...)
}

// emitReviewCircuitBreakerOpen is called when reviewEmptyCount reaches the
// threshold and review extraction is skipped for this place.
func emitReviewCircuitBreakerOpen(ctx context.Context, j *PlaceJob) {
	args := userArgs(j)
	args = append(args,
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"consecutive_failures", int(reviewEmptyCount.Load()),
		"action", "skipping reviews for remaining places",
		"likely_cause", "cookies expired or IP rate-limited",
	)
	scrapemate.GetLoggerFromContext(ctx).Error("review_circuit_breaker_open", args...)
}

// emitReviewExtractionFailed is called when the review fetcher returns an error.
func emitReviewExtractionFailed(ctx context.Context, j *PlaceJob, err error) {
	args := userArgs(j)
	args = append(args,
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"error", err,
	)
	scrapemate.GetLoggerFromContext(ctx).Warn("review_extraction_failed", args...)
}

// emitReviewAPIEmptyResponse is called when the review-RPC returned HTTP 200
// with a payload too short to be real review data. The `response_sample` field
// is critical for root-cause analysis: the exact bytes Google returned let
// operators distinguish "unauthenticated stub" from "rate-limited stub" from
// "consent-required redirect" from "JSON shape change" without spelunking
// further. Captured at 256 bytes — enough to recognize any known stub
// signature, small enough to keep log lines under Loki's per-line limit.
func emitReviewAPIEmptyResponse(ctx context.Context, j *PlaceJob, reviewCountOnPage, responseBytes, consecutiveEmpty int, body []byte) {
	args := userArgs(j)
	args = append(args,
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"review_count_on_page", reviewCountOnPage,
		"response_bytes", responseBytes,
		"consecutive_empty", consecutiveEmpty,
		"response_sample", responseSampleForLog(body, 256),
		"proxy_used", proxyHostForLog(j.ProxyURL),
		"possible_cause", "expired cookies, IP blocked, rate limited, or proxy returning stub",
	)
	scrapemate.GetLoggerFromContext(ctx).Warn("review_api_empty_response", args...)
}

// responseSampleForLog returns a printable representation of the first n bytes
// of body, with control characters escaped so the value stays on a single log
// line and JSON-decodes cleanly in Loki/Grafana. Empty body yields "".
func responseSampleForLog(body []byte, n int) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) < n {
		n = len(body)
	}
	var b strings.Builder
	b.Grow(n + 8)
	for _, c := range body[:n] {
		switch {
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c < 0x20 || c == 0x7f:
			b.WriteString(fmt.Sprintf(`\x%02x`, c))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func (j *PlaceJob) getReviewCount(data []byte) int {
	tmpEntry, err := EntryFromJSON(data, true)
	if err != nil {
		return 0
	}

	return tmpEntry.ReviewCount
}

func (j *PlaceJob) UseInResults() bool {
	return j.UsageInResults
}

func ctxWait(ctx context.Context, dur time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(dur):
	}
}

const js = `
(() => {
  try {
    const state = window.APP_INITIALIZATION_STATE;
    if (!state) return null;

    const toOut = (val) => {
      if (val == null) return null;
      try {
        if (typeof val === 'string') return val;
        return JSON.stringify(val);
      } catch (_) {
        return null;
      }
    };

    // If it's an array, iterate all entries
    if (Array.isArray(state)) {
      for (let i = 0; i < state.length; i++) {
        const s = state[i];
        if (!s) continue;

        // Case: object with keys like 'af', 'bf', etc.
        if (typeof s === 'object' && !Array.isArray(s)) {
          for (const k in s) {
            const node = s[k];
            if (!node) continue;
            // Direct [6] index if present
            if (Array.isArray(node) && node.length > 6 && node[6] != null) {
              const v = node[6];
              const out = toOut(v);
              if (out) return out;
            }
            // Nested object with [6]
            if (typeof node === 'object' && node[6] != null) {
              const out = toOut(node[6]);
              if (out) return out;
            }
          }
        }

        // Case: array with index 6
        if (Array.isArray(s) && s.length > 6 && s[6] != null) {
          const out = toOut(s[6]);
          if (out) return out;
        }
      }
    }

    // Also check direct object form
    if (typeof state === 'object' && !Array.isArray(state)) {
      for (const k in state) {
        const node = state[k];
        if (node && node[6] != null) {
          const out = toOut(node[6]);
          if (out) return out;
        }
      }
    }

    return null;
  } catch (e) {
    return null;
  }
})()`
