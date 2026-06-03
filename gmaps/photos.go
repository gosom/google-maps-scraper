package gmaps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gosom/scrapemate"
	smpw "github.com/gosom/scrapemate/adapters/browsers/playwright"
	"github.com/playwright-community/playwright-go"
)

type Photo struct {
	URL string `json:"url"`
}

type PhotoAlbum struct {
	Title  string  `json:"title"`
	Photos []Photo `json:"photos"`
}

const maxPhotosPerAlbum = 20

type photoBrowserState struct {
	URL          string     `json:"url"`
	TabLabels    []string   `json:"tab_labels"`
	SelectedTab  string     `json:"selected_tab"`
	GridItems    int        `json:"grid_items"`
	URLCount     int        `json:"url_count"`
	SampleURLs   []string   `json:"sample_urls"`
	SeenTablists [][]string `json:"seen_tablists,omitempty"`
}

var photoEntrySelectors = []string{
	`button[aria-label="See photos"]`,
	`button[aria-label^="Photo of"]`,
	`button[aria-label^="Photos of"]`,
	`button[aria-label*="See photos"]`,
	`button[jsaction*="pane.heroHeaderImage"]`,
	`button[data-photo-index="0"]`,
	`button.aoRNLd`,
	`a[aria-label*="See photos"]`,
	`a[href*="/photo/"]`,
}

var photoBackSelectors = []string{
	`button[jsaction*="pane.topappbar.back"]`,
	`button[aria-label="Back"]`,
}

const photoEvalPrelude = `() => {
  const PLACE_TABS = new Set([
    "overview", "about", "reviews", "updates", "photos",
    "menu", "services", "products", "tickets"
  ]);
  const PHOTO_HINT_TABS = new Set(["all", "latest", "videos"]);

  const tabsOf = (tablist) =>
    Array.from(tablist.querySelectorAll('button[role="tab"], [role="tab"]'));

  const tabLabel = (tab) =>
    (tab.innerText || tab.textContent || tab.getAttribute('aria-label') || "")
      .trim();

  const labelsOf = (tablist) =>
    tabsOf(tablist)
      .map((tab) => tabLabel(tab).toLowerCase())
      .filter(Boolean);

  const isPhotoTablist = (tablist) => {
    const labels = labelsOf(tablist);
    if (labels.length < 2) return false;
    if (labels.some((label) => PHOTO_HINT_TABS.has(label))) return true;
    return !labels.every((label) => PLACE_TABS.has(label));
  };

  const styleURL = (el) => {
    const bg = el.style && el.style.backgroundImage;
    if (!bg) return "";
    const match = bg.match(/url\(["']?([^"')]+)["']?\)/);
    return match && match[1] ? match[1] : "";
  };

  const isPhotoURL = (url) =>
    url.includes("googleusercontent") ||
    url.includes("streetviewpixels-pa.googleapis.com");

  const collectURLs = (root) => {
    const full = new Set();
    const fallback = new Set();

    root.querySelectorAll('.gCPOGf.vCWwFf[style*="background-image"]').forEach((el) => {
      const url = styleURL(el);
      if (url && isPhotoURL(url)) full.add(url);
    });

    root.querySelectorAll('a.MIgS0d .aHpZye[style*="background-image"], .aHpZye[style*="background-image"]').forEach((el) => {
      const url = styleURL(el);
      if (url && isPhotoURL(url)) fallback.add(url);
    });

    root.querySelectorAll('img[src]').forEach((img) => {
      if (img.src && isPhotoURL(img.src)) fallback.add(img.src);
    });

    return Array.from(full.size > 0 ? full : fallback);
  };

  const findPhotoTablist = () => {
    const lists = Array.from(document.querySelectorAll('[role="tablist"]'));
    return lists.find((tablist) => isPhotoTablist(tablist)) || null;
  };

  const findPhotoGrid = () => {
    const grids = Array.from(document.querySelectorAll('.m6QErb.XiKgde, .m6QErb'));
    let best = null;
    let bestScore = 0;

    for (const grid of grids) {
      const photoItems = grid.querySelectorAll('a.MIgS0d').length;
      const bgItems = grid.querySelectorAll('.gCPOGf.vCWwFf[style*="background-image"], .aHpZye[style*="background-image"]').length;
      const score = (photoItems * 10) + (bgItems * 5) + grid.childElementCount;
      if (score > bestScore) {
        best = grid;
        bestScore = score;
      }
    }

    return bestScore > 0 ? best : null;
  };

	const findPhotoScroller = () => {
		const grids = Array.from(document.querySelectorAll('.m6QErb'));
		let best = null;
		let bestScore = 0;

		for (const grid of grids) {
			const photoItems = grid.querySelectorAll('a.MIgS0d').length;
			const bgItems = grid.querySelectorAll('.gCPOGf.vCWwFf[style*="background-image"], .aHpZye[style*="background-image"]').length;
			const score = (photoItems * 10) + (bgItems * 5) + grid.childElementCount;
			if (score == 0) {
				continue;
			}
			if (grid.scrollHeight <= grid.clientHeight + 10) {
				continue;
			}
			if (score > bestScore) {
				best = grid;
				bestScore = score;
			}
		}

		return bestScore > 0 ? best : null;
	};
`

