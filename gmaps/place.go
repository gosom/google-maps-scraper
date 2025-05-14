package gmaps

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

type PlaceJobOptions func(*PlaceJob)

type PlaceJob struct {
	scrapemate.Job

	UsageInResultststs  bool
	ExtractEmail        bool
	ExitMonitor         exiter.Exiter
	ExtractExtraReviews bool
	ReviewsLimit        int
}

func NewPlaceJob(parentID, langCode, u string, extractEmail, extraExtraReviews bool, reviewsLimit int, opts ...PlaceJobOptions) *PlaceJob {
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
		ReviewsLimit: reviewsLimit,
	}

	job.UsageInResultststs = true
	job.ExtractEmail = extractEmail
	job.ExtractExtraReviews = extraExtraReviews

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

	entry.ID = j.ParentID

	if entry.Link == "" {
		entry.Link = j.GetFullURL()
	}

	if j.ExtractExtraReviews {
		reviewCount := j.getReviewCount(raw)
		if reviewCount > 8 { // we have more reviews
			if j.ReviewsLimit != 0 {
				// Safely attempt to convert the document to a Playwright page
				page, ok := resp.Document.(playwright.Page)
				if !ok {
					log.Printf("Warning: Document is not a playwright.Page, skipping review extraction")
					return entry, nil, nil
				}
				
				// Introduce a delay to ensure page is fully loaded
				time.Sleep(3 * time.Second)
				
				// Create a context with reasonable timeout
				reviewsCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				
				// Try to get reviews with error recovery
				fetchedCount, reviews, err := scrollReviews(reviewsCtx, page, j.ReviewsLimit)
				if err != nil {
					log.Printf("Warning: error scrolling reviews: %v", err)
				} else {
					log.Printf("Successfully fetched %d reviews", fetchedCount)
					
					if len(reviews) > 0 {
						for _, review := range reviews {
							entry.AddReview(review.AuthorName, review.AuthorURL, review.Rating, review.RelativeTimeDescription, review.Text)
						}
						log.Printf("Added %d reviews to entry", len(reviews))
					}
				}
			} else {
				// For this path, also safely handle the page conversion
				page, ok := resp.Document.(playwright.Page)
				if !ok {
					log.Printf("Warning: Document is not a playwright.Page, skipping review extraction")
					return entry, nil, nil
				}
				
				params := fetchReviewsParams{
					page:        page,
					mapURL:      j.GetFullURL(),
					reviewCount: reviewCount,
				}
				
				reviewFetcher := newReviewFetcher(params)
				
				reviewData, err := reviewFetcher.fetch(context.Background())
				if err != nil {
					log.Printf("Warning: failed to fetch reviews: %s", err)
				} else {
					resp.Meta["reviews_raw"] = reviewData
				}
			}
		}
	}

	if j.ExtractEmail {
		info := extractBusinessInfo(raw)
		if info.Website != "" {
			entry.WebSite = info.Website
		}

		if entry.IsWebsiteValidForEmail() {
			j := NewEmailJob(j.ParentID, entry)
			return entry, []scrapemate.IJob{j}, nil
		}
	}

	return entry, nil, nil
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
  const inputString = window.APP_INITIALIZATION_STATE[3][6]
  return inputString
}
`
