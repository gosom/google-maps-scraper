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
	log.Info(fmt.Sprintf("DEBUG: Creating PlaceJob %s - ExtractImages: %v, ExtractEmail: %v", job.ID, extractImages, extractEmail))

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
	log.Info(fmt.Sprintf("DEBUG: Processing images for %s - Original JSON images: %d", entry.Title, originalImageCount))

	imageResult, imgOk := resp.Meta["images_data"].([]images.BusinessImage)
	if !imgOk || len(imageResult) == 0 {
		log.Info(fmt.Sprintf("DEBUG: No enhanced images found for %s - keeping JSON images (%d)", entry.Title, originalImageCount))
		return
	}

	log.Info(fmt.Sprintf("DEBUG: Enhanced extraction found %d images for %s", len(imageResult), entry.Title))

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
		log.Info(fmt.Sprintf("DEBUG: Enhanced metadata - Load time: %dms, Scroll actions: %d", imageMetadata.LoadTime, imageMetadata.ScrollActions))
	}

	// CRITICAL FIX: Merge enhanced images with JSON images instead of overwriting
	log.Info(fmt.Sprintf("DEBUG: BEFORE merge - JSON images: %d, Enhanced images: %d", len(entry.Images), len(enhancedImages)))

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
		log.Info(fmt.Sprintf("DEBUG: Skipped %d duplicate images during merge", skippedCount))
	}

	// Convert back to slice
	mergedImages := make([]Image, 0, len(imageURLMap))
	for _, img := range imageURLMap {
		mergedImages = append(mergedImages, img)
	}
	entry.Images = mergedImages

	log.Info(fmt.Sprintf("DEBUG: AFTER merge - Total images: %d (added %d new from enhanced)", len(entry.Images), addedCount))

	// DEBUG: Double-check image count for troubleshooting
	if len(entry.Images) != len(imageURLMap) {
		log.Warn(fmt.Sprintf("DEBUG: Image count mismatch! entry.Images: %d, imageURLMap: %d", len(entry.Images), len(imageURLMap)))
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
	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil
	}()

	raw, ok := resp.Meta["json"].([]byte)
	if !ok {
		return nil, nil, fmt.Errorf("could not convert to []byte")
	}

	entry, err := EntryFromJSON(raw)
	if err != nil {
		return nil, nil, err
	}

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

	if j.ExtractEmail && entry.IsWebsiteValidForEmail() {
		opts := []EmailExtractJobOptions{}
		if j.ExitMonitor != nil {
			opts = append(opts, WithEmailJobExitMonitor(j.ExitMonitor))
		}

		emailJob := NewEmailJob(j.ID, &entry, opts...)

		j.UsageInResultststs = false

		return nil, []scrapemate.IJob{emailJob}, nil
	} else if j.ExitMonitor != nil {
		fmt.Printf("DEBUG: PlaceJob %s completed directly (no email extraction)\n", j.ID)
		j.ExitMonitor.IncrPlacesCompleted(1)
	}

	return &entry, nil, err
}

// extractImages performs enhanced image extraction if enabled
func (j *PlaceJob) extractImages(ctx context.Context, page playwright.Page, resp *scrapemate.Response) {
	log := scrapemate.GetLoggerFromContext(ctx)

	// DEBUG: Always log whether extraction is enabled or not
	log.Info(fmt.Sprintf("DEBUG: ExtractImages flag is %v for job %s", j.ExtractImages, j.ID))

	if !j.ExtractImages {
		log.Info(fmt.Sprintf("DEBUG: Multi-image extraction DISABLED for job %s - skipping", j.ID))
		return
	}

	log.Info(fmt.Sprintf("DEBUG: Multi-image extraction ENABLED for job %s - starting extraction", j.ID))

	// Create a separate context for image extraction with longer timeout
	imageCtx, imageCancel := context.WithTimeout(ctx, 90*time.Second) // Increased from 30s to 90s
	defer imageCancel()

	imageExtractor := images.NewImageExtractor(page)
	imageResult, err := imageExtractor.ExtractAllImages(imageCtx)
	if err != nil {
		// Log error but don't fail the entire operation
		log.Warn(fmt.Sprintf("DEBUG: Image extraction FAILED for job %s: %v", j.ID, err))
		return
	}

	// Store images data and metadata for processing in Process method
	resp.Meta["images_data"] = imageResult
	resp.Meta["images_metadata"] = imageExtractor.GetMetadata()
	log.Info(fmt.Sprintf("DEBUG: Image extraction SUCCESSFUL for job %s - extracted %d images", j.ID, len(imageResult)))

	// DEBUG: Log some sample URLs to verify they're different from JSON
	if len(imageResult) > 0 {
		log.Info(fmt.Sprintf("DEBUG: Sample enhanced image URL: %s", imageResult[0].URL))
	}
}

func (j *PlaceJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response

	pageResponse, err := page.Goto(j.GetURL(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		resp.Error = err

		return resp
	}

	if err = clickRejectCookiesIfRequired(page); err != nil {
		resp.Error = err

		return resp
	}

	const defaultTimeout = 5000

	err = page.WaitForURL(page.URL(), playwright.PageWaitForURLOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(defaultTimeout),
	})
	if err != nil {
		resp.Error = err

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
	rawI, err := page.Evaluate(js)
	if err != nil {
		return nil, err
	}

	raw, ok := rawI.(string)
	if !ok {
		return nil, fmt.Errorf("could not convert to string")
	}

	const prefix = `)]}'`

	raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))

	return []byte(raw), nil
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
function parse() {
	const appState = window.APP_INITIALIZATION_STATE[3];
	if (!appState) {
		return null;
	}

	for (let i = 65; i <= 90; i++) {
		const key = String.fromCharCode(i) + "f";
		if (appState[key] && appState[key][6]) {
		return appState[key][6];
		}
	}

	return null;
}
`