const photoEvalPostlude = `
}`

var photoStateJS = photoEval(`
  const tablist = findPhotoTablist();
  const grid = findPhotoGrid();
  let selectedTab = "";

  if (tablist) {
    const selected = tabsOf(tablist).find((tab) => tab.getAttribute('aria-selected') === 'true');
    if (selected) selectedTab = tabLabel(selected);
  }

  const urls = grid ? collectURLs(grid) : [];

  return JSON.stringify({
    url: location.href,
    tab_labels: tablist ? tabsOf(tablist).map((tab) => tabLabel(tab)).filter(Boolean) : [],
    selected_tab: selectedTab,
    grid_items: grid ? grid.querySelectorAll('a.MIgS0d').length : 0,
    url_count: urls.length,
    sample_urls: urls.slice(0, 3),
    seen_tablists: Array.from(document.querySelectorAll('[role="tablist"]'))
      .map((candidate) => tabsOf(candidate).map((tab) => tabLabel(tab)).filter(Boolean)),
  });
`)

var photoURLsJS = photoEval(`
	const root = findPhotoScroller() || findPhotoGrid();
	return JSON.stringify(root ? collectURLs(root) : []);
`)

var photoScrollJS = photoEval(`
	const scroller = findPhotoScroller();
	if (!scroller) return false;
	const step = Math.max(200, Math.floor(scroller.clientHeight * 0.8));
	const prevTop = scroller.scrollTop;
	scroller.scrollTop = Math.min(scroller.scrollTop + step, scroller.scrollHeight);
	return scroller.scrollTop > prevTop;
`)

func photoEval(body string) string {
	return photoEvalPrelude + body + photoEvalPostlude
}

func reattachPhotoPage(page scrapemate.BrowserPage) (scrapemate.BrowserPage, error) {
	wrapped := page.Unwrap()
	pwPage, ok := wrapped.(playwright.Page)
	if !ok {
		return page, fmt.Errorf("unexpected browser page type: %T", wrapped)
	}
	if !pwPage.IsClosed() {
		return page, nil
	}

	pages := pwPage.Context().Pages()
	for i := len(pages) - 1; i >= 0; i-- {
		candidate := pages[i]
		if candidate == nil || candidate.IsClosed() {
			continue
		}

		return smpw.NewPage(candidate), nil
	}

	return page, fmt.Errorf("playwright: target closed: no replacement page found")
}

func bestEffortReattach(page scrapemate.BrowserPage) scrapemate.BrowserPage {
	next, err := reattachPhotoPage(page)
	if err == nil {
		return next
	}

	return page
}

