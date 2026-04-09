package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

const defaultURL = "https://www.google.com/maps/place/Wachmacher/@52.5484228,13.344701,17z/data=!4m8!3m7!1s0x47a8512da4258d8d:0x691ebb031ee3878a!8m2!3d52.5484228!4d13.3472759!9m1!1b1!16s%2Fg%2F11fs08w_7z?entry=ttu&g_ep=EgoyMDI2MDIxMS4wIKXMDSoASAFQAw%3D%3D"

func main() {
	urlFlag := flag.String("url", defaultURL, "Google Maps place URL")
	outDirFlag := flag.String("out", "", "Output directory (default: /tmp/gmaps-debug-<timestamp>)")
	headful := flag.Bool("headful", false, "Run browser headful")
	timeout := flag.Duration("timeout", 90*time.Second, "Overall timeout (navigation + extraction)")
	stateFileFlag := flag.String("state-file", filepath.Join(os.TempDir(), "gmaps-debug-storage-state.json"), "Path to persistent Playwright storage state file")
	resetState := flag.Bool("reset-state", false, "Ignore existing state file and start with a fresh context")
	cookiesFileFlag := flag.String("cookies-file", "", "Optional path to JSON cookies export to import before navigation")
	flag.Parse()

	outDir := *outDirFlag
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "gmaps-debug-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("mkdir %s: %v", outDir, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	originalURL := *urlFlag

	pw, err := playwright.Run()
	if err != nil {
		fatalf("playwright.Run: %v (did you run `./brezel-api --run-mode install-playwright`?)", err)
	}
	defer pw.Stop() //nolint:errcheck // debug tool

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(!*headful),
	})
	if err != nil {
		fatalf("chromium.Launch: %v", err)
	}
	defer browser.Close() //nolint:errcheck // debug tool

	ctxOptions := playwright.BrowserNewContextOptions{
		Locale:   playwright.String("en-US"),
		Viewport: &playwright.Size{Width: 1920, Height: 1080},
	}

	stateFile := strings.TrimSpace(*stateFileFlag)
	stateLoaded := false
	if stateFile != "" && !*resetState {
		if info, statErr := os.Stat(stateFile); statErr == nil && info.Size() > 0 {
			ctxOptions.StorageStatePath = playwright.String(stateFile)
		}
	}

	ctxBrowser, err := browser.NewContext(ctxOptions)
	if err != nil && ctxOptions.StorageStatePath != nil {
		fmt.Printf("STATE_LOAD_ERROR=%v\n", err)
		ctxOptions.StorageStatePath = nil
		ctxBrowser, err = browser.NewContext(ctxOptions)
	} else if err == nil && ctxOptions.StorageStatePath != nil {
		stateLoaded = true
	}
	if err != nil {
		fatalf("browser.NewContext: %v", err)
	}
	defer ctxBrowser.Close() //nolint:errcheck // debug tool

	if stateFile != "" {
		defer func() {
			if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
				fmt.Printf("STATE_SAVE_ERROR=%v\n", err)
				return
			}
			if _, err := ctxBrowser.StorageState(stateFile); err != nil {
				fmt.Printf("STATE_SAVE_ERROR=%v\n", err)
				return
			}
			fmt.Printf("STATE_SAVED=1\n")
			fmt.Printf("STATE_FILE=%s\n", stateFile)
		}()
	}

	fmt.Printf("STATE_LOADED=%d\n", boolToInt(stateLoaded))
	if stateFile != "" {
		fmt.Printf("STATE_FILE=%s\n", stateFile)
	}

	cookiesFile := strings.TrimSpace(*cookiesFileFlag)
	if cookiesFile != "" {
		cookies, err := loadCookiesForContext(cookiesFile)
		if err != nil {
			fmt.Printf("COOKIES_IMPORT_ERROR=%v\n", err)
		} else if len(cookies) == 0 {
			fmt.Printf("COOKIES_IMPORTED=0\n")
		} else if err := ctxBrowser.AddCookies(cookies); err != nil {
			fmt.Printf("COOKIES_IMPORT_ERROR=%v\n", err)
		} else {
			fmt.Printf("COOKIES_IMPORTED=%d\n", len(cookies))
		}
	}

	page, err := ctxBrowser.NewPage()
	if err != nil {
		fatalf("ctx.NewPage: %v", err)
	}

	cap := newResponseCapture(outDir)
	page.OnResponse(func(resp playwright.Response) {
		// Best-effort capture: this is for debugging only; never fail the run due to capture errors.
		u := resp.URL()
		if !shouldCaptureResponseURL(u) {
			return
		}
		// Only capture successful responses to reduce noise.
		if resp.Status() < 200 || resp.Status() >= 300 {
			return
		}

		req := resp.Request()
		method := ""
		postData := ""
		if req != nil {
			method = req.Method()
			if pd, err := req.PostData(); err == nil {
				postData = pd
			}
		}

		// NOTE: Never call resp.Body() inside the response event callback; Playwright dispatches events
		// on its connection goroutine. Calling into the protocol from here can deadlock.
		status := resp.Status()
		headers := resp.Headers()

		go func() {
			body, err := resp.Body()
			if err != nil || len(body) == 0 {
				return
			}
			cap.Save(u, method, status, headers, postData, body)
		}()
	})

	_, err = page.Goto(*urlFlag, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64((*timeout).Milliseconds())),
	})
	if err != nil {
		fatalf("page.Goto: %v", err)
	}

	// Google may redirect to a consent page (common in EU). Handle it so we reach the actual Maps page.
	if err := maybeHandleConsent(page, outDir); err != nil {
		fatalf("handle consent: %v", err)
	}

	// Some consent flows land on a generic /maps/@lat,lng page; once consent is stored, re-navigate to the original.
	if strings.Contains(originalURL, "/maps/place/") && !strings.Contains(page.URL(), "/maps/place/") {
		_, err = page.Goto(originalURL, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
			Timeout:   playwright.Float(float64((*timeout).Milliseconds())),
		})
		if err != nil {
			fatalf("page.Goto(after consent): %v", err)
		}
	}

	placeName := guessPlaceNameFromURL(originalURL)
	if placeName != "" {
		_ = openPlacePanelBestEffort(page, placeName)
	}

	// If we still landed on an area view (e.g. "Wedding"), try a more generic click.
	// This handles cases where Google rewrites/decodes the bundle id differently than the URL slug.
	if placeName != "" {
		h1 := getH1Text(page)
		if h1 == "Wedding" {
			_ = openPlacePanelFallback(page, placeName)
			h1 = getH1Text(page)
		}
		// If direct URL navigation doesn't open the place panel, force selection via the search box.
		// This is the most reliable way to get the "More reviews (N)" element to render under automation.
		if h1 == "" || !strings.EqualFold(h1, placeName) {
			if err := searchAndOpenPlace(page, placeName); err != nil {
				fmt.Printf("SEARCH_OPEN_ERROR=%v\n", err)
			}

			// If search executed but we're still on an area panel, the place is typically present in the
			// navigation rail as a bundle item. Clicking it reliably opens the actual place panel.
			_ = clickNavigationRailBundle(page, placeName)
			_ = waitForH1Contains(page, placeName, 15*time.Second)
		}
	}

	if isLimitedView(page) {
		fmt.Printf("LIMITED_VIEW_DETECTED=1\n")
		if placeName != "" {
			if err := searchAndOpenPlace(page, placeName); err != nil {
				fmt.Printf("LIMITED_VIEW_RECOVERY_SEARCH_ERROR=%v\n", err)
			}
			_ = clickNavigationRailBundle(page, placeName)
			_ = waitForH1Contains(page, placeName, 15*time.Second)
			if isLimitedView(page) {
				// Hard reset recovery: reopen root Maps and search place again.
				if err := recoverLimitedViewByReloadingMaps(page, placeName, *timeout); err != nil {
					fmt.Printf("LIMITED_VIEW_RECOVERY_RELOAD_ERROR=%v\n", err)
				}
			}
		}
	}
	if isLimitedView(page) {
		fmt.Printf("LIMITED_VIEW_FINAL=1\n")
	} else {
		fmt.Printf("LIMITED_VIEW_FINAL=0\n")
	}

	// Wait for something stable.
	_, _ = page.WaitForSelector("h1", playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(30_000),
	})

	_ = dismissGoogleDialogs(page)

	if b, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(filepath.Join(outDir, "page.png")),
	}); err == nil && len(b) > 0 {
		// ignore; writing to Path already handled by Playwright
	}

	html, err := page.Content()
	if err == nil && html != "" {
		_ = os.WriteFile(filepath.Join(outDir, "page.html"), []byte(html), 0o644)
	}

	domInfoAny, err := page.Evaluate(domExtractJS)
	if err != nil {
		fatalf("page.Evaluate(domExtractJS): %v", err)
	}

	domInfoJSON, _ := json.MarshalIndent(domInfoAny, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "dom_info.json"), domInfoJSON, 0o644)

	domReviewCount := extractReviewCountFromDOMInfo(domInfoAny)
	fmt.Printf("OUT_DIR=%s\n", outDir)
	fmt.Printf("DOM_REVIEW_COUNT=%d\n", domReviewCount)

	rawBytes, err := extractScraperJSON(ctx, page)
	if err != nil {
		fmt.Printf("SCRAPER_EXTRACT_ERROR=%v\n", err)
	} else {
		_ = os.WriteFile(filepath.Join(outDir, "scraper_extracted.json"), rawBytes, 0o644)

		var root any
		if err := json.Unmarshal(rawBytes, &root); err != nil {
			fmt.Printf("SCRAPER_UNMARSHAL_ERROR=%v\n", err)
		} else {
			jd, ok := root.([]any)
			if !ok {
				fmt.Printf("SCRAPER_TOP_TYPE=%T\n", root)
			} else {
				fmt.Printf("SCRAPER_TOP_LEN=%d\n", len(jd))
				if len(jd) > 6 {
					darray, ok := jd[6].([]any)
					if !ok {
						fmt.Printf("SCRAPER_JD6_TYPE=%T\n", jd[6])
					} else {
						fmt.Printf("SCRAPER_JD6_LEN=%d\n", len(darray))
						dumpDarray4(darray)
						if domReviewCount > 0 {
							paths := findFloat64Paths(darray, float64(domReviewCount), 25)
							if len(paths) > 0 {
								fmt.Printf("JSON_MATCH_PATHS_FOR_DOM_REVIEW_COUNT=%s\n", strings.Join(paths, ","))
							} else {
								fmt.Printf("JSON_MATCH_PATHS_FOR_DOM_REVIEW_COUNT=\n")
							}
						}
					}
				}
			}
		}
	}

	// DOM-driven review scraping: scroll place panel to load the "More reviews (N)" button,
	// click it, then scroll the reviews list until no more new reviews appear.
	totalReviews, clickedReviews, err := clickMoreReviewsButtonAndExtractTotal(page)
	if err != nil {
		fmt.Printf("MORE_REVIEWS_ERROR=%v\n", err)
	} else if totalReviews > 0 {
		fmt.Printf("MORE_REVIEWS_TOTAL=%d\n", totalReviews)
	}

	if clickedReviews {
		revs, err := scrapeAllReviewsFromDOM(ctx, page, totalReviews)
		if err != nil {
			fmt.Printf("DOM_SCRAPE_REVIEWS_ERROR=%v\n", err)
		}
		revsJSON, _ := json.MarshalIndent(revs, "", "  ")
		_ = os.WriteFile(filepath.Join(outDir, "dom_reviews.json"), revsJSON, 0o644)
		fmt.Printf("DOM_REVIEWS_SCRAPED=%d\n", len(revs))
	}

	select {
	case <-ctx.Done():
		// fallthrough; exit normally so the files remain.
	default:
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func loadCookiesForContext(path string) ([]playwright.OptionalCookie, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cookies file: %w", err)
	}

	var cookieMaps []map[string]any

	// Format A: top-level array of cookie objects.
	if err := json.Unmarshal(raw, &cookieMaps); err != nil || len(cookieMaps) == 0 {
		// Format B: object with a cookies array.
		var envelope map[string]any
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("unsupported cookies JSON format")
		}
		cookieMaps = asMapSlice(envelope["cookies"])
	}

	if len(cookieMaps) == 0 {
		return nil, nil
	}

	out := make([]playwright.OptionalCookie, 0, len(cookieMaps))
	for _, m := range cookieMaps {
		name := strField(m, "name")
		value := strField(m, "value")
		if name == "" {
			continue
		}

		c := playwright.OptionalCookie{
			Name:  name,
			Value: value,
		}

		urlField := strField(m, "url")
		domain := strField(m, "domain")
		path := strField(m, "path")
		if path == "" {
			path = "/"
		}

		switch {
		case urlField != "":
			c.URL = playwright.String(urlField)
		case domain != "":
			c.Domain = playwright.String(domain)
			c.Path = playwright.String(path)
		default:
			continue
		}

		if expires, ok := floatField(m, "expires", "expirationDate"); ok && expires > 0 {
			c.Expires = playwright.Float(expires)
		}
		if httpOnly, ok := boolField(m, "httpOnly"); ok {
			c.HttpOnly = playwright.Bool(httpOnly)
		}
		if secure, ok := boolField(m, "secure"); ok {
			c.Secure = playwright.Bool(secure)
		}
		if sameSite := parseSameSite(strField(m, "sameSite")); sameSite != nil {
			c.SameSite = sameSite
		}

		out = append(out, c)
	}

	return out, nil
}

