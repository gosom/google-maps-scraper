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

// maxMenuPhotos caps the Menu album to the N most recently uploaded photos so
// the stored menu reflects current pricing/items rather than stale shots.
const maxMenuPhotos = 6

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

// photoMarkGridJS captures a content signature of the album currently shown in
// the grid: the first few photo URLs. Google Maps virtualizes/recycles the grid
// nodes (it swaps background-image on existing nodes rather than creating new
// ones), so node-identity tricks are unreliable. Comparing the leading URLs is
// a stable way to tell whether a tab switch actually changed the content.
var photoMarkGridJS = photoEval(`
	const grid = findPhotoGrid();
	if (!grid) return JSON.stringify({ ok: false, baseline_firsts: [] });
	const urls = collectURLs(grid);
	return JSON.stringify({ ok: true, baseline_firsts: urls.slice(0, 4) });
`)

// photoRefreshStateJS reports the currently selected tab and a content signature
// (the first few photo URLs) of the grid, so the caller can detect when the
// grid has actually re-rendered for a newly selected album.
var photoRefreshStateJS = photoEval(`
	const tablist = findPhotoTablist();
	let selectedTab = "";
	if (tablist) {
		const selected = tabsOf(tablist).find((tab) => tab.getAttribute('aria-selected') === 'true');
		if (selected) selectedTab = tabLabel(selected);
	}
	const grid = findPhotoGrid();
	if (!grid) {
		return JSON.stringify({ selected_tab: selectedTab, has_grid: false, firsts: [] });
	}
	const urls = collectURLs(grid);
	return JSON.stringify({ selected_tab: selectedTab, has_grid: true, firsts: urls.slice(0, 4) });
`)

// photoDatesJS walks every photo tile currently in the open album, clicks each
// one to surface its "Image capture: Mon YYYY" date in the detail panel, and
// returns [{url, date}] pairs. Google Maps orders album tiles by relevance, not
// recency, and only exposes a photo's date once it is selected — so reading the
// dates is the only way to find the most recently uploaded photos. This is an
// async function: scrapemate/playwright awaits the returned promise.
const photoDatesJS = `async () => {
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  const styleURL = (el) => {
    const bg = el && el.style && el.style.backgroundImage;
    if (!bg) return "";
    const m = bg.match(/url\(["']?([^"')]+)["']?\)/);
    return m && m[1] ? m[1] : "";
  };

  const isPhotoURL = (u) =>
    !!u && (u.includes("googleusercontent") || u.includes("streetviewpixels-pa.googleapis.com"));

  const tileURL = (el) => {
    const inner = el.querySelector('[style*="background-image"]');
    let u = styleURL(inner) || styleURL(el);
    if (!u) {
      const img = el.querySelector("img");
      if (img && img.src) u = img.src;
    }
    return isPhotoURL(u) ? u : "";
  };

  const readDate = () => {
    const info = document.querySelector('[role="contentinfo"]') || document.body;
    const nodes = info.querySelectorAll("*");
    for (const e of nodes) {
      if (e.children.length !== 0) continue;
      const t = (e.textContent || "").trim();
      const m = t.match(/Image capture:\s*([A-Z][a-z]{2})\s+(20\d\d)/);
      if (m) return m[1] + " " + m[2];
    }
    return "";
  };

  const items = Array.from(document.querySelectorAll("a.MIgS0d"));
  const out = [];
  for (let i = 0; i < items.length && i < 25; i++) {
    const el = items[i];
    const url = tileURL(el);
    if (!url) continue;
    try { el.scrollIntoView({ block: "center" }); } catch (e) {}
    el.click();
    await sleep(450);
    let date = readDate();
    if (!date) { await sleep(350); date = readDate(); }
    out.push({ url: url, date: date });
  }
  return JSON.stringify(out);
}`

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

type photoMarkResult struct {
	OK             bool     `json:"ok"`
	BaselineFirsts []string `json:"baseline_firsts"`
}

type photoRefreshState struct {
	SelectedTab string   `json:"selected_tab"`
	HasGrid     bool     `json:"has_grid"`
	Firsts      []string `json:"firsts"`
}

