package gmaps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/exiter"
)

type PlaceJobOptions func(*PlaceJob)

type PlaceJob struct {
	scrapemate.Job

	UsageInResultststs  bool
	ExtractEmail        bool
	ExitMonitor         exiter.Exiter
	ExtractExtraReviews bool
	ExtractExtraPhotos  bool
}

func NewPlaceJob(parentID, langCode, u string, extractEmail, extraExtraReviews, extraPhotos bool, opts ...PlaceJobOptions) *PlaceJob {
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
	job.ExtractExtraReviews = extraExtraReviews
	job.ExtractExtraPhotos = extraPhotos

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

	if j.ExtractExtraPhotos {
		entry.AddExtraPhotos(raw)
	}

	entry.ID = j.ParentID

	if entry.Link == "" {
		entry.Link = j.GetURL()
	}

	// Handle RPC-based reviews
	allReviewsRaw, ok := resp.Meta["reviews_raw"].(FetchReviewsResponse)
	if ok && len(allReviewsRaw.pages) > 0 {
		entry.AddExtraReviews(allReviewsRaw.pages)
	}

	// Handle DOM-based reviews (fallback)
	domReviews, ok := resp.Meta["dom_reviews"].([]DOMReview)
	if ok && len(domReviews) > 0 {
		convertedReviews := ConvertDOMReviewsToReviews(domReviews)
		entry.UserReviewsExtended = append(entry.UserReviewsExtended, convertedReviews...)
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
		j.ExitMonitor.IncrPlacesCompleted(1)
	}

	return &entry, nil, err
}

func (j *PlaceJob) BrowserActions(ctx context.Context, page scrapemate.BrowserPage) scrapemate.Response {
	var resp scrapemate.Response

	pageResponse, err := page.Goto(j.GetURL(), scrapemate.WaitUntilDOMContentLoaded)
	if err != nil {
		resp.Error = err

		return resp
	}

	clickRejectCookiesIfRequired(page)

	const defaultTimeout = 5 * time.Second

	err = page.WaitForURL(page.URL(), defaultTimeout)
	if err != nil {
		resp.Error = err

		return resp
	}

	resp.URL = pageResponse.URL
	resp.StatusCode = pageResponse.StatusCode
	resp.Headers = pageResponse.Headers

	raw, err := j.extractJSON(page)
	if err != nil {
		resp.Error = err

		return resp
	}

	if resp.Meta == nil {
		resp.Meta = make(map[string]any)
	}

	resp.Meta["json"] = raw

	if j.ExtractExtraReviews {
		reviewCount := j.getReviewCount(raw)
		if reviewCount > 8 { // we have more reviews
			params := fetchReviewsParams{
				page:        page,
				mapURL:      page.URL(),
				reviewCount: reviewCount,
			}

			// Use the new fallback mechanism that tries RPC first, then DOM
			rpcData, domReviews, err := FetchReviewsWithFallback(ctx, params)

			switch {
			case err != nil:
				fmt.Printf("Warning: review extraction failed: %v\n", err)
			case len(rpcData.pages) > 0:
				resp.Meta["reviews_raw"] = rpcData
			case len(domReviews) > 0:
				resp.Meta["dom_reviews"] = domReviews
			}
		}
	}

	return resp
}

func (j *PlaceJob) getRaw(ctx context.Context, page scrapemate.BrowserPage) (any, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout while getting raw data: %w", ctx.Err())
		default:
			raw, err := page.Eval(js)
			if err != nil {
				// Continue retrying on error
				<-time.After(time.Millisecond * 200)
				continue
			}

			// Check for valid non-null result
			// go-rod may return nil for JS null, or empty string
			if raw == nil {
				<-time.After(time.Millisecond * 200)
				continue
			}

			// If it's a string, make sure it's not empty
			if str, ok := raw.(string); ok {
				if str == "" {
					<-time.After(time.Millisecond * 200)
					continue
				}
			}

			return raw, nil
		}
	}
}

func (j *PlaceJob) extractJSON(page scrapemate.BrowserPage) ([]byte, error) {
	const maxRetries = 2

	for attempt := range maxRetries {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rawI, err := j.getRaw(ctx, page)

		cancel()

		if err != nil {
			// On timeout, try reloading the page
			if attempt < maxRetries-1 {
				if reloadErr := page.Reload(scrapemate.WaitUntilDOMContentLoaded); reloadErr == nil {
					continue
				}
			}

			return nil, err
		}

		if rawI == nil {
			if attempt < maxRetries-1 {
				if reloadErr := page.Reload(scrapemate.WaitUntilDOMContentLoaded); reloadErr == nil {
					continue
				}
			}

			return nil, fmt.Errorf("APP_INITIALIZATION_STATE data not found")
		}

		raw, ok := rawI.(string)
		if !ok {
			return nil, fmt.Errorf("could not convert to string, got type %T", rawI)
		}

		const prefix = `)]}'`

		raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))

		return []byte(raw), nil
	}

	return nil, fmt.Errorf("APP_INITIALIZATION_STATE data not found after retries")
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

const js = `
(function() {
	if (!window.APP_INITIALIZATION_STATE || !window.APP_INITIALIZATION_STATE[3]) {
		return null;
	}
	const appState = window.APP_INITIALIZATION_STATE[3];
	
	// Search all properties of appState for arrays containing JSON strings
	for (const key of Object.keys(appState)) {
		const arr = appState[key];
		if (Array.isArray(arr)) {
			// Check indices 6 and 5 (where place data typically is)
			for (const idx of [6, 5]) {
				const item = arr[idx];
				if (typeof item === 'string' && item.startsWith(")]}'")) {
					return item;
				}
			}
		}
	}
	return null;
})()
`