func asMapSlice(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, el := range list {
		m, ok := el.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, m)
	}
	return out
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if strings.TrimSpace(t) != "" {
				return strings.TrimSpace(t)
			}
		case json.Number:
			s := t.String()
			if strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func floatField(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return t, true
		case float32:
			return float64(t), true
		case int:
			return float64(t), true
		case int64:
			return float64(t), true
		case json.Number:
			if f, err := t.Float64(); err == nil {
				return f, true
			}
		case string:
			s := strings.TrimSpace(t)
			if s == "" {
				continue
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func boolField(m map[string]any, key string) (bool, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		switch s {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

func parseSameSite(v string) *playwright.SameSiteAttribute {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "strict":
		return playwright.SameSiteAttributeStrict
	case "lax":
		return playwright.SameSiteAttributeLax
	case "none", "no_restriction":
		return playwright.SameSiteAttributeNone
	default:
		return nil
	}
}

type DOMReview struct {
	ID                string   `json:"id"`
	Author            string   `json:"author"`
	ProfileLink       string   `json:"profile_link"`
	ProfilePicture    string   `json:"profile_picture"`
	Rating            float64  `json:"rating"`
	Time              string   `json:"time"`
	Text              string   `json:"text"`
	Images            []string `json:"images"`
	ReviewURL         string   `json:"review_url"`
	OwnerResponse     string   `json:"owner_response"`
	OwnerResponseTime string   `json:"owner_response_time"`
}

type responseCapture struct {
	outDir string
	mu     sync.Mutex
	n      int
}

func newResponseCapture(outDir string) *responseCapture {
	return &responseCapture{outDir: outDir}
}

func shouldCaptureResponseURL(u string) bool {
	// Keep this narrow to avoid dumping thousands of assets.
	switch {
	case strings.Contains(u, "/maps/preview/place"):
		return true
	case strings.Contains(u, "/maps/rpc/"):
		return true
	case strings.Contains(u, "listugcposts"):
		return true
	default:
		return false
	}
}

func (c *responseCapture) Save(u, method string, status int, headers map[string]string, postData string, body []byte) {
	c.mu.Lock()
	c.n++
	n := c.n
	c.mu.Unlock()

	kind := "resp"
	if strings.Contains(u, "/maps/preview/place") {
		kind = "preview-place"
	} else if strings.Contains(u, "listugcposts") {
		kind = "listugcposts"
	} else if strings.Contains(u, "/maps/rpc/") {
		kind = "maps-rpc"
	}

	base := fmt.Sprintf("%04d-%s-%s", n, kind, sanitizeFilename(u, 80))
	bodyPath := filepath.Join(c.outDir, base+".body")
	metaPath := filepath.Join(c.outDir, base+".meta.json")

	// Avoid huge postData files; keep a truncated version in metadata.
	if len(postData) > 2000 {
		postData = postData[:2000] + "...(truncated)"
	}

	meta := map[string]any{
		"url":        u,
		"method":     method,
		"status":     status,
		"headers":    headers,
		"post_data":  postData,
		"body_bytes": len(body),
	}

	if b, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = os.WriteFile(metaPath, b, 0o644)
	}
	_ = os.WriteFile(bodyPath, body, 0o644)
}

var nonFileCharRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "https://", "")
	s = strings.ReplaceAll(s, "http://", "")
	s = nonFileCharRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		s = "empty"
	}
	if len(s) > max {
		s = s[:max]
	}
	return s
}