func evalJSONString(page scrapemate.BrowserPage, js string) (scrapemate.BrowserPage, string, error) {
	page = bestEffortReattach(page)

	raw, err := page.Eval(js)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "target closed") {
		nextPage, reattachErr := reattachPhotoPage(page)
		if reattachErr == nil {
			page = nextPage
			raw, err = page.Eval(js)
		}
	}
	if err != nil {
		return page, "", err
	}

	s, ok := raw.(string)
	if !ok {
		return page, "", fmt.Errorf("unexpected photo result type: %T", raw)
	}

	return page, s, nil
}

func evalBool(page scrapemate.BrowserPage, js string) (scrapemate.BrowserPage, bool, error) {
	page = bestEffortReattach(page)

	raw, err := page.Eval(js)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "target closed") {
		nextPage, reattachErr := reattachPhotoPage(page)
		if reattachErr == nil {
			page = nextPage
			raw, err = page.Eval(js)
		}
	}
	if err != nil {
		return page, false, err
	}

	b, ok := raw.(bool)
	if !ok {
		return page, false, fmt.Errorf("unexpected photo bool type: %T", raw)
	}

	return page, b, nil
}

func getPhotoState(page scrapemate.BrowserPage) (scrapemate.BrowserPage, photoBrowserState, error) {
	var state photoBrowserState

	nextPage, raw, err := evalJSONString(page, photoStateJS)
	if err != nil {
		return nextPage, state, err
	}

	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nextPage, state, fmt.Errorf("parse photo state: %w", err)
	}

	return nextPage, state, nil
}

func getPhotoURLs(page scrapemate.BrowserPage) (scrapemate.BrowserPage, []string, error) {
	nextPage, raw, err := evalJSONString(page, photoURLsJS)
	if err != nil {
		return nextPage, nil, err
	}

	var urls []string
	if err := json.Unmarshal([]byte(raw), &urls); err != nil {
		return nextPage, nil, fmt.Errorf("parse photo urls: %w", err)
	}

	return nextPage, urls, nil
}

func clickFirstMatching(page scrapemate.BrowserPage, selectors []string, timeout time.Duration) (bool, error) {
	var lastErr error

	for _, selector := range selectors {
		locator := page.Locator(selector)
		count, err := locator.Count()
		if err != nil {
			lastErr = err
			continue
		}
		if count == 0 {
			continue
		}
		if err := locator.First().Click(timeout); err != nil {
			lastErr = err
			continue
		}

		return true, nil
	}

	return false, lastErr
}

func clickPhotoTab(page scrapemate.BrowserPage, title string) (scrapemate.BrowserPage, bool, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return page, false, nil
	}

	page = bestEffortReattach(page)

	wrapped := page.Unwrap()
	pwPage, ok := wrapped.(playwright.Page)
	if !ok {
		return page, false, fmt.Errorf("unexpected browser page type: %T", wrapped)
	}

	locator := pwPage.GetByRole(*playwright.AriaRoleTab, playwright.PageGetByRoleOptions{
		Name:  title,
		Exact: playwright.Bool(true),
	})

	count, err := locator.Count()
	if err != nil {
		return page, false, err
	}
	if count == 0 {
		return page, false, nil
	}

	if err := locator.First().Click(playwright.LocatorClickOptions{
		Timeout: playwright.Float(5000),
	}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "target closed") {
			nextPage, reattachErr := reattachPhotoPage(page)
			if reattachErr == nil {
				return nextPage, true, nil
			}
		}

		return page, false, err
	}

	return bestEffortReattach(page), true, nil
}

func normalizePhotoLabel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func photoSignature(state photoBrowserState) string {
	return fmt.Sprintf("%s|%d|%d|%s",
		strings.ToLower(state.SelectedTab),
		state.GridItems,
		state.URLCount,
		strings.Join(state.SampleURLs, "|"),
	)
}

