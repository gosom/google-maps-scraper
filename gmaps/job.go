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

	MaxDepth     int
	LangCode     string
	ExtractEmail bool
	ReviewsLimit int

	Deduper             deduper.Deduper
	ExitMonitor         exiter.Exiter
	ExtractExtraReviews bool
}

func NewGmapJob(
	id, langCode, query string,
	maxDepth int,
	extractEmail bool,
	geoCoordinates string,
	zoom int,
	reviewsLimit int,
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
		MaxDepth:     maxDepth,
		LangCode:     langCode,
		ExtractEmail: extractEmail,
		ReviewsLimit: reviewsLimit,
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

func (j *GmapJob) UseInResults() bool {
	return false
}

func (j *GmapJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	log := scrapemate.GetLoggerFromContext(ctx)

	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		return nil, nil, fmt.Errorf("could not convert to goquery document")
	}

	var next []scrapemate.IJob

	if strings.Contains(resp.URL, "/maps/place/") {
		jopts := []PlaceJobOptions{}
		if j.ExitMonitor != nil {
			jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
		}

		placeJob := NewPlaceJob(j.ID, j.LangCode, resp.URL, j.ExtractEmail, j.ExtractExtraReviews, j.ReviewsLimit, jopts...)

		next = append(next, placeJob)
	} else {
		doc.Find(`div[role=feed] div[jsaction]>a`).Each(func(_ int, s *goquery.Selection) {
			if href := s.AttrOr("href", ""); href != "" {
				jopts := []PlaceJobOptions{}
				if j.ExitMonitor != nil {
					jopts = append(jopts, WithPlaceJobExitMonitor(j.ExitMonitor))
				}

				nextJob := NewPlaceJob(j.ID, j.LangCode, href, j.ExtractEmail, j.ExtractExtraReviews, j.ReviewsLimit, jopts...)

				if j.Deduper == nil || j.Deduper.AddIfNotExists(ctx, href) {
					next = append(next, nextJob)
				}
			}
		})
	}

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesFound(len(next))
		j.ExitMonitor.IncrSeedCompleted(1)
	}

	log.Info(fmt.Sprintf("%d places found", len(next)))

	return nil, next, nil
}

func (j *GmapJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response

	fullURL := j.GetFullURL()
	fmt.Printf("Visiting URL: %s\n", fullURL)
	
	const navigationTimeout = 30000 // 30 seconds
	
	_, _ = page.SetExtraHTTPHeaders(map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	})

	pageResponse, err := page.Goto(fullURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(navigationTimeout),
	})

	if err != nil {
		resp.Error = fmt.Errorf("navigation failed: %w", err)
		fmt.Printf("Navigation error: %v\n", err)
		return resp
	}

	// Reject cookies if needed
	if err = clickRejectCookiesIfRequired(page); err != nil {
		fmt.Printf("Cookie rejection error (non-fatal): %v\n", err)
		// Don't return yet, continue with the process
	}

	const defaultTimeout = 5000

	// Wait for the URL to stabilize
	err = page.WaitForURL(page.URL(), playwright.PageWaitForURLOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(defaultTimeout),
	})

	if err != nil {
		fmt.Printf("URL stabilization error (non-fatal): %v\n", err)
		// Don't return yet, continue with the process
	}

	resp.URL = pageResponse.URL()
	resp.StatusCode = pageResponse.Status()
	resp.Headers = make(http.Header, len(pageResponse.Headers()))

	for k, v := range pageResponse.Headers() {
		resp.Headers.Add(k, v)
	}

	// When Google Maps finds only 1 place, it slowly redirects to that place's URL
	// Check for this redirection
	singlePlace := false
	feedSelector := `div[role='feed']`

	// Try multiple selectors for the feed element
	selectors := []string{
		feedSelector,
		".section-layout.section-scrollbox",
		".section-layout.section-scrollbox scrollable-y",
		".m6QErb.DxyBCb.kA9KIf.dS8AEf",
		".m6QErb.DxyBCb.kA9KIf",
		".DxyBCb.kA9KIf",
		".section-scrollbox",
	}
	
	feedFound := false
	for _, sel := range selectors {
		//nolint:staticcheck // TODO replace with the new playwright API
		feedElement, err := page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(700),
		})
		
		if err == nil && feedElement != nil {
			feedFound = true
			feedSelector = sel
			break
		}
	}

	if !feedFound {
		waitCtx, waitCancel := context.WithTimeout(ctx, time.Second*10)
		defer waitCancel()

		singlePlace = waitUntilURLContains(waitCtx, page, "/maps/place/")
		
		if !singlePlace {
			// If we're not in a single place view and couldn't find the feed selector,
			// check if we've been redirected to a search results view with a different structure
			fmt.Println("Feed not found, checking for alternative results structure...")
			
			// Try one last approach - just get the page content regardless
			singlePlace = true
		}

		waitCancel()
	}

	// Handle single place or search results list appropriately
	if singlePlace {
		resp.URL = page.URL()

		var body string

		body, err = page.Content()
		if err != nil {
			resp.Error = err
			return resp
		}

		resp.Body = []byte(body)

		return resp
	}

	// Handle search results with scrolling
	scrollCnt, err := scroll(ctx, page, j.MaxDepth, feedSelector)
	if err != nil {
		fmt.Printf("Scroll error: %v\n", err)
		// Continue to get the content anyway
	}
	
	fmt.Printf("Scrolled %d times\n", scrollCnt)

	// Get the final page content
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
	// click the cookie reject button if exists
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