func guessPlaceNameFromURL(u string) string {
	// Best-effort extraction from /maps/place/<NAME>/...
	// Example: https://www.google.com/maps/place/Wachmacher/@...
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}

	const prefix = "/maps/place/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(parsed.Path, prefix)
	if rest == "" {
		return ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		return ""
	}
	name := parts[0]
	if name == "" || strings.HasPrefix(name, "@") {
		return ""
	}
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	// Maps place slugs frequently use '+' for spaces in the path segment.
	name = strings.ReplaceAll(name, "+", " ")
	return strings.TrimSpace(name)
}

func clickMoreReviewsButtonAndExtractTotal(page playwright.Page) (total int, clicked bool, err error) {
	// We often need to scroll the place panel before the "More reviews (N)" CTA is rendered.
	const maxScrolls = 30

	for i := 0; i < maxScrolls; i++ {
		ariaAny, err := page.Evaluate(`() => {
      const clickAndGetAria = (el) => {
        if (!el) return '';
        const aria = (el.getAttribute('aria-label') || '').trim();
        try { el.scrollIntoView({ block: 'center', inline: 'nearest' }); } catch (_) {}
        try { el.click(); } catch (_) {}
        return aria;
      };

      const selectors = [
        'button[aria-label^=\"More reviews\"]',
        'button[aria-label*=\"More reviews\"]',
        'button[jsaction*=\"pane.rating.moreReviews\"]',
      ];
      for (const sel of selectors) {
        const btns = Array.from(document.querySelectorAll(sel));
        for (const btn of btns) {
          const aria = (btn.getAttribute('aria-label') || '').trim();
          if (!aria) continue;
          if (!/more\s+reviews/i.test(aria) && !/reviews?\s*\(/i.test(aria)) continue;
          return clickAndGetAria(btn);
        }
      }

      // Some variants render the label as span text and click is handled by parent button.
      const spans = Array.from(document.querySelectorAll('span'));
      for (const sp of spans) {
        const txt = (sp.textContent || '').trim();
        if (!/^More reviews\s*\([0-9.,]+\)$/i.test(txt)) continue;
        const btn = sp.closest('button');
        if (!btn) continue;
        return clickAndGetAria(btn);
      }

      return '';
    }`)
		if err == nil {
			if aria, ok := ariaAny.(string); ok && strings.TrimSpace(aria) != "" {
				fmt.Printf("MORE_REVIEWS_ARIA=%s\n", aria)
				// Wait for reviews dialog/list content to appear.
				_, _ = page.WaitForSelector(`.jftiEf[data-review-id], [data-review-id].jftiEf, [data-review-id]`, playwright.PageWaitForSelectorOptions{
					Timeout: playwright.Float(30_000),
				})
				n := parseReviewCount(aria)
				return n, true, nil
			}
		}

		moved, _ := scrollLeftPanelOneStep(page)
		_ = moved
		time.Sleep(650 * time.Millisecond)
	}

	return 0, false, fmt.Errorf("could not find/click the More reviews button after scrolling")
}