func normalizePhotoURL(raw string) string {
	if raw == "" {
		return raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	host := strings.ToLower(parsed.Host)
	if strings.Contains(host, "streetviewpixels-pa.googleapis.com") {
		query := parsed.Query()
		query.Set("w", "1600")
		query.Set("h", "900")
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}

	if !strings.Contains(host, "googleusercontent.com") {
		return raw
	}

	lastSlash := strings.LastIndex(parsed.Path, "/")
	lastEqual := strings.LastIndex(parsed.Path, "=")
	if lastEqual > lastSlash {
		parsed.Path = parsed.Path[:lastEqual+1] + "s0"
		return parsed.String()
	}

	lastEqual = strings.LastIndex(raw, "=")
	if lastEqual != -1 && lastEqual > strings.LastIndex(raw, "/") {
		return raw[:lastEqual+1] + "s0"
	}

	return raw
}

func accumulatePhotoURLs(dst map[string]struct{}, urls []string) bool {
	changed := false
	for _, raw := range urls {
		normalized := normalizePhotoURL(raw)
		if normalized == "" {
			continue
		}
		if _, ok := dst[normalized]; ok {
			continue
		}
		dst[normalized] = struct{}{}
		changed = true
	}

	return changed
}

func sortedPhotoURLs(collected map[string]struct{}) []string {
	urls := make([]string, 0, len(collected))
	for raw := range collected {
		urls = append(urls, raw)
	}
	sort.Strings(urls)

	return urls
}

func orderPhotoTabs(labels []string) []string {
	ordered := make([]string, 0, len(labels))
	deferred := make([]string, 0, len(labels))

	for _, label := range labels {
		switch normalizePhotoLabel(label) {
		case "all":
			ordered = append(ordered, label)
		case "latest", "videos", "street view & 360°":
			deferred = append(deferred, label)
		default:
			ordered = append(ordered, label)
		}
	}

	return append(ordered, deferred...)
}

func waitForPhotoViewer(ctx context.Context, page scrapemate.BrowserPage, timeout time.Duration) (scrapemate.BrowserPage, photoBrowserState, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	var lastState photoBrowserState

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return page, lastState, err
		}

		nextPage, state, err := getPhotoState(page)
		page = nextPage
		if err == nil {
			lastState = state
			if len(state.TabLabels) > 0 {
				return page, state, nil
			}
		} else {
			lastErr = err
		}

		time.Sleep(250 * time.Millisecond)
	}

	if len(lastState.SeenTablists) > 0 || lastState.URL != "" {
		return page, lastState, fmt.Errorf("photo extractor: photo tablist not found (url=%s tablists=%v)", lastState.URL, lastState.SeenTablists)
	}
	if lastErr != nil {
		return page, lastState, lastErr
	}

	return page, lastState, fmt.Errorf("photo extractor: photo tablist not found")
}

func waitForPhotoTab(ctx context.Context, page scrapemate.BrowserPage, title, beforeSignature string, timeout time.Duration) (scrapemate.BrowserPage, photoBrowserState, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	var lastState photoBrowserState
	attempts := 0

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return page, lastState, err
		}

		nextPage, state, err := getPhotoState(page)
		page = nextPage
		if err == nil {
			lastState = state
			if strings.EqualFold(state.SelectedTab, title) && (photoSignature(state) != beforeSignature || attempts >= 3) {
				return page, state, nil
			}
		} else {
			lastErr = err
		}

		attempts++
		time.Sleep(250 * time.Millisecond)
	}

	if lastErr != nil && lastState.URL == "" {
		return page, lastState, lastErr
	}

	return page, lastState, fmt.Errorf("photo extractor: tab %q did not load (url=%s selected=%q)", title, lastState.URL, lastState.SelectedTab)
}