func scroll(ctx context.Context,
	page playwright.Page,
	maxDepth int,
	scrollSelector string,
) (int, error) {
	// First, check if the selector exists at all
	hasElement, err := page.Evaluate(fmt.Sprintf(`() => {
		const selectors = [
			"%s",
			"div[role='feed']",
			".section-layout.section-scrollbox",
			".section-layout.section-scrollbox scrollable-y",
			".m6QErb.DxyBCb.kA9KIf.dS8AEf",
			".m6QErb.DxyBCb.kA9KIf",
			".DxyBCb.kA9KIf",
			".section-scrollbox",
			".Yr7JMd.fontTitleLarge"
		];
		
		for (const selector of selectors) {
			const el = document.querySelector(selector);
			if (el) {
				console.log("Found scrollable element: " + selector);
				return true;
			}
		}
		
		console.error("No scrollable element found with any of the selectors");
		return false;
	}`, scrollSelector))
	
	if err != nil {
		fmt.Printf("Error checking for scrollable elements: %v\n", err)
	} else if hasElement.(bool) == false {
		fmt.Println("No scrollable elements found, will try to scroll the document body")
		
		// If no elements found, just scroll the document and return
		for i := 0; i < maxDepth; i++ {
			_, err := page.Evaluate(`() => {
				window.scrollBy(0, 500);
				return document.body.scrollHeight;
			}`)
			
			if err != nil {
				return i, fmt.Errorf("failed to scroll document: %w", err)
			}
			
			// Wait between scrolls
			page.WaitForTimeout(500)
		}
		
		return maxDepth, nil
	}

	// Continue with the normal scrolling if we found elements
	expr := `async () => {
		try {
			// Try multiple potential selectors in case the UI structure has changed
			const selectors = [
				"` + scrollSelector + `",
				"div[role='feed']",
				".section-layout.section-scrollbox",
				".section-layout.section-scrollbox scrollable-y",
				".m6QErb.DxyBCb.kA9KIf.dS8AEf",
				".m6QErb.DxyBCb.kA9KIf",
				".DxyBCb.kA9KIf",
				".section-scrollbox",
				".Yr7JMd.fontTitleLarge"
			];
			
			let el = null;
			for (const selector of selectors) {
				el = document.querySelector(selector);
				if (el) {
					console.log("Using selector for scrolling: " + selector);
					break;
				}
			}
			
			// If no scrollable element is found, try the document body or return 0
			if (!el) {
				console.warn("No scrollable element found for scrolling, using document.body");
				el = document.body;
				
				if (!el) {
					console.error("No scrollable element found, not even document.body");
					return 0;
				}
			}
			
			// Log the scroll properties for debugging
			console.log("Element properties before scroll - scrollHeight: " + 
				(el.scrollHeight || "undefined") + 
				", scrollTop: " + (el.scrollTop || "undefined") + 
				", clientHeight: " + (el.clientHeight || "undefined"));
			
			// Safely attempt to scroll
			try {
				const scrollHeight = el.scrollHeight || 0;
				if (typeof el.scrollTop !== 'undefined') {
					el.scrollTop = scrollHeight;
					console.log("Scrolled element to: " + el.scrollTop);
				} else {
					// Fallback to window scrolling
					window.scrollTo(0, document.body.scrollHeight);
					console.log("Used window.scrollTo fallback");
				}
				
				return new Promise((resolve) => {
					setTimeout(() => {
						const newScrollHeight = el.scrollHeight || 0;
						console.log("New scroll height: " + newScrollHeight);
						resolve(newScrollHeight);
					}, %d);
				});
			} catch (e) {
				console.error("Error during scroll:", e);
				// Try window.scrollBy as a fallback
				window.scrollBy(0, 500);
				console.log("Used window.scrollBy fallback due to error");
				return 0;
			}
		} catch (outerError) {
			console.error("Outer error in scroll function:", outerError);
			return 0;
		}
	}`;

	var currentScrollHeight int
	// Scroll to the bottom of the page.
	waitTime := 100.
	cnt := 0

	const (
		timeout  = 500
		maxWait2 = 2000
	)

	for i := 0; i < maxDepth; i++ {
		cnt++
		waitTime2 := timeout * cnt

		if waitTime2 > timeout {
			waitTime2 = maxWait2
		}

		// Scroll to the bottom of the page.
		scrollHeight, err := page.Evaluate(fmt.Sprintf(expr, waitTime2))
		if err != nil {
			fmt.Printf("Scroll error on iteration %d: %v\n", i, err)
			
			// Try a simple fallback
			_, fallbackErr := page.Evaluate(`() => {
				window.scrollBy(0, 500);
				return true;
			}`)
			
			if fallbackErr != nil {
				return cnt, err // Return the original error if fallback also fails
			}
			
			// Wait and continue
			page.WaitForTimeout(500)
			continue
		}

		height, ok := scrollHeight.(int)
		if !ok {
			// Try to convert from float64 which is common in JavaScript returns
			if floatHeight, isFloat := scrollHeight.(float64); isFloat {
				height = int(floatHeight)
			} else {
				fmt.Printf("Unexpected scrollHeight type: %T\n", scrollHeight)
				// Continue with fallback scrolling
				_, fallbackErr := page.Evaluate(`() => {
					window.scrollBy(0, 500);
					return true;
				}`)
				
				if fallbackErr != nil {
					return cnt, fmt.Errorf("scrollHeight is not an int or float64 and fallback failed: %w", fallbackErr)
				}
				
				// Wait and continue
				page.WaitForTimeout(500)
				continue
			}
		}

		if height == 0 || height == currentScrollHeight {
			// If height is 0 or hasn't changed, try one more approach with window.scrollBy
			_, byErr := page.Evaluate(`() => {
				window.scrollBy(0, 500);
				console.log("Used window.scrollBy because height is unchanged or zero");
				return true;
			}`)
			
			if byErr != nil {
				// If even this fails, break the loop
				break
			}
		}

		currentScrollHeight = height

		select {
		case <-ctx.Done():
			return currentScrollHeight, nil
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
