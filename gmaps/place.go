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
	// ImageBudget is a per-job total image budget shared with all sibling
	// PlaceJobs in the same scrape job. When non-nil, extractImages checks
	// the counter before scraping and decrements after — once the budget
	// is exhausted, image extraction is skipped for the remaining places
	// in the job. When nil, no cross-place enforcement.
	ImageBudget *atomic.Int64
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

// WithPlaceJobImageBudget attaches a per-job total image budget to the
// PlaceJob. The counter is shared via pointer with sibling PlaceJobs in
// the same scrape job — once exhausted, image extraction is skipped for
// the remaining places. Pass nil (or omit this option) to disable
// cross-place image budget enforcement.
func WithPlaceJobImageBudget(budget *atomic.Int64) PlaceJobOptions {
	return func(j *PlaceJob) {
		j.ImageBudget = budget
	}
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

	// Integrate enhanced image data if available
	j.processExtractedImages(ctx, &entry, resp)

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

// extractImages performs enhanced image extraction if enabled.
//
// When j.ImageBudget is non-nil, the budget is checked BEFORE scraping
// (skip if exhausted) and decremented AFTER scraping by the number of
// images actually extracted. This is the cross-place enforcement that
// bounds total image extraction across an entire scrape job — see §2 of
// docs/superpowers/plans/2026-04-08-api-production-readiness-audit.md
// for the rationale.
//
// Race-safety note: Load() and Add() are independent operations, so two
// concurrent PlaceJobs can both observe a non-zero budget at the same
// instant and both decide to scrape, even if the post-scrape decrement
// would push the counter below zero. The result is a small overshoot
// bounded by `concurrency × images_per_place`. This is acceptable: the
// goal is bounding billing exposure, not exact accounting, and the API
// already enforces a hard cap of 20000 at the validator layer.
func (j *PlaceJob) extractImages(ctx context.Context, page playwright.Page, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(ctx)

	if !j.ExtractImages {
		return
	}

	// Cross-place per-job total budget check (Task 2.3).
	if j.ImageBudget != nil && j.ImageBudget.Load() <= 0 {
		log.Info("image_budget_exhausted", "job_id", j.ID)
		return
	}

	log.Info("image_extraction_started", "job_id", j.ID)

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

	// Decrement the per-job total budget by the number of images we just
	// extracted. The next PlaceJob to call extractImages will observe the
	// reduced counter and bail early if the budget is exhausted.
	if j.ImageBudget != nil && len(imageResult) > 0 {
		j.ImageBudget.Add(-int64(len(imageResult)))
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
