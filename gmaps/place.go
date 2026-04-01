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
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps/images"
)

type PlaceJobOptions func(*PlaceJob)

type PlaceJob struct {
	scrapemate.Job

	UsageInResultststs  bool
	ExtractEmail        bool
	ExtractImages       bool
	ExitMonitor         exiter.Exiter
	ExtractExtraReviews bool
	ReviewsMax          int // Maximum number of reviews to extract
}

func NewPlaceJob(parentID, langCode, u string, extractEmail, extractImages bool, reviewsMax int, opts ...PlaceJobOptions) *PlaceJob {
	const (
		defaultPrio       = scrapemate.PriorityMedium
		defaultMaxRetries = 3
	)

	job := PlaceJob{
		Job: scrapemate.Job{
			ID:         uuid.New().String(),
			ParentID:   parentID,
			Method:     "GET",
			URL:        u,
			URLParams:  map[string]string{"hl": langCode},
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.UsageInResultststs = true
	job.ExtractEmail = extractEmail
	job.ExtractImages = extractImages
	job.ExtractExtraReviews = reviewsMax > 0
	job.ReviewsMax = reviewsMax

	// DEBUG: Log the job creation with flags
	log := scrapemate.GetLoggerFromContext(context.Background())
	log.Debug("place_job_created",
		slog.String("job_id", job.ID),
		slog.Bool("extract_images", extractImages),
		slog.Bool("extract_email", extractEmail),
	)

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

// processExtractedImages converts and integrates extracted image data into the entry
func (j *PlaceJob) processExtractedImages(entry *Entry, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(context.Background())

	// DEBUG: Log the current state before processing
	originalImageCount := len(entry.Images)
	log.Debug("place_job_processing_images",
		slog.String("job_id", j.ID),
		slog.String("title", entry.Title),
		slog.Int("original_json_images", originalImageCount),
	)

	imageResult, imgOk := resp.Meta["images_data"].([]images.BusinessImage)
	if !imgOk || len(imageResult) == 0 {
		log.Debug("place_job_no_enhanced_images",
			slog.String("job_id", j.ID),
			slog.String("title", entry.Title),
			slog.Int("original_json_images", originalImageCount),
		)
		return
	}

	log.Debug("place_job_enhanced_images_found",
		slog.String("job_id", j.ID),
		slog.String("title", entry.Title),
		slog.Int("enhanced_image_count", len(imageResult)),
	)

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
		log.Debug("place_job_enhanced_image_metadata",
			slog.String("job_id", j.ID),
			slog.Int("load_time_ms", imageMetadata.LoadTime),
			slog.Int("scroll_actions", imageMetadata.ScrollActions),
		)
	}

	// CRITICAL FIX: Merge enhanced images with JSON images instead of overwriting
	log.Debug("place_job_image_merge_before",
		slog.String("job_id", j.ID),
		slog.Int("json_images", len(entry.Images)),
		slog.Int("enhanced_images", len(enhancedImages)),
	)

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

	// DEBUG: Log duplicate filtering stats
	if skippedCount > 0 {
		log.Debug("place_job_image_merge_duplicates_skipped",
			slog.String("job_id", j.ID),
			slog.Int("duplicates_skipped", skippedCount),
		)
	}

	// Convert back to slice
	mergedImages := make([]Image, 0, len(imageURLMap))
	for _, img := range imageURLMap {
		mergedImages = append(mergedImages, img)
	}
	entry.Images = mergedImages

	log.Debug("place_job_image_merge_after",
		slog.String("job_id", j.ID),
		slog.Int("total_images", len(entry.Images)),
		slog.Int("enhanced_images_added", addedCount),
	)

	// DEBUG: Double-check image count for troubleshooting
	if len(entry.Images) != len(imageURLMap) {
		log.Warn("place_job_image_count_mismatch",
			slog.String("job_id", j.ID),
			slog.Int("entry_images", len(entry.Images)),
			slog.Int("image_url_map", len(imageURLMap)),
		)
	}

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

func (j *PlaceJob) Process(_ context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	log := scrapemate.GetLoggerFromContext(context.Background())

	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil
	}()

	// CRITICAL FIX: Always create an entry with at least the URL, even if JSON extraction fails
	// This ensures all visited places are written to PostgreSQL and CSV
	var entry Entry
	var entryCreated bool

	raw, ok := resp.Meta["json"].([]byte)
	if !ok {
		// JSON extraction failed - create minimal fallback entry
		log.Warn("place_job_json_extraction_failed_fallback",
			slog.String("job_id", j.ID),
			slog.String("url", j.GetURL()),
		)
		entry = Entry{
			ID:          j.ParentID,
			Link:        j.GetURL(),
			Title:       fmt.Sprintf("EXTRACTION_FAILED_%s", j.ID[:8]), // Unique identifier
			Status:      "extraction_failed",
			Description: "JSON extraction failed - partial data only",
		}
		entryCreated = true

		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		// IMPORTANT: Return the fallback entry instead of nil, so it gets written to database
		return &entry, nil, fmt.Errorf("JSON extraction failed but fallback entry created")
	}

	parsedEntry, err := EntryFromJSON(raw)
	if err != nil {
		// JSON parsing failed - create minimal fallback entry
		log.Warn("place_job_json_parsing_failed_fallback",
			slog.String("job_id", j.ID),
			slog.String("url", j.GetURL()),
			slog.Any("error", err),
		)
		entry = Entry{
			ID:          j.ParentID,
			Link:        j.GetURL(),
			Title:       fmt.Sprintf("PARSING_FAILED_%s", j.ID[:8]), // Unique identifier
			Status:      "parsing_failed",
			Description: fmt.Sprintf("JSON parsing error: %v", err),
		}
		entryCreated = true

		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
		// IMPORTANT: Return the fallback entry instead of nil, so it gets written to database
		return &entry, nil, fmt.Errorf("JSON parsing failed but fallback entry created: %w", err)
	}

	// Successful JSON extraction and parsing
	entry = parsedEntry
	entryCreated = true

	// Integrate enhanced image data if available
	j.processExtractedImages(&entry, resp)

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
		log.Info("place_job_email_extraction_queued",
			slog.String("job_id", j.ID),
			slog.String("website", entry.WebSite),
		)

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

	if !entryCreated {
		log.Error("place_job_no_entry_created", slog.String("job_id", j.ID))
		return nil, nil, fmt.Errorf("no entry created")
	}

	return &entry, nil, nil
}

// extractImages performs enhanced image extraction if enabled
func (j *PlaceJob) extractImages(ctx context.Context, page playwright.Page, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(ctx)

	// DEBUG: Always log whether extraction is enabled or not
	log.Debug("place_job_extract_images_flag",
		slog.String("job_id", j.ID),
		slog.Bool("extract_images", j.ExtractImages),
	)

	if !j.ExtractImages {
		log.Debug("place_job_image_extraction_disabled", slog.String("job_id", j.ID))
		return
	}

	log.Debug("place_job_image_extraction_enabled", slog.String("job_id", j.ID))

	// Create a separate context for image extraction with optimized timeout
	imageCtx, imageCancel := context.WithTimeout(ctx, 30*time.Second) // Fast extraction should complete quickly
	defer imageCancel()

	imageExtractor := images.NewImageExtractor(page)
	imageResult, err := imageExtractor.ExtractAllImages(imageCtx)
	if err != nil {
		// Log error but don't fail the entire operation
		log.Warn("place_job_image_extraction_failed",
			slog.String("job_id", j.ID),
			slog.Any("error", err),
		)
		return
	}

	// Store images data and metadata for processing in Process method
	resp.Meta["images_data"] = imageResult
	resp.Meta["images_metadata"] = imageExtractor.GetMetadata()
	log.Debug("place_job_image_extraction_succeeded",
		slog.String("job_id", j.ID),
		slog.Int("image_count", len(imageResult)),
	)

	// DEBUG: Log some sample URLs to verify they're different from JSON
	if len(imageResult) > 0 {
		log.Debug("place_job_image_extraction_sample_url",
			slog.String("job_id", j.ID),
			slog.String("url", imageResult[0].URL),
		)
	}
}

func (j *PlaceJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response

	// Inject Google cookies for authenticated access (reviews, full data)
	if err := InjectCookiesIntoPage(page); err != nil {
		slog.Debug("place_cookies_inject_skipped", slog.Any("error", err))
	}

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

	// Re-inject cookies AFTER consent handling — the consent dialog may have
	// cleared or overwritten our auth cookies when clicking reject/accept.
	if err := InjectCookiesIntoPage(page); err != nil {
		slog.Debug("place_cookies_reinject_skipped", slog.Any("error", err))
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

	// IMPORTANT: Save the place URL BEFORE image extraction navigates away from the page.
	// Image extraction clicks the "Photos" button and navigates to a different view,
	// which changes page.URL(). Reviews need the original place URL to build the RPC request.
	placeURL := page.URL()

	// Extract reviews in an isolated block — panics here don't crash the PlaceJob.
	// Reviews are a "Lego piece": they can fail independently without losing the place.
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("review_extraction_panic",
					slog.String("job_id", j.ID),
					slog.String("parent_job_id", j.ParentID),
					slog.String("place_url", placeURL),
					slog.Any("panic", r),
				)
			}
		}()

		if !j.ExtractExtraReviews {
			return
		}

		reviewCount := j.getReviewCount(raw)
		if reviewCount > 8 {
			params := fetchReviewsParams{
				page:        page,
				mapURL:      placeURL,
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
					slog.String("place_url", placeURL),
					slog.Any("error", err),
				)
				return
			}
			resp.Meta["reviews_raw"] = reviewData
		}
	}()

	// Extract images AFTER reviews (image extraction navigates to Photos tab)
	j.extractImages(ctx, page, &resp)

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
	return j.UsageInResultststs
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