func scrollLeftPanelOneStep(page playwright.Page) (bool, error) {
	// Scroll the main left panel container (place panel). We pick the best scrollable element in the left half
	// of the viewport and prefer one that contains the place <h1>.
	resAny, err := page.Evaluate(`() => {
      const winW = window.innerWidth || 0;
      const candidates = Array.from(document.querySelectorAll('div')).filter(el => {
        if (!el) return false;
        if ((el.scrollHeight || 0) <= (el.clientHeight || 0) + 80) return false;
        const r = el.getBoundingClientRect();
        if (!r) return false;
        if (r.width < 260 || r.height < 260) return false;
        if (r.left > winW * 0.65) return false;
        return true;
      });

      let best = null;
      let bestScore = -1;
      for (const el of candidates) {
        const r = el.getBoundingClientRect();
        const hasH1 = !!el.querySelector('h1');
        const score = (hasH1 ? 1e9 : 0) + (r.height * 10) + r.width;
        if (score > bestScore) { best = el; bestScore = score; }
      }
      if (!best) return {ok:false, moved:false, reason:'no_scrollable'};

      const before = best.scrollTop || 0;
      const delta = Math.floor((best.clientHeight || 600) * 0.85);
      best.scrollTop = Math.min(before + delta, (best.scrollHeight || before));
      const after = best.scrollTop || 0;
      return {ok:true, moved: after > before, before, after, clientHeight: best.clientHeight || 0, scrollHeight: best.scrollHeight || 0};
    }`)
	if err != nil {
		return false, err
	}
	m, ok := resAny.(map[string]any)
	if !ok {
		return false, nil
	}
	mv, _ := m["moved"].(bool)
	return mv, nil
}

func scrapeAllReviewsFromDOM(ctx context.Context, page playwright.Page, total int) ([]DOMReview, error) {
	// Ensure at least one review-like node exists before we start.
	_, _ = page.WaitForSelector(`.jftiEf[data-review-id], [data-review-id].jftiEf, [data-review-id]`, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(30_000),
	})

	byID := map[string]DOMReview{}
	noProgress := 0
	const maxNoProgress = 10

	// Upper bound to avoid infinite loops if Google changes the UI.
	maxIters := 800
	if total > 0 && total*4 > maxIters {
		maxIters = total * 4
	}

	for i := 0; i < maxIters; i++ {
		select {
		case <-ctx.Done():
			return mapToSlice(byID), ctx.Err()
		default:
		}

		batch, err := extractDOMReviews(page)
		if err == nil {
			if i < 3 {
				fmt.Printf("DOM_BATCH_%d=%d\n", i+1, len(batch))
			}
			added := 0
			for _, r := range batch {
				if r.ID == "" {
					continue
				}
				if _, ok := byID[r.ID]; ok {
					continue
				}
				byID[r.ID] = r
				added++
			}
			if added > 0 {
				noProgress = 0
			} else {
				noProgress++
			}
		} else {
			if i < 3 {
				fmt.Printf("DOM_BATCH_%d_ERROR=%v\n", i+1, err)
			}
			noProgress++
		}

		if total > 0 && len(byID) >= total {
			break
		}

		moved, _ := scrollReviewsOneStep(page)
		if !moved {
			noProgress++
		}

		if noProgress >= maxNoProgress {
			break
		}

		time.Sleep(650 * time.Millisecond)
	}

	return mapToSlice(byID), nil
}