// photoSigEqual reports whether two album content signatures (leading URL
// lists) are identical. An empty signature never matches, so an unloaded grid
// is never considered "equal" to a real album.
func photoSigEqual(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func markPhotoGrid(page scrapemate.BrowserPage) (scrapemate.BrowserPage, photoMarkResult, error) {
	nextPage, raw, err := evalJSONString(page, photoMarkGridJS)
	if err != nil {
		return nextPage, photoMarkResult{}, err
	}

	var res photoMarkResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nextPage, res, fmt.Errorf("parse photo mark: %w", err)
	}

	return nextPage, res, nil
}

func getPhotoRefreshState(page scrapemate.BrowserPage) (scrapemate.BrowserPage, photoRefreshState, error) {
	nextPage, raw, err := evalJSONString(page, photoRefreshStateJS)
	if err != nil {
		return nextPage, photoRefreshState{}, err
	}

	var st photoRefreshState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nextPage, st, fmt.Errorf("parse photo refresh state: %w", err)
	}

	return nextPage, st, nil
}

type datedPhoto struct {
	URL  string `json:"url"`
	Date string `json:"date"`
}

var photoMonthIndex = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// photoDateKey converts "Mon YYYY" into a sortable integer (year*12+month).
// Unparseable/missing dates return -1 so they sort after every dated photo.
func photoDateKey(date string) int {
	fields := strings.Fields(strings.TrimSpace(date))
	if len(fields) != 2 {
		return -1
	}

	month, ok := photoMonthIndex[strings.ToLower(fields[0])]
	if !ok {
		return -1
	}

	year := 0
	for _, r := range fields[1] {
		if r < '0' || r > '9' {
			return -1
		}
		year = year*10 + int(r-'0')
	}

	return year*12 + month
}

// getDatedPhotos clicks through every tile in the currently open album to read
// each photo's capture date, returning them in the album's native (relevance)
// order. Best-effort: returns whatever it managed to read.
func getDatedPhotos(page scrapemate.BrowserPage) (scrapemate.BrowserPage, []datedPhoto, error) {
	nextPage, raw, err := evalJSONString(page, photoDatesJS)
	if err != nil {
		return nextPage, nil, err
	}

	var photos []datedPhoto
	if err := json.Unmarshal([]byte(raw), &photos); err != nil {
		return nextPage, nil, fmt.Errorf("parse dated photos: %w", err)
	}

	return nextPage, photos, nil
}

