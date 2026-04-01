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

	UsageInResults      bool
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

// processExtractedImages converts and integrates extracted image data into the entry
func (j *PlaceJob) processExtractedImages(entry *Entry, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(context.Background())

	originalImageCount := len(entry.Images)
	log.Info(fmt.Sprintf("Processing images for %s - original JSON images: %d", entry.Title, originalImageCount))

	imageResult, imgOk := resp.Meta["images_data"].([]images.BusinessImage)
	if !imgOk || len(imageResult) == 0 {
		log.Info(fmt.Sprintf("No enhanced images found for %s - keeping JSON images (%d)", entry.Title, originalImageCount))
		return
	}

	log.Info(fmt.Sprintf("Enhanced extraction found %d images for %s", len(imageResult), entry.Title))

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
		log.Info(fmt.Sprintf("Enhanced metadata - load time: %dms, scroll actions: %d", imageMetadata.LoadTime, imageMetadata.ScrollActions))
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
		log.Info(fmt.Sprintf("Skipped %d duplicate images during merge", skippedCount))
	}

	// Convert back to slice
	mergedImages := make([]Image, 0, len(imageURLMap))
	for _, img := range imageURLMap {
		mergedImages = append(mergedImages, img)
	}
	entry.Images = mergedImages

	log.Info(fmt.Sprintf("Image merge complete - total images: %d (added %d from enhanced extraction)", len(entry.Images), addedCount))

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

	raw, ok := resp.Meta["json"].([]byte)
	if !ok {
		// JSON extraction failed - create minimal fallback entry
		log.Warn(fmt.Sprintf("FALLBACK: PlaceJob %s - JSON extraction failed, creating minimal entry with URL only", j.ID))
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
		log.Warn(fmt.Sprintf("FALLBACK: PlaceJob %s - JSON parsing failed (%v), creating minimal entry with URL only", j.ID, err))
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
	j.processExtractedImages(&entry, resp)

	entry.ID = j.ParentID

	if entry.Link == "" {
		entry.Link = j.GetURL()
	}

	allReviewsRaw, ok := resp.Meta["reviews_raw"].(fetchReviewsResponse)
	if ok && len(allReviewsRaw.pages) > 0 {
		entry.AddExtraReviews(allReviewsRaw.pages)
	}

	// CRITICAL FIX: Always write the place entry to database FIRST, even if we're going to extract emails
	// This ensures we don't lose place data if email extraction fails
	if j.ExtractEmail && entry.IsWebsiteValidForEmail() {
		log.Info(fmt.Sprintf("PlaceJob %s - Will extract emails from %s but writing place data first", j.ID, entry.WebSite))

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

// extractImages performs enhanced image extraction if enabled
func (j *PlaceJob) extractImages(ctx context.Context, page playwright.Page, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(ctx)

	if !j.ExtractImages {
		return
	}

	log.Info(fmt.Sprintf("Starting multi-image extraction for job %s", j.ID))

	// Create a separate context for image extraction with optimized timeout
	imageCtx, imageCancel := context.WithTimeout(ctx, 30*time.Second) // Fast extraction should complete quickly
	defer imageCancel()

	imageExtractor := images.NewImageExtractor(page)
	imageResult, err := imageExtractor.ExtractAllImages(imageCtx)
	if err != nil {
		// Log error but don't fail the entire operation
		log.Warn(fmt.Sprintf("Image extraction failed for job %s: %v", j.ID, err))
		return
	}

	// Store images data and metadata for processing in Process method
	resp.Meta["images_data"] = imageResult
	resp.Meta["images_metadata"] = imageExtractor.GetMetadata()
	log.Info(fmt.Sprintf("Image extraction completed for job %s - extracted %d images", j.ID, len(imageResult)))
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

	if j.ExtractExtraReviews {
		reviewCount := j.getReviewCount(raw)
		if reviewCount > 8 { // we have more reviews
			params := fetchReviewsParams{
				page:        page,
				mapURL:      page.URL(),
				reviewCount: reviewCount,
				maxReviews:  j.ReviewsMax, // Pass the review limit
			}

			reviewFetcher := newReviewFetcher(params)

			reviewData, err := reviewFetcher.fetch(ctx)
			if err != nil {
				return resp
			}

			resp.Meta["reviews_raw"] = reviewData
		}
	}

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
