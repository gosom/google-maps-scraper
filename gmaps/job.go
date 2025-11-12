package gmaps

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

type GmapJobOptions func(*GmapJob)

type GmapJob struct {
	scrapemate.Job

	MaxDepth      int
	LangCode      string
	ExtractEmail  bool
	ExtractImages bool
	Debug         bool

	Deduper             deduper.Deduper
	ExitMonitor         exiter.Exiter
	ExtractExtraReviews bool
	ReviewsMax          int // Maximum number of reviews to extract
}

func NewGmapJob(
	id, langCode, query string,
	maxDepth int,
	extractEmail bool,
	extractImages bool,
	reviewsMax int,
	geoCoordinates string,
	zoom int,
	opts ...GmapJobOptions,
) *GmapJob {
	query = url.QueryEscape(query)

	const (
		maxRetries = 3
		prio       = scrapemate.PriorityLow
	)

	if id == "" {
		id = uuid.New().String()
	}

	mapURL := ""
	if geoCoordinates != "" && zoom > 0 {
		mapURL = fmt.Sprintf("https://www.google.com/maps/search/%s/@%s,%dz", query, strings.ReplaceAll(geoCoordinates, " ", ""), zoom)
	} else {
		//Warning: geo and zoom MUST be both set or not
		mapURL = fmt.Sprintf("https://www.google.com/maps/search/%s", query)
	}

	job := GmapJob{
		Job: scrapemate.Job{
			ID:         id,
			Method:     http.MethodGet,
			URL:        mapURL,
			URLParams:  map[string]string{"hl": langCode},
			MaxRetries: maxRetries,
			Priority:   prio,
		},
		MaxDepth:            maxDepth,
		LangCode:            langCode,
		ExtractEmail:        extractEmail,
		ExtractImages:       extractImages,
		ExtractExtraReviews: reviewsMax > 0,
		ReviewsMax:          reviewsMax,
	}

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithDeduper(d deduper.Deduper) GmapJobOptions {
	return func(j *GmapJob) {
		j.Deduper = d
	}
}

func WithExitMonitor(e exiter.Exiter) GmapJobOptions {
	return func(j *GmapJob) {
		j.ExitMonitor = e
	}
}

func WithExtraReviews() GmapJobOptions {
	return func(j *GmapJob) {
		j.ExtractExtraReviews = true
	}
}

func WithDebug() GmapJobOptions {
	return func(j *GmapJob) {
		j.Debug = true
	}
}

func (j *GmapJob) UseInResults() bool {
	return false
}

func (j *GmapJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	// Check for cancellation before processing
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}

	log := scrapemate.GetLoggerFromContext(ctx)

	// DEBUG: Log GmapJob flags
	log.Info(fmt.Sprintf("DEBUG: GmapJob %s processing - ExtractImages: %v, ExtractEmail: %v", j.ID, j.ExtractImages, j.ExtractEmail))

	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		return nil, nil, fmt.Errorf("could not convert to goquery document")
	}

	var next []scrapemate.IJob

	if strings.Contains(resp.URL, "/maps/place/") {
		// Check for cancellation before creating place job
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		jopts := []PlaceJobOptions{}
		if j.ExitMonitor != nil {
			jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
		}

		log.Info(fmt.Sprintf("DEBUG: Creating single PlaceJob from direct place URL with ExtractImages: %v", j.ExtractImages))
		placeJob := NewPlaceJob(j.ID, j.LangCode, resp.URL, j.ExtractEmail, j.ExtractImages, j.ReviewsMax, jopts...)

		next = append(next, placeJob)
	} else {
		log.Info(fmt.Sprintf("DEBUG: Processing search results page - will create PlaceJobs with ExtractImages: %v", j.ExtractImages))

		// Get max results limit from ExitMonitor if available
		maxResults := 0
		if j.ExitMonitor != nil {
			maxResults = j.ExitMonitor.GetMaxResults()
			log.Info(fmt.Sprintf("DEBUG: Max results limit set to: %d", maxResults))
		}

		doc.Find(`div[role=feed] div[jsaction]>a`).Each(func(i int, s *goquery.Selection) {
			// Check for cancellation during processing
			select {
			case <-ctx.Done():
				return // Exit the Each loop early
			default:
			}

			if href := s.AttrOr("href", ""); href != "" {
				// Note: Removed early termination logic - let exit monitor handle max results
				// based on actual successful results, not PlaceJobs created

				jopts := []PlaceJobOptions{}
				if j.ExitMonitor != nil {
					jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
				}

				log.Info(fmt.Sprintf("DEBUG: Creating PlaceJob %d from search result with ExtractImages: %v", i+1, j.ExtractImages))
				nextJob := NewPlaceJob(j.ID, j.LangCode, href, j.ExtractEmail, j.ExtractImages, j.ReviewsMax, jopts...)

				if j.Deduper == nil || j.Deduper.AddIfNotExists(ctx, href) {
					next = append(next, nextJob)
				}
			}
		})
	}

	// Check for cancellation after processing
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesFound(len(next))
		j.ExitMonitor.IncrSeedCompleted(1)
	}

	log.Info(fmt.Sprintf("DEBUG: Created %d PlaceJobs from GmapJob %s", len(next), j.ID))

	return nil, next, nil
}