func mapToSlice(m map[string]DOMReview) []DOMReview {
	out := make([]DOMReview, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func extractDOMReviews(page playwright.Page) ([]DOMReview, error) {
	anyBatch, err := page.Evaluate(domReviewsExtractJS)
	if err != nil {
		return nil, err
	}
	// Convert via JSON for simplicity (this is a debug tool).
	b, err := json.Marshal(anyBatch)
	if err != nil {
		return nil, err
	}
	var out []DOMReview
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func scrollReviewsOneStep(page playwright.Page) (bool, error) {
	resAny, err := page.Evaluate(`() => {
      let nodes = Array.from(document.querySelectorAll('.jftiEf[data-review-id], [data-review-id].jftiEf'));
      if (nodes.length === 0) {
        nodes = Array.from(document.querySelectorAll('[data-review-id]'));
      }
      if (nodes.length === 0) return {ok:false, moved:false, reason:'no_reviews'};

      const isScrollable = (el) => (el && (el.scrollHeight || 0) > (el.clientHeight || 0) + 80);

      // Prefer the nearest scrollable ancestor of the first review.
      let scroller = null;
      let el = nodes[0].parentElement;
      for (let i = 0; i < 20 && el; i++, el = el.parentElement) {
        if (isScrollable(el)) { scroller = el; break; }
      }

      if (!scroller) {
        // Fallback: choose scrollable element containing the most reviews.
        const candidates = Array.from(document.querySelectorAll('div')).filter(isScrollable);
        let best = null, bestScore = -1;
        for (const c of candidates) {
          let cnt = c.querySelectorAll('.jftiEf[data-review-id], [data-review-id].jftiEf').length;
          if (cnt === 0) cnt = c.querySelectorAll('[data-review-id]').length;
          if (cnt === 0) continue;
          const score = (cnt * 1000) + (c.clientHeight || 0);
          if (score > bestScore) { best = c; bestScore = score; }
        }
        scroller = best;
      }

      if (!scroller) return {ok:false, moved:false, reason:'no_scroller'};
      const before = scroller.scrollTop || 0;
      const delta = Math.floor((scroller.clientHeight || 600) * 0.85);
      scroller.scrollTop = Math.min(before + delta, (scroller.scrollHeight || before));
      const after = scroller.scrollTop || 0;
      return {ok:true, moved: after > before, before, after, clientHeight: scroller.clientHeight || 0, scrollHeight: scroller.scrollHeight || 0, reviewsInDom: nodes.length};
    }`)
	if err != nil {
		return false, err
	}
	m, ok := resAny.(map[string]any)
	if !ok {
		return false, nil
	}
	mv, _ := m["moved"].(bool)
	return mv, nil
}

func openPlacePanelBestEffort(page playwright.Page, placeName string) error {
	// In some cases Maps lands on an area view (e.g., "Wedding") while the place is present
	// in the navigation rail as a bundle item. Clicking it opens the place panel.
	selector := fmt.Sprintf("button[data-bundle-id=\"%s\"]", cssEscapeAttr(placeName))
	loc := page.Locator(selector).First()

	// NOTE: Avoid IsVisible() here; it calls into the Playwright protocol and can hang on heavy pages.
	// Just try a click with a short timeout.
	err := loc.Click(playwright.LocatorClickOptions{
		Timeout: playwright.Float(2_000),
	})
	if err != nil {
		return nil
	}

	// Wait (best-effort) for the place title h1 to appear.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		okAny, _ := page.Evaluate(`(name) => {
      const hs = Array.from(document.querySelectorAll('h1'));
      return hs.some(h => (h.textContent || '').trim() === name);
    }`, placeName)
		if ok, _ := okAny.(bool); ok {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	return nil
}

func cssEscapeAttr(s string) string {
	// Minimal escaping for attribute selectors; we wrap in double quotes, so escape backslash + quotes.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func openPlacePanelFallback(page playwright.Page, placeName string) error {
	// Try to click a left-rail bundle item that looks like the target place name.
	// We don't rely on brittle classnames; instead we scan for a visible button with data-bundle-id.
	_, _ = page.Evaluate(`() => { try { window.scrollTo(0, 0); } catch (_) {} }`)

	_, err := page.Evaluate(`(name) => {
    const want = (name || '').toLowerCase();
    const btns = Array.from(document.querySelectorAll('button[data-bundle-id]'));
    for (const b of btns) {
      const bid = (b.getAttribute('data-bundle-id') || '').toLowerCase();
      const txt = (b.textContent || '').trim().toLowerCase();
      if (!bid && !txt) continue;
      if (want && (bid === want || txt === want || bid.includes(want) || txt.includes(want))) {
        try { b.click(); return true; } catch (_) {}
      }
    }
    // Fallback: click the first bundle button on the rail (often the place item) if only one exists.
    if (btns.length === 1) { try { btns[0].click(); return true; } catch (_) {} }
    return false;
  }`, placeName)
	if err != nil {
		return nil
	}

	// Wait briefly for a place title to appear.
	_, _ = page.WaitForSelector("h1", playwright.PageWaitForSelectorOptions{Timeout: playwright.Float(10_000)})
	return nil
}

func clickNavigationRailBundle(page playwright.Page, placeName string) error {
	placeName = strings.TrimSpace(placeName)
	if placeName == "" {
		return nil
	}
	alts := []string{
		placeName,
		strings.ReplaceAll(placeName, "+", " "),
		strings.ReplaceAll(placeName, " ", "+"),
	}

	// Prefer JS click: locator-based clicks sometimes fail when the rail is mid-render.
	okAny, err := page.Evaluate(`(names) => {
    try {
      const want = (Array.isArray(names) ? names : []).map(v => String(v || '').trim().toLowerCase()).filter(Boolean);
      const btns = Array.from(document.querySelectorAll('button[data-bundle-id]'));
      for (const b of btns) {
        const bid = (b.getAttribute('data-bundle-id') || '').trim().toLowerCase();
        const txt = (b.textContent || '').trim().toLowerCase();
        if (!bid && !txt) continue;
        for (const n of want) {
          if (bid === n || txt === n || bid.includes(n) || txt.includes(n) || n.includes(bid) || n.includes(txt)) {
            b.click();
            return true;
          }
        }
      }
      return false;
    } catch (_) {
      return false;
    }
  }`, alts)
	if err == nil {
		if ok, _ := okAny.(bool); ok {
			return nil
		}
	}

	// Fallback: locator click.
	for _, alt := range alts {
		selector := fmt.Sprintf("button[data-bundle-id=\"%s\"]", cssEscapeAttr(strings.TrimSpace(alt)))
		loc := page.Locator(selector).First()
		if err := loc.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(2_000)}); err == nil {
			return nil
		}
	}
	return nil
}

func getH1Text(page playwright.Page) string {
	h1Any, err := page.Evaluate(`() => document.querySelector('h1')?.textContent?.trim() || ''`)
	if err != nil {
		return ""
	}
	s, _ := h1Any.(string)
	return strings.TrimSpace(s)
}

func waitForH1Contains(page playwright.Page, want string, timeout time.Duration) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h1 := strings.ToLower(getH1Text(page))
		if h1 != "" && (h1 == want || strings.Contains(h1, want)) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func isLimitedView(page playwright.Page) bool {
	anyVal, err := page.Evaluate(`() => {
    const bodyText = (document.body?.innerText || '').toLowerCase();
    if (bodyText.includes('limited view of google maps')) return true;
    if (document.querySelector('button[aria-label*="limited view"]')) return true;
    return false;
  }`)
	if err != nil {
		return false
	}
	ok, _ := anyVal.(bool)
	return ok
}

func recoverLimitedViewByReloadingMaps(page playwright.Page, placeName string, timeout time.Duration) error {
	_, err := page.Goto("https://www.google.com/maps?authuser=0&hl=en", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(timeout.Milliseconds())),
	})
	if err != nil {
		return fmt.Errorf("goto root maps: %w", err)
	}

	_ = dismissGoogleDialogs(page)

	if err := searchAndOpenPlace(page, placeName); err != nil {
		return fmt.Errorf("search after root reload: %w", err)
	}
	_ = clickNavigationRailBundle(page, placeName)
	_ = waitForH1Contains(page, placeName, 20*time.Second)

	return nil
}

func searchAndOpenPlace(page playwright.Page, placeName string) error {
	placeName = strings.TrimSpace(placeName)
	if placeName == "" {
		return nil
	}

	// Give Maps a moment; the search box isn't always immediately ready after a redirect.
	time.Sleep(500 * time.Millisecond)

	searchSelectors := []string{
		`input[aria-label="Search Google Maps"]`,
		`input[aria-label="Search"]`,
		`input#searchboxinput`,
		`input[name="q"]`,
		`[role="combobox"] input`,
	}

	var lastErr error
	for _, sel := range searchSelectors {
		loc := page.Locator(sel).First()
		if err := loc.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(5_000)}); err != nil {
			lastErr = err
			continue
		}
		if err := loc.Fill(placeName, playwright.LocatorFillOptions{Timeout: playwright.Float(5_000)}); err != nil {
			lastErr = err
			continue
		}

		// Give suggestions a moment to refresh.
		time.Sleep(300 * time.Millisecond)

		// Enter typically opens the place panel directly for unique place queries.
		_ = loc.Press("Enter", playwright.LocatorPressOptions{Timeout: playwright.Float(5_000)})
		if waitForH1Contains(page, placeName, 20*time.Second) {
			return nil
		}

		// Try clicking the first suggestion (Maps uses div[data-suggestion-index] with jsaction handlers).
		_, _ = page.Evaluate(`(name) => {
      const want = (name || '').toLowerCase();
      const sugs = Array.from(document.querySelectorAll('div[data-suggestion-index]'));
      for (const s of sugs) {
        const txt = (s.textContent || '').trim().toLowerCase();
        if (!txt) continue;
        if (want && !txt.includes(want)) continue;
        try { s.click(); return true; } catch (_) {}
      }
      // Fallback: click suggestion index 0 even if text didn't match (recent history often contains the place).
      const s0 = document.querySelector('div[data-suggestion-index=\"0\"]');
      if (s0) { try { s0.click(); return true; } catch (_) {} }
      return false;
    }`, placeName)
		if waitForH1Contains(page, placeName, 20*time.Second) {
			return nil
		}

		// If Enter didn't select a place, pick the first suggestion.
		_ = page.Keyboard().Press("ArrowDown")
		_ = page.Keyboard().Press("Enter")
		if waitForH1Contains(page, placeName, 20*time.Second) {
			return nil
		}

		// If we're in a results list, click the first visible element that looks like the target.
		_, _ = page.Evaluate(`(name) => {
      const want = (name || '').toLowerCase();
      const isClickable = (el) => {
        if (!el) return false;
        const r = el.getBoundingClientRect();
        if (!r || r.width < 40 || r.height < 20) return false;
        if (r.left > (window.innerWidth || 0) * 0.7) return false;
        if (r.top < 0 || r.top > (window.innerHeight || 0)) return false;
        return true;
      };
      const els = Array.from(document.querySelectorAll('a, button, [role="button"], div[data-suggestion-index]'));
      for (const el of els) {
        const txt = (el.textContent || '').trim().toLowerCase();
        if (!txt || !want) continue;
        if (!txt.includes(want)) continue;
        if (!isClickable(el)) continue;
        try { el.click(); return true; } catch (_) {}
      }
      return false;
    }`, placeName)
		if waitForH1Contains(page, placeName, 20*time.Second) {
			return nil
		}
	}

	if lastErr != nil {
		return fmt.Errorf("searchAndOpenPlace: could not interact with search box: %v", lastErr)
	}
	return fmt.Errorf("searchAndOpenPlace: could not find search input")
}

