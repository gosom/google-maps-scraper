package gmaps

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/kit/logging"
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

	log.Debug("gmap_job_processing",
		slog.String("job_id", j.ID),
		slog.Bool("extract_images", j.ExtractImages),
		slog.Bool("extract_email", j.ExtractEmail),
	)

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

		log.Debug("gmap_job_creating_single_place_job",
			slog.String("job_id", j.ID),
			slog.Bool("extract_images", j.ExtractImages),
			slog.String("url", resp.URL),
		)
		placeJob := NewPlaceJob(j.ID, j.LangCode, resp.URL, j.ExtractEmail, j.ExtractImages, j.ReviewsMax, jopts...)

		next = append(next, placeJob)
	} else {
		log.Debug("gmap_job_processing_search_results",
			slog.String("job_id", j.ID),
			slog.Bool("extract_images", j.ExtractImages),
		)

		// Get max results limit from ExitMonitor if available
		maxResults := 0
		if j.ExitMonitor != nil {
			maxResults = j.ExitMonitor.GetMaxResults()
			log.Debug("gmap_job_max_results_limit",
				slog.String("job_id", j.ID),
				slog.Int("max_results", maxResults),
			)
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

				log.Debug("gmap_job_creating_place_job_from_search_result",
					slog.String("job_id", j.ID),
					slog.Int("result_index", i+1),
					slog.Bool("extract_images", j.ExtractImages),
				)
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

	log.Debug("gmap_job_place_jobs_created",
		slog.String("job_id", j.ID),
		slog.Int("place_jobs_count", len(next)),
	)

	return nil, next, nil
}

// Timeout constants for feed detection after consent/navigation.
// Based on measured data: feed appears in 2.7-4.4s typical, up to 6.3s worst case.
const (
	feedWaitPrimaryTimeout   = 6000  // Primary WaitForSelector timeout (ms) — covers p99
	feedWaitExtendedTimeout  = 4000  // Extended wait if page is still loading (ms)
	consentCheckTimeout      = 500   // Timeout for checking consent overlay (ms)
	singlePlaceWaitTimeout   = 5     // Timeout for single-place URL redirect (seconds)
)

func (j *GmapJob) BrowserActions(ctx context.Context, page playwright.Page) scrapemate.Response {
	var resp scrapemate.Response

	log := scrapemate.GetLoggerFromContext(ctx)

	// Inject Google cookies for authenticated access (reviews, full data)
	if err := InjectCookiesIntoPage(page); err != nil {
		slog.Debug("search_cookies_inject_skipped", slog.Any("error", err))
	}

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		resp.Error = ctx.Err()
		return resp
	default:
	}

	pageResponse, err := page.Goto(j.GetFullURL(), playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30000),
	})

	if err != nil {
		resp.Error = err
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
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
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
		return resp
	}

	// Re-inject cookies AFTER consent handling
	if err := InjectCookiesIntoPage(page); err != nil {
		slog.Debug("job_cookies_reinject_skipped", slog.Any("error", err))
	}

	// Check for cancellation after consent handling
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

	// Wait for feed with smart tiered fallback
	feedFound, singlePlace, err := j.waitForFeedWithFallback(ctx, page, log)
	if err != nil {
		resp.Error = err
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
		return resp
	}

	log.Debug("gmap_job_current_page_url", slog.String("url", page.URL()))

	if singlePlace {
		resp.URL = page.URL()
		log.Debug("gmap_job_single_place_redirect_detected", slog.String("url", resp.URL))

		body, err := page.Content()
		if err != nil {
			resp.Error = err
			if j.ExitMonitor != nil {
				j.ExitMonitor.IncrSeedCompleted(1)
			}
			return resp
		}

		resp.Body = []byte(body)
		return resp
	}

	if !feedFound {
		resp.Error = fmt.Errorf("feed not found after all fallback attempts, url=%s", page.URL())
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
		return resp
	}

	_, err = scroll(ctx, page, j.MaxDepth, `div[role='feed']`)
	if err != nil {
		resp.Error = err
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
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
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
		return resp
	}

	resp.Body = []byte(body)

	return resp
}

