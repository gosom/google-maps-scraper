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
	"log/slog"
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
		log.Warn("json_extraction_fallback", "job_id", j.ID, "reason", "creating minimal entry with URL only")
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
		log.Warn("json_parsing_fallback", "job_id", j.ID, "error", err, "reason", "creating minimal entry with URL only")
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
				slog.Error("add_extra_reviews_panic",
					slog.String("parent_job_id", j.ParentID),
					slog.String("entry_title", entry.Title),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
			}
		}()
		allReviewsRaw, ok := resp.Meta["reviews_raw"].(fetchReviewsResponse)
		if ok && len(allReviewsRaw.pages) > 0 {
			entry.AddExtraReviews(allReviewsRaw.pages)
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

	raw, err := j.extractJSON(page)
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
				slog.Error("review_extraction_panic",
					slog.String("job_id", j.ID),
					slog.String("parent_job_id", j.ParentID),
					slog.String("place_url", j.GetURL()),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
			}
		}()

		if !j.ExtractExtraReviews {
			return
		}

		// Circuit breaker: skip reviews if too many consecutive empty responses
		if reviewEmptyCount.Load() >= reviewCircuitBreakerThreshold {
			slog.Error("review_circuit_breaker_open",
				slog.String("job_id", j.ID),
				slog.String("parent_job_id", j.ParentID),
				slog.Int("consecutive_failures", int(reviewEmptyCount.Load())),
				slog.String("action", "skipping reviews for remaining places"),
				slog.String("likely_cause", "cookies expired or IP rate-limited"),
			)
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
			}

			reviewFetcher := newReviewFetcher(params)

			reviewData, err := reviewFetcher.fetch(ctx)
			if err != nil {
				slog.Warn("review_extraction_failed",
					slog.String("job_id", j.ID),
					slog.String("parent_job_id", j.ParentID),
					slog.String("place_url", j.GetURL()),
					slog.Any("error", err),
				)
				return
			}

			// Detect "silent empty" responses — Google returns HTTP 200 with empty data
			// when cookies are expired or IP is blocked, instead of an error.
			if len(reviewData.pages) == 0 || (len(reviewData.pages) == 1 && len(reviewData.pages[0]) < 100) {
				responseBytes := 0
				if len(reviewData.pages) > 0 {
					responseBytes = len(reviewData.pages[0])
				}
				count := reviewEmptyCount.Add(1)
				slog.Warn("review_api_empty_response",
					slog.String("job_id", j.ID),
					slog.String("parent_job_id", j.ParentID),
					slog.String("place_url", j.GetURL()),
					slog.Int("review_count_on_page", reviewCount),
					slog.Int("response_bytes", responseBytes),
					slog.Int("consecutive_empty", int(count)),
					slog.String("possible_cause", "expired cookies, IP blocked, or rate limited"),
				)
				return
			}

			// Success — reset the circuit breaker
			reviewEmptyCount.Store(0)
			resp.Meta["reviews_raw"] = reviewData
		}
	}()

	return resp
}

func (j *PlaceJob) extractJSON(page playwright.Page) ([]byte, error) {
	const (
		maxAttempts   = 15
		retryInterval = 200 * time.Millisecond
	)

	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		rawI, err := page.Evaluate(js)
		if err != nil {
			lastErr = err
			time.Sleep(retryInterval)
			continue
		}

		switch v := rawI.(type) {
		case string:
			const prefix = ")]}'"
			v = strings.TrimSpace(strings.TrimPrefix(v, prefix))
			if v != "" && (strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{")) {
				return []byte(v), nil
			}
		case []byte:
			if len(v) > 0 {
				return v, nil
			}
		case nil:
			// keep retrying
		default:
			// Try to marshal complex types
			if b, mErr := json.Marshal(v); mErr == nil && len(b) > 0 && string(b) != "null" {
				return b, nil
			}
		}

		time.Sleep(retryInterval)
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("empty app state after extraction")
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