func normalizeRaw(v any) []byte {
	switch t := v.(type) {
	case string:
		trimmed := strings.TrimSpace(t)
		trimmed = strings.TrimPrefix(trimmed, ")]}'")
		trimmed = strings.TrimPrefix(trimmed, ")]}'\n")
		return []byte(strings.TrimSpace(trimmed))
	case []byte:
		return t
	default:
		b, _ := json.Marshal(t)
		return b
	}
}

func extractScraperJSON(ctx context.Context, page playwright.Page) ([]byte, error) {
	const (
		maxAttempts   = 25
		retryInterval = 250 * time.Millisecond
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rawAny, err := page.Evaluate(scraperExtractJS)
		if err != nil {
			time.Sleep(retryInterval)
			continue
		}

		rawBytes := normalizeRaw(rawAny)
		rawStr := strings.TrimSpace(string(rawBytes))
		if rawStr == "" || rawStr == "null" {
			time.Sleep(retryInterval)
			continue
		}

		// Mirrors gmaps/place.go: only accept payloads that look like JSON arrays/objects.
		if strings.HasPrefix(rawStr, "[") || strings.HasPrefix(rawStr, "{") {
			return []byte(rawStr), nil
		}

		time.Sleep(retryInterval)
	}

	return nil, fmt.Errorf("no valid JSON array/object after retries (last url=%s)", page.URL())
}

func dumpDarray4(darray []any) {
	el4, ok := darray[4].([]any)
	if !ok {
		fmt.Printf("DARRAY_4_TYPE=%T\n", darray[4])
		return
	}

	fmt.Printf("DARRAY_4_LEN=%d\n", len(el4))
	for i := range el4 {
		switch v := el4[i].(type) {
		case float64:
			fmt.Printf("DARRAY_4[%d]=float64:%v\n", i, v)
		case string:
			fmt.Printf("DARRAY_4[%d]=string:%s\n", i, oneLine(v, 140))
		case nil:
			fmt.Printf("DARRAY_4[%d]=nil\n", i)
		default:
			fmt.Printf("DARRAY_4[%d]=%T\n", i, v)
		}
	}
}

func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func findFloat64Paths(v any, target float64, limit int) []string {
	var out []string
	var walk func(cur any, path string)
	walk = func(cur any, path string) {
		if len(out) >= limit {
			return
		}
		switch t := cur.(type) {
		case float64:
			if t == target {
				out = append(out, path)
			}
		case []any:
			for i := range t {
				walk(t[i], fmt.Sprintf("%s[%d]", path, i))
				if len(out) >= limit {
					return
				}
			}
		case map[string]any:
			for k, vv := range t {
				walk(vv, fmt.Sprintf("%s[%q]", path, k))
				if len(out) >= limit {
					return
				}
			}
		}
	}
	walk(v, "")
	return out
}

func extractReviewCountFromDOMInfo(domInfo any) int {
	// domInfo comes from domExtractJS; we scan selector hits + candidates for a "N reviews" pattern.
	asMap, ok := domInfo.(map[string]any)
	if !ok {
		return 0
	}

	tryFields := []string{"metaReviewCount"}
	for _, f := range tryFields {
		if raw, ok := asMap[f].(string); ok && raw != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
				return n
			}
		}
	}

	if fromSelectors, ok := asMap["fromSelectors"].([]any); ok {
		for _, it := range fromSelectors {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if aria, ok := m["aria"].(string); ok {
				if n := parseReviewCount(aria); n > 0 {
					return n
				}
			}
			if text, ok := m["text"].(string); ok {
				if n := parseReviewCount(text); n > 0 {
					return n
				}
			}
		}
	}

	if candidates, ok := asMap["candidates"].([]any); ok {
		for _, it := range candidates {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if aria, ok := m["aria"].(string); ok {
				if n := parseReviewCount(aria); n > 0 {
					return n
				}
			}
		}
	}

	return 0
}

var reviewCountRe = regexp.MustCompile(`(?i)([0-9][0-9,\.]*)\s*(reviews?|ratings?|rezensionen|bewertungen)\b`)
var reviewCountReParen = regexp.MustCompile(`(?i)(reviews?|ratings?|rezensionen|bewertungen)[^0-9]*\(([0-9][0-9,\.]*)\)`)