func (j *GmapJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		resp.Error = ctx.Err()
		return resp
	default:
	}

	pageResponse, err := page.Goto(j.GetFullURL(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000), // Increased timeout
	})

	if err != nil {
		resp.Error = err
		return resp
	}

	// Check for cancellation after navigation
	select {
	case <-ctx.Done():
		resp.Error = ctx.Err()
		return resp
	default:
	}

	// Wait a bit before handling cookies to let page load
	page.WaitForTimeout(3000)

	if err = clickRejectCookiesIfRequired(page); err != nil {
		resp.Error = err
		return resp
	}

	// Wait for main content to be ready
	const defaultTimeout = 10000

	err = page.WaitForURL(page.URL(), playwright.PageWaitForURLOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(defaultTimeout),
	})

	if err != nil {
		resp.Error = err
		return resp
	}

	// Check for cancellation after waiting
	select {
	case <-ctx.Done():
		resp.Error = ctx.Err()
		return resp
	default:
	}

	resp.URL = pageResponse.URL()
	resp.StatusCode = pageResponse.Status()
	resp.Headers = make(http.Header, len(pageResponse.Headers()))

	for k, v := range pageResponse.Headers() {
		resp.Headers.Add(k, v)
	}

	// When Google Maps finds only 1 place, it slowly redirects to that place's URL
	// check element scroll
	sel := `div[role='feed']`

	//nolint:staticcheck // TODO replace with the new playwright API
	_, err = page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(700),
	})

	var singlePlace bool

	if err != nil {
		waitCtx, waitCancel := context.WithTimeout(ctx, time.Second*5)
		defer waitCancel()

		singlePlace = waitUntilURLContains(waitCtx, page, "/maps/place/")

		waitCancel()
	}

	// Check for cancellation before processing single place
	select {
	case <-ctx.Done():
		resp.Error = ctx.Err()
		return resp
	default:
	}

	// Debug: log the current URL to see if we're being redirected
	log := scrapemate.GetLoggerFromContext(ctx)
	log.Info(fmt.Sprintf("DEBUG: Current page URL after navigation: %s", page.URL()))

	if singlePlace {
		resp.URL = page.URL()
		log.Info(fmt.Sprintf("DEBUG: Detected single place redirect to: %s", resp.URL))

		var body string

		body, err = page.Content()
		if err != nil {
			resp.Error = err
			return resp
		}

		resp.Body = []byte(body)
		log.Info(fmt.Sprintf("DEBUG: Single place content length: %d", len(resp.Body)))

		return resp
	}

	// Debug: Check if the feed selector exists
	log.Info("DEBUG: Looking for search results feed...")
	_, feedErr := page.QuerySelector(`div[role='feed']`)
	if feedErr != nil {
		log.Info(fmt.Sprintf("DEBUG: Feed selector not found: %v", feedErr))
	} else {
		log.Info("DEBUG: Feed selector found successfully")
	}

	scrollSelector := `div[role='feed']`

	_, err = scroll(ctx, page, j.MaxDepth, scrollSelector)
	if err != nil {
		resp.Error = err
		return resp
	}

	// Final cancellation check before getting content
	select {
	case <-ctx.Done():
		resp.Error = ctx.Err()
		return resp
	default:
	}

	body, err := page.Content()
	if err != nil {
		resp.Error = err
		return resp
	}

	resp.Body = []byte(body)

	return resp
}