// waitForFeedWithFallback implements a tiered fallback strategy for detecting the search results feed.
// Returns (feedFound, singlePlace, error).
func (j *GmapJob) waitForFeedWithFallback(ctx context.Context, page playwright.Page, log logging.Logger) (bool, bool, error) {
	sel := `div[role='feed']`

	// Step 1: Primary wait — covers the typical 2.7-4.4s range and up to 6s outliers
	log.Debug("feed_fallback_step1_primary_wait", slog.Int("timeout_ms", feedWaitPrimaryTimeout))

	//nolint:staticcheck // TODO replace with the new playwright API
	_, err := page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(feedWaitPrimaryTimeout),
	})
	if err == nil {
		log.Debug("feed_fallback_step1_success")
		return true, false, nil
	}

	log.Debug("feed_fallback_step1_failed", slog.Any("error", err))

	// Step 2: Check if it's a single-place redirect (no feed expected)
	log.Debug("feed_fallback_step2_check_single_place")
	if strings.Contains(page.URL(), "/maps/place/") {
		log.Debug("feed_fallback_step2_single_place_detected")
		return false, true, nil
	}

	// Also wait briefly for a redirect that may still be in progress
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Duration(singlePlaceWaitTimeout)*time.Second)
	defer waitCancel()

	if waitUntilURLContains(waitCtx, page, "/maps/place/") {
		log.Debug("feed_fallback_step2_single_place_redirect_detected")
		waitCancel()
		return false, true, nil
	}
	waitCancel()

	// Step 3: Check if consent overlay is still showing (click may have failed)
	log.Debug("feed_fallback_step3_check_consent_overlay")
	currentURL := page.URL()
	if strings.Contains(currentURL, "consent.google.com") {
		log.Debug("feed_fallback_step3_consent_still_showing_retrying")
		// Try clicking consent again
		if retryErr := clickRejectCookiesIfRequired(page); retryErr != nil {
			log.Debug("feed_fallback_step3_retry_consent_failed", slog.Any("error", retryErr))
		} else {
			// Wait for feed after retry
			//nolint:staticcheck
			_, err2 := page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
				Timeout: playwright.Float(feedWaitPrimaryTimeout),
			})
			if err2 == nil {
				log.Debug("feed_fallback_step3_feed_found_after_consent_retry")
				return true, false, nil
			}
		}
	}

	// Step 4: Check if we got redirected somewhere unexpected (CAPTCHA, error page)
	log.Debug("feed_fallback_step4_check_unexpected_redirect", slog.String("url", page.URL()))
	if !strings.Contains(page.URL(), "google.com/maps") {
		title, _ := page.Title()
		return false, false, fmt.Errorf("unexpected redirect away from Maps: url=%s title=%s", page.URL(), title)
	}

	// Step 5: Check if page is still loading (spinner present), wait extended time
	log.Debug("feed_fallback_step5_check_loading_state")
	//nolint:staticcheck
	spinner, _ := page.QuerySelector(`div[class*="loading"], div[class*="spinner"], img[src*="spinner"]`)
	if spinner != nil {
		log.Debug("feed_fallback_step5_spinner_detected_waiting_extended", slog.Int("timeout_ms", feedWaitExtendedTimeout))
		//nolint:staticcheck
		_, err2 := page.WaitForSelector(sel, playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(feedWaitExtendedTimeout),
		})
		if err2 == nil {
			log.Debug("feed_fallback_step5_feed_found_after_extended_wait")
			return true, false, nil
		}
	}

	// Step 6: Check if feed exists but is empty — wait for children
	log.Debug("feed_fallback_step6_check_empty_feed")
	//nolint:staticcheck
	feedEl, _ := page.QuerySelector(sel)
	if feedEl != nil {
		log.Debug("feed_fallback_step6_feed_exists_waiting_for_children")
		//nolint:staticcheck
		_, err2 := page.WaitForSelector(sel+` div[jsaction]>a`, playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(feedWaitExtendedTimeout),
		})
		if err2 == nil {
			log.Debug("feed_fallback_step6_feed_children_found")
			return true, false, nil
		}
		// Feed element exists even if empty — still valid for scroll
		log.Debug("feed_fallback_step6_feed_exists_but_empty_proceeding")
		return true, false, nil
	}

	// Step 7: Final fallback — gather diagnostics and error
	log.Debug("feed_fallback_step7_all_attempts_exhausted")
	title, _ := page.Title()
	consentVisible := strings.Contains(page.URL(), "consent.google.com")

	return false, false, fmt.Errorf(
		"feed not found after tiered fallback: url=%s title=%q consent_visible=%v",
		page.URL(), title, consentVisible,
	)
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
					// Wait for navigation away from consent page (measured: 2.7-6.3s for full load)
					page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
						State:   playwright.LoadStateDomcontentloaded,
						Timeout: playwright.Float(feedWaitPrimaryTimeout),
					})
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
		if err := el.Click(); err != nil {
			return err
		}
		// Wait for navigation away from consent page
		page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateDomcontentloaded,
			Timeout: playwright.Float(feedWaitPrimaryTimeout),
		})
		return nil
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

		heightF, ok := scrollHeight.(float64)
		if !ok {
			return cnt, fmt.Errorf("scrollHeight is not a number")
		}
		height := int(heightF)

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