func parseReviewCount(s string) int {
	s = strings.TrimSpace(s)
	m := reviewCountRe.FindStringSubmatch(s)
	if len(m) >= 2 {
		digits := regexp.MustCompile(`[^0-9]`).ReplaceAllString(m[1], "")
		if digits == "" {
			return 0
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return 0
		}
		return n
	}

	m = reviewCountReParen.FindStringSubmatch(s)
	if len(m) < 3 {
		return 0
	}

	digits := regexp.MustCompile(`[^0-9]`).ReplaceAllString(m[2], "")
	if digits == "" {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return n
}

// dismissGoogleDialogs handles a couple of common dialogs (sign-in, etc.) that may block content.
func dismissGoogleDialogs(page playwright.Page) error {
	// "How your posts appear" dialog with OK button.
	okButtonSelectors := []string{
		`button:has-text("OK")`,
		`button:has-text("Ok")`,
		`button[aria-label="OK"]`,
		`button[aria-label="Ok"]`,
	}
	for _, selector := range okButtonSelectors {
		okButton := page.Locator(selector).First()
		visible, err := okButton.IsVisible()
		if err == nil && visible {
			_ = okButton.Click()
			time.Sleep(500 * time.Millisecond)
			break
		}
	}

	// "Sign in to your Google Account" dialog close button.
	closeButtonSelectors := []string{
		`button[aria-label="Close"]`,
		`button[aria-label="close"]`,
		`button[aria-label="Schließen"]`,
		`button:has([aria-label="Close"])`,
		`div[role="dialog"] button:has-text("×")`,
		`div[role="dialog"] button[class*="close"]`,
	}
	for _, selector := range closeButtonSelectors {
		closeButton := page.Locator(selector).First()
		visible, err := closeButton.IsVisible()
		if err == nil && visible {
			_ = closeButton.Click()
			time.Sleep(300 * time.Millisecond)
			return nil
		}
	}

	return nil
}

func maybeHandleConsent(page playwright.Page, outDir string) error {
	if !strings.Contains(page.URL(), "consent.google.com") {
		return nil
	}

	// Dump the consent page so we can adjust selectors if Google changes the flow again.
	// (This is a debug tool; keep artifacts even on failure.)
	if b, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(filepath.Join(outDir, "consent.png")),
	}); err == nil && len(b) > 0 {
		// ignore; writing to Path already handled by Playwright
	}
	if html, err := page.Content(); err == nil && html != "" {
		_ = os.WriteFile(filepath.Join(outDir, "consent.html"), []byte(html), 0o644)
	}
	{
		var lines []string
		for _, fr := range page.Frames() {
			lines = append(lines, fr.URL())
		}
		if len(lines) > 0 {
			_ = os.WriteFile(filepath.Join(outDir, "consent_frames.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		}
	}

	// Prefer "Accept all" to avoid Google Maps "limited view" mode; fall back to "Reject all".
	clickFirst := func(selectors []string) (bool, string) {
		for _, fr := range page.Frames() {
			for _, selector := range selectors {
				loc := fr.Locator(selector).First()
				err := loc.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(2_000)})
				if err == nil {
					return true, selector
				}
			}
		}
		return false, ""
	}

	rejectSelectors := []string{
		// Consent UI frequently uses <input type="submit" value="..."> instead of <button>.
		`input[type="submit"][value="Reject all"]`,
		`input[type="submit"][value="Reject"]`,
		`input[type="submit"][value="Alle ablehnen"]`,
		`input[type="submit"][value="Ablehnen"]`,

		`button:has-text("Reject all")`,
		`button:has-text("Reject")`,
		`button:has-text("Alle ablehnen")`,
		`button:has-text("Ablehnen")`,
		`button[aria-label="Reject all"]`,
		`button[aria-label="Alle ablehnen"]`,
		`[role="button"]:has-text("Reject all")`,
		`[role="button"]:has-text("Alle ablehnen")`,
	}
	acceptSelectors := []string{
		`input[type="submit"][value="Accept all"]`,
		`input[type="submit"][value="Accept"]`,
		`input[type="submit"][value="I agree"]`,
		`input[type="submit"][value="Agree"]`,
		`input[type="submit"][value="Alle akzeptieren"]`,
		`input[type="submit"][value="Akzeptieren"]`,

		`button:has-text("Accept all")`,
		`button:has-text("Accept")`,
		`button:has-text("I agree")`,
		`button:has-text("Agree")`,
		`button:has-text("Alle akzeptieren")`,
		`button:has-text("Akzeptieren")`,
		`button[aria-label="Accept all"]`,
		`button[aria-label="Alle akzeptieren"]`,
		`[role="button"]:has-text("Accept all")`,
		`[role="button"]:has-text("Alle akzeptieren")`,
	}

	// Wait a moment for consent UI to render.
	_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateDomcontentloaded})

	// Sometimes buttons are below the fold.
	_, _ = page.Evaluate(`() => { try { window.scrollTo(0, document.body.scrollHeight); } catch (_) {} }`)

	// Try a couple of passes: some flows have multiple screens.
	consentAction := "none"
	for pass := 0; pass < 3; pass++ {
		// Prefer accepting cookies because reject often leads to Maps "limited view".
		if ok, sel := clickFirst(acceptSelectors); ok {
			consentAction = "accept:" + sel
			time.Sleep(500 * time.Millisecond)
		} else if pass == 2 {
			// Debug fallback only: if we couldn't accept at all, try reject so flow can continue.
			if ok, sel := clickFirst(rejectSelectors); ok {
				consentAction = "reject:" + sel
				time.Sleep(500 * time.Millisecond)
			}
		}
		if consentAction != "none" {
			time.Sleep(500 * time.Millisecond)
		}
		// Break early if we've left the consent domain.
		if !strings.Contains(page.URL(), "consent.google.com") {
			break
		}
		time.Sleep(700 * time.Millisecond)
	}
	fmt.Printf("CONSENT_ACTION=%s\n", consentAction)

	// Wait (best-effort) until we're no longer on the consent domain.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(page.URL(), "consent.google.com") {
			_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State: playwright.LoadStateNetworkidle,
			})
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("still on consent page after clicking (url=%s)", page.URL())
}

const domExtractJS = `
(() => {
  const out = {
    url: location.href,
    title: document.title || '',
    h1: document.querySelector('h1')?.textContent?.trim() || '',
    metaReviewCount: document.querySelector('meta[itemprop="reviewCount"]')?.getAttribute('content') || '',
    metaRatingValue: document.querySelector('meta[itemprop="ratingValue"]')?.getAttribute('content') || '',
    fromSelectors: [],
    candidates: [],
  };

  const selectors = [
    'button[jsaction*="pane.rating.moreReviews"]',
    'button[aria-label*="reviews"]',
    'a[aria-label*="reviews"]',
    '[role="button"][aria-label*="reviews"]',
    'span[aria-label*="reviews"]',
    '[aria-label*="reviews"]',
  ];
  for (const sel of selectors) {
    const el = document.querySelector(sel);
    if (!el) continue;
    out.fromSelectors.push({
      selector: sel,
      text: (el.textContent || '').trim(),
      aria: el.getAttribute('aria-label') || '',
      outerHTML: (el.outerHTML || '').slice(0, 2000),
    });
  }

  // Candidate scan: aria-label contains digits and "review"
  const max = 80;
  const els = Array.from(document.querySelectorAll('[aria-label]'));
  for (const el of els) {
    const aria = el.getAttribute('aria-label') || '';
    if (!aria) continue;
    if (!/review/i.test(aria)) continue;
    if (!/\d/.test(aria)) continue;
    out.candidates.push({
      aria,
      tag: el.tagName,
      className: el.className || '',
      outerHTML: (el.outerHTML || '').slice(0, 600),
    });
    if (out.candidates.length >= max) break;
  }

  return out;
})()
`