func waitUntilURLContains(ctx context.Context, page playwright.Page, s string) bool {
	ticker := time.NewTicker(time.Millisecond * 150)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if strings.Contains(page.URL(), s) {
				return true
			}
		}
	}
}

func clickRejectCookiesIfRequired(page playwright.Page) error {
	// Check if we're on the new Google consent page (consent.google.com/ml)
	currentURL := page.URL()
	if strings.Contains(currentURL, "consent.google.com") {
		// Google's consent page now uses input elements instead of buttons
		// Try multiple selectors in order until one works
		selectors := []string{
			// New input-based selectors (Google's latest design)
			`input.baseButtonGm3.filledButton`,
			`input[value*="Reject"]`,
			`input[value*="reject"]`,
			`input[value*="Ablehnen"]`,
			`input[value*="ablehnen"]`,
			`form input[type="button"]:first-of-type`,
			`form input:first-of-type`,
			// Legacy button selectors (backward compatibility)
			`button:has-text("Reject all")`,
			`button:has-text("reject all")`,
			`button[aria-label*="Reject"]`,
			`button:has-text("Alle ablehnen")`,
			`button:has-text("alle ablehnen")`,
			`button:has(span[jsname="V67aGc"])`,
			`button:has(span.UywwFc-vQzf8d)`,
			`form button:first-of-type`,
			`form[action="https://consent.google.com/save"]:first-of-type button:first-of-type`,
		}

		const timeout = 500 // Short timeout per selector since we try many

		for _, sel := range selectors {
			//nolint:staticcheck // TODO replace with the new playwright API
			el, err := page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
				Timeout: playwright.Float(timeout),
			})

			if err == nil && el != nil {
				// Wait a bit before clicking to ensure element is interactive
				page.WaitForTimeout(300)

				// Click the element (input or button)
				//nolint:staticcheck // TODO replace with the new playwright API
				if err := el.Click(); err == nil {
					// Wait for navigation away from consent page
					page.WaitForTimeout(2000)
					return nil
				}
			}
		}
	} else {
		// Not on consent page, try old cookie rejection logic
		sel := `form[action="https://consent.google.com/save"]:first-of-type button:first-of-type`

		const timeout = 500

		//nolint:staticcheck // TODO replace with the new playwright API
		el, err := page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(timeout),
		})

		if err != nil {
			return nil
		}

		if el == nil {
			return nil
		}

		//nolint:staticcheck // TODO replace with the new playwright API
		return el.Click()
	}

	return nil
}

func scroll(ctx context.Context,
	page playwright.Page,
	maxDepth int,
	scrollSelector string,
) (int, error) {
	expr := `async () => {
		const el = document.querySelector("` + scrollSelector + `");
		el.scrollTop = el.scrollHeight;

		return new Promise((resolve, reject) => {
  			setTimeout(() => {
    		resolve(el.scrollHeight);
  			}, %d);
		});
	}`

	var currentScrollHeight int
	// Scroll to the bottom of the page.
	waitTime := 100.
	cnt := 0

	initialTimeout := 500
	maxWait2 := 2000.0

	for i := 0; i < maxDepth; i++ {
		// Check for cancellation before each scroll
		select {
		case <-ctx.Done():
			return cnt, ctx.Err()
		default:
		}

		cnt++
		waitTime2 := initialTimeout * cnt

		if waitTime2 > initialTimeout {
			waitTime2 = int(maxWait2)
		}

		// Scroll to the bottom of the page.
		scrollHeight, err := page.Evaluate(fmt.Sprintf(expr, waitTime2))
		if err != nil {
			return cnt, err
		}

		height, ok := scrollHeight.(int)
		if !ok {
			return cnt, fmt.Errorf("scrollHeight is not an int")
		}

		if height == currentScrollHeight {
			break
		}

		currentScrollHeight = height

		// Check for cancellation after scroll
		select {
		case <-ctx.Done():
			return cnt, ctx.Err()
		default:
		}

		waitTime *= 1.5

		if waitTime > maxWait2 {
			waitTime = maxWait2
		}

		//nolint:staticcheck // TODO replace with the new playwright API
		page.WaitForTimeout(waitTime)
	}

	return cnt, nil
}