// latestPhotoURLs sorts dated photos newest-first (stable: ties keep relevance
// order) and returns up to limit normalized URLs.
func latestPhotoURLs(photos []datedPhoto, limit int) []string {
	indexed := make([]int, len(photos))
	for i := range indexed {
		indexed[i] = i
	}

	sort.SliceStable(indexed, func(a, b int) bool {
		return photoDateKey(photos[indexed[a]].Date) > photoDateKey(photos[indexed[b]].Date)
	})

	urls := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, idx := range indexed {
		normalized := normalizePhotoURL(photos[idx].URL)
		if normalized == "" {
			continue
		}
		if _, dup := seen[normalized]; dup {
			continue
		}
		seen[normalized] = struct{}{}
		urls = append(urls, normalized)
		if len(urls) >= limit {
			break
		}
	}

	return urls
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

func waitForPhotoTabRefresh(
	ctx context.Context,
	page scrapemate.BrowserPage,
	title string,
	baseline []string,
	timeout time.Duration,
) (scrapemate.BrowserPage, photoRefreshState, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	var lastState photoRefreshState
	var prevFirsts []string
	stableCount := 0

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return page, lastState, err
		}

		nextPage, st, err := getPhotoRefreshState(page)
		page = nextPage
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)

			continue
		}

		lastState = st

		// Track how long the visible content signature has been stable. A grid
		// mid-swap keeps changing between polls; we only trust settled content.
		if len(st.Firsts) > 0 && photoSigEqual(st.Firsts, prevFirsts) {
			stableCount++
		} else {
			stableCount = 0
		}
		prevFirsts = st.Firsts

		if !strings.EqualFold(st.SelectedTab, title) || !st.HasGrid || len(st.Firsts) == 0 {
			time.Sleep(200 * time.Millisecond)

			continue
		}

		settled := stableCount >= 1 // same signature on two consecutive polls

		// A genuine album switch shows content different from the previous album.
		// Comparing the leading URLs (not just the first) distinguishes a real
		// switch — where albums may legitimately share the single most-relevant
		// hero photo but differ further down — from a stale grid that still shows
		// the entire previous album.
		differsFromBaseline := len(baseline) == 0 || !photoSigEqual(st.Firsts, baseline)

		if settled && differsFromBaseline {
			return page, st, nil
		}

		time.Sleep(200 * time.Millisecond)
	}

	if lastErr != nil && !lastState.HasGrid {
		return page, lastState, lastErr
	}

	// Timed out. If the content never diverged from the previous album, treat it
	// as stale so the caller emits an empty album instead of a duplicate.
	if len(baseline) > 0 && photoSigEqual(lastState.Firsts, baseline) {
		return page, lastState, fmt.Errorf(
			"photo extractor: tab %q stale (content still equals previous album)", title,
		)
	}

	// Otherwise the tab is selected and showing *some* content that differs from
	// the previous album — accept it best-effort rather than dropping real photos.
	if strings.EqualFold(lastState.SelectedTab, title) && lastState.HasGrid && len(lastState.Firsts) > 0 {
		return page, lastState, nil
	}

	return page, lastState, fmt.Errorf(
		"photo extractor: tab %q did not refresh (selected=%q)", title, lastState.SelectedTab,
	)
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

	initialSelected := state.SelectedTab

	for idx, title := range labels {
		// The album Google opens on (usually "All") is already rendered, so we
		// can scrape it directly. Every other tab must be clicked and we must
		// wait for the grid to actually re-render before scraping — otherwise we
		// capture the previous album's photos (the stale-duplicate bug).
		alreadySelected := idx == 0 && strings.EqualFold(initialSelected, title)

		if !alreadySelected {
			// Capture a content signature of the album currently shown so we can
			// detect when the grid actually changes to the new album.
			var mark photoMarkResult
			page, mark, err = markPhotoGrid(page)
			var baseline []string
			if err == nil && mark.OK {
				baseline = mark.BaselineFirsts
			}

			var clicked bool
			page, clicked, err = clickPhotoTab(page, title)
			if err != nil {
				// A single flaky tab must not abort the remaining albums. Retry
				// once after a brief settle; if it still fails, skip just this
				// tab and keep going so other albums are still captured.
				time.Sleep(400 * time.Millisecond)
				page = bestEffortReattach(page)
				page, clicked, err = clickPhotoTab(page, title)
				if err != nil {
					albums = append(albums, PhotoAlbum{Title: title, Photos: []Photo{}})
					continue
				}
			}
			if !clicked {
				albums = append(albums, PhotoAlbum{Title: title, Photos: []Photo{}})
				continue
			}

			page, _, err = waitForPhotoTabRefresh(ctx, page, title, baseline, 12*time.Second)
			if err != nil {
				// The tab never rendered its own album within the timeout. Emit an
				// empty album rather than duplicating the previous one's photos.
				albums = append(albums, PhotoAlbum{Title: title, Photos: []Photo{}})
				continue
			}

			// Brief settle so the grid finishes its first paint before scrolling.
			time.Sleep(300 * time.Millisecond)
		}

		// Menu: tiles are relevance-ordered, so reorder by upload date and keep
		// only the most recent few. Falls back to relevance order if dates can't
		// be read.
		if normalizePhotoLabel(title) == "menu" {
			var dated []datedPhoto
			page, dated, err = getDatedPhotos(page)

			var urls []string
			if err == nil && len(dated) > 0 {
				urls = latestPhotoURLs(dated, maxMenuPhotos)
			} else {
				page, urls, err = scrollPhotoGrid(ctx, page)
				if err != nil {
					albums = append(albums, PhotoAlbum{Title: title, Photos: []Photo{}})
					continue
				}
				if len(urls) > maxMenuPhotos {
					urls = urls[:maxMenuPhotos]
				}
			}

			albums = append(albums, PhotoAlbum{Title: title, Photos: photosFromURLs(urls)})
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

		albums = append(albums, PhotoAlbum{Title: title, Photos: photosFromURLs(urls)})
	}

	closePhotoViewer(page)

	return albums, nil
}

func photosFromURLs(urls []string) []Photo {
	photos := make([]Photo, len(urls))
	for i := range urls {
		photos[i] = Photo{URL: urls[i]}
	}

	return photos
}