const domReviewsExtractJS = `
(() => {
  const out = [];
  const normalizeGoogleHref = (href) => {
    if (!href) return '';
    try {
      const abs = new URL(href, location.origin);
      if (abs.pathname === '/url') {
        const q = abs.searchParams.get('q');
        if (q) return q.trim();
      }
      return abs.toString();
    } catch (_) {
      return String(href || '').trim();
    }
  };
  let nodes = Array.from(document.querySelectorAll('.jftiEf[data-review-id], [data-review-id].jftiEf'));
  if (nodes.length === 0) {
    nodes = Array.from(document.querySelectorAll('[data-review-id]'));
  }
  for (const node of nodes) {
    const id = (node.getAttribute('data-review-id') || '').trim();
    if (!id) continue;

    const authorEl = node.querySelector('.d4r55') || node.querySelector('[class*=\"d4r55\"]');
    const timeEl = node.querySelector('.rsqaWe') || node.querySelector('[class*=\"rsqaWe\"]') || node.querySelector('.dehysf');

    // Rating is typically exposed via aria-label like \"5 stars\" on a role=img span.
    const ratingEl =
      node.querySelector('[role=\"img\"][aria-label*=\"star\"]') ||
      node.querySelector('span[aria-label*=\"star\"]') ||
      node.querySelector('span[aria-label*=\"stars\"]') ||
      node.querySelector('.kvMYJc');
    const ratingAria = (ratingEl && ratingEl.getAttribute && ratingEl.getAttribute('aria-label')) ? ratingEl.getAttribute('aria-label') : '';
    const ratingMatch = (ratingAria || '').match(/([0-9]+(?:\.[0-9]+)?)/);
    const rating = ratingMatch ? parseFloat(ratingMatch[1]) : 0;

    // Text may be in various containers; prefer the full text if present.
    const textEl =
      node.querySelector('.wiI7pd') ||
      node.querySelector('.MyEned') ||
      node.querySelector('span[jsname=\"fbQN7e\"]') ||
      node.querySelector('[data-expandable-section]');

    const profileImgEl = node.querySelector('img[src*=\"googleusercontent.com\"]') || node.querySelector('img[src]');
    const profilePicture = profileImgEl ? (profileImgEl.getAttribute('src') || '') : '';

    const profileLinkEl = node.querySelector('a[href*=\"/maps/contrib/\"], a[href*=\"maps/contrib\"]');
    let profileLink = '';
    if (profileLinkEl) {
      const href = profileLinkEl.getAttribute('href') || '';
      const normalized = normalizeGoogleHref(href);
      if (/\/maps\/contrib\//i.test(normalized)) {
        profileLink = normalized;
      }
    }

    const reviewUrlEl = node.querySelector('a[href*=\"/maps/reviews/\"]') ||
      node.querySelector('a[href*=\"review\"]');
    let reviewUrl = '';
    if (reviewUrlEl) {
      const href = reviewUrlEl.getAttribute('href') || '';
      const normalized = normalizeGoogleHref(href);
      if (!/\/maps\/contrib\//i.test(normalized)) {
        reviewUrl = normalized;
      }
    }

    let ownerResponse = '';
    let ownerResponseTime = '';
    const responseSelectors = [
      '.CDe7pd',
      '.wiI7pd.xwPlne',
      '.review-response',
      '.owner-response',
    ];
    for (const sel of responseSelectors) {
      const responseEl = node.querySelector(sel);
      if (!responseEl) continue;
      ownerResponse = (responseEl.textContent || '').trim();
      const responseTimeEl = responseEl.closest('.review-response-container')?.querySelector('.rsqaWe') ||
        responseEl.parentElement?.querySelector('.rsqaWe, .dehysf');
      if (responseTimeEl) {
        ownerResponseTime = (responseTimeEl.textContent || '').trim();
      }
      if (ownerResponse) break;
    }

    const images = [];
    const seen = new Set();
    const imageNodes = Array.from(node.querySelectorAll('img[src]'));
    for (const img of imageNodes) {
      const src = (img.getAttribute('src') || '').trim();
      if (!src) continue;
      if (!/googleusercontent\.com|gstatic\.com/i.test(src)) continue;
      if (src === profilePicture) continue;
      if (seen.has(src)) continue;
      seen.add(src);
      images.push(src);
    }

    out.push({
      id,
      author: (authorEl?.textContent || '').trim(),
      profile_link: (profileLink || '').trim(),
      profile_picture: (profilePicture || '').trim(),
      rating,
      time: (timeEl?.textContent || '').trim(),
      text: (textEl?.textContent || '').trim(),
      images,
      review_url: (reviewUrl || '').trim(),
      owner_response: ownerResponse,
      owner_response_time: ownerResponseTime,
    });
  }
  return out;
})()
`

// Copy of the extractor used by gmaps/place.go (we want the exact same blob the scraper sees).
// NOTE: This is a debug-friendly variant. The production scraper uses a simpler version that returns
// the first non-nil [6] it finds; here we skip non-JSON values so we can reliably capture the payload.
const scraperExtractJS = `
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

    const looksLikeJSON = (s) => {
      if (!s) return false;
      s = String(s).trim();
      if (s.startsWith(")]}'")) {
        s = s.slice(4).trim();
      }
      return s.startsWith('[') || s.startsWith('{');
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
              if (looksLikeJSON(out)) return out;
            }
            // Nested object with [6]
            if (typeof node === 'object' && node[6] != null) {
              const out = toOut(node[6]);
              if (looksLikeJSON(out)) return out;
            }
          }
        }

        // Case: array with index 6
        if (Array.isArray(s) && s.length > 6 && s[6] != null) {
          const out = toOut(s[6]);
          if (looksLikeJSON(out)) return out;
        }
      }
    }

    // Also check direct object form
    if (typeof state === 'object' && !Array.isArray(state)) {
      for (const k in state) {
        const node = state[k];
        if (node && node[6] != null) {
          const out = toOut(node[6]);
          if (looksLikeJSON(out)) return out;
        }
      }
    }

    return null;
  } catch (e) {
    return null;
  }
})()`