func scrollPhotoGrid(ctx context.Context, page scrapemate.BrowserPage) (scrapemate.BrowserPage, []string, error) {
	collected := make(map[string]struct{})
	stable := 0

	page, urls, err := getPhotoURLs(page)
	if err == nil {
		_ = accumulatePhotoURLs(collected, urls)
		if len(collected) >= maxPhotosPerAlbum {
			return page, sortedPhotoURLs(collected), nil
		}
	}

	for i := 0; i < 160 && stable < 5; i++ {
		if err := ctx.Err(); err != nil {
			return page, sortedPhotoURLs(collected), err
		}

		nextPage, _, err := evalBool(page, photoScrollJS)
		page = nextPage
		if err != nil {
			return page, sortedPhotoURLs(collected), err
		}

		time.Sleep(350 * time.Millisecond)

		page, urls, err = getPhotoURLs(page)
		if err != nil {
			return page, sortedPhotoURLs(collected), err
		}

		if accumulatePhotoURLs(collected, urls) {
			if len(collected) >= maxPhotosPerAlbum {
				return page, sortedPhotoURLs(collected), nil
			}
			stable = 0
			continue
		}

		nextPage, state, err := getPhotoState(page)
		page = nextPage
		if err != nil {
			return page, sortedPhotoURLs(collected), err
		}

		// Google Maps virtualizes the grid, so stop only after several
		// consecutive scrolls that neither add URLs nor change the viewport state.
		if state.URLCount == 0 && state.GridItems == 0 {
			stable++
			continue
		}

		stable++
	}

	return page, sortedPhotoURLs(collected), nil
}

func closePhotoViewer(page scrapemate.BrowserPage) {
	page = bestEffortReattach(page)
	_, _ = clickFirstMatching(page, photoBackSelectors, 3*time.Second)
}

// fetchPhotoAlbums runs the JS extractor against the open place page.
// It is best-effort: on any error or unexpected shape it returns an empty
// list rather than failing the parent place job.
func fetchPhotoAlbums(ctx context.Context, page scrapemate.BrowserPage) ([]PhotoAlbum, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	page, state, err := getPhotoState(page)
	if err != nil {
		state = photoBrowserState{}
	}

	if len(state.TabLabels) == 0 {
		clicked, clickErr := clickFirstMatching(page, photoEntrySelectors, 5*time.Second)
		if clickErr != nil {
			return nil, fmt.Errorf("open photo viewer click: %w", clickErr)
		}
		if !clicked {
			return nil, fmt.Errorf("photo extractor: photo entry not found")
		}

		page = bestEffortReattach(page)
		page, state, err = waitForPhotoViewer(ctx, page, 20*time.Second)
		if err != nil {
			return nil, fmt.Errorf("wait for photo viewer: %w", err)
		}
	}

	labels := orderPhotoTabs(append([]string(nil), state.TabLabels...))
	albums := make([]PhotoAlbum, 0, len(labels))

	for _, title := range labels {
		beforeSignature := photoSignature(state)

		var clicked bool
		page, clicked, err = clickPhotoTab(page, title)
		if err != nil {
			if len(albums) > 0 {
				return albums, nil
			}
			closePhotoViewer(page)
			return nil, fmt.Errorf("click photo tab %q: %w", title, err)
		}
		if !clicked {
			albums = append(albums, PhotoAlbum{Title: title, Photos: []Photo{}})
			continue
		}

		page, state, err = waitForPhotoTab(ctx, page, title, beforeSignature, 15*time.Second)
		if err != nil {
			albums = append(albums, PhotoAlbum{Title: title, Photos: []Photo{}})
			continue
		}

		var urls []string
		page, urls, err = scrollPhotoGrid(ctx, page)
		if err != nil {
			albums = append(albums, PhotoAlbum{Title: title, Photos: []Photo{}})
			continue
		}
		if len(urls) > maxPhotosPerAlbum {
			urls = urls[:maxPhotosPerAlbum]
		}

		photos := make([]Photo, len(urls))
		for i := range urls {
			photos[i] = Photo{URL: urls[i]}
		}

		albums = append(albums, PhotoAlbum{Title: title, Photos: photos})
	}

	closePhotoViewer(page)

	return albums, nil
}
