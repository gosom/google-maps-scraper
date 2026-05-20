package gmaps

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

// CookieEntry represents a cookie from a JSON export file.
// Compatible with Chrome "EditThisCookie" extension export format
// and Playwright's cookie format.
type CookieEntry struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expirationDate,omitempty"` // EditThisCookie format
	Secure   bool    `json:"secure"`
	HttpOnly bool    `json:"httpOnly"`
	SameSite string  `json:"sameSite,omitempty"`
}

// cookiesCache is the mtime-keyed cache for parsed cookies. Holding the
// mutex across the read+parse is acceptable: cookie reloads are rare
// (operator action) and review fetches happen at most a few times per
// second per process, dominated by network I/O.
var (
	cookiesMu       sync.RWMutex
	cookiesEntries  []CookieEntry
	cookiesMtime    time.Time
	cookiesFilePath string
)

// SetCookiesFile sets the path to the cookies JSON file.
// Must be called before any cookie loading happens.
func SetCookiesFile(path string) {
	cookiesMu.Lock()
	defer cookiesMu.Unlock()
	if path != cookiesFilePath {
		// Path change invalidates the cache — different file may have a
		// coincidentally identical mtime.
		cookiesEntries = nil
		cookiesMtime = time.Time{}
	}
	cookiesFilePath = path
}

// LoadGoogleCookies returns the parsed cookies. It re-reads from disk only
// when the file's mtime has changed since the last successful load, so a
// fresh cookie dump on a running prod box takes effect on the next request
// without a backend restart.
//
// Concurrency: serialized through cookiesMu. Callers receive a freshly
// allocated slice (not aliasing the cache) — safe to mutate or hand off.
func LoadGoogleCookies() ([]CookieEntry, error) {
	cookiesMu.RLock()
	path := cookiesFilePath
	cachedMtime := cookiesMtime
	cookiesMu.RUnlock()

	if path == "" {
		return nil, fmt.Errorf("no cookies file configured")
	}

	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat cookies file %s: %w", path, err)
	}

	if st.ModTime().Equal(cachedMtime) {
		// Cache hit — return a copy under read lock.
		cookiesMu.RLock()
		out := append([]CookieEntry(nil), cookiesEntries...)
		cookiesMu.RUnlock()
		return out, nil
	}

	// Cache miss / file changed — re-read under write lock.
	cookiesMu.Lock()
	defer cookiesMu.Unlock()
	// Double-check after acquiring write lock: another goroutine may have
	// already refreshed the cache.
	if st.ModTime().Equal(cookiesMtime) {
		return append([]CookieEntry(nil), cookiesEntries...), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cookies file %s: %w", path, err)
	}

	var parsed []CookieEntry
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse cookies file: %w", err)
	}

	filtered := make([]CookieEntry, 0, len(parsed))
	for _, c := range parsed {
		if strings.Contains(c.Domain, "google") {
			filtered = append(filtered, c)
		}
	}

	cookiesEntries = filtered
	cookiesMtime = st.ModTime()
	slog.Info("google_cookies_loaded",
		slog.Int("total", len(parsed)),
		slog.Int("google_filtered", len(filtered)),
		slog.Time("file_mtime", st.ModTime()),
	)

	return append([]CookieEntry(nil), filtered...), nil
}

// resetCookiesCacheForTest clears the package-level cookie cache. Test-only.
func resetCookiesCacheForTest() {
	cookiesMu.Lock()
	defer cookiesMu.Unlock()
	cookiesEntries = nil
	cookiesMtime = time.Time{}
}

// InjectCookiesIntoPage adds Google cookies to a Playwright page's browser context.
// Call this BEFORE page.Goto() to ensure cookies are set before navigation.
func InjectCookiesIntoPage(page playwright.Page) error {
	cookies, err := LoadGoogleCookies()
	if err != nil {
		return err
	}

	if len(cookies) == 0 {
		return nil
	}

	var pwCookies []playwright.OptionalCookie
	for _, c := range cookies {
		sameSite := playwright.SameSiteAttributeLax // default (matches Chrome's default for "unspecified")
		switch strings.ToLower(c.SameSite) {
		case "lax":
			sameSite = playwright.SameSiteAttributeLax
		case "strict":
			sameSite = playwright.SameSiteAttributeStrict
		case "none", "no_restriction":
			sameSite = playwright.SameSiteAttributeNone
		case "unspecified", "":
			// Chrome's "unspecified" means browser default = Lax
			sameSite = playwright.SameSiteAttributeLax
		}

		cookie := playwright.OptionalCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   playwright.String(c.Domain),
			Path:     playwright.String(c.Path),
			Secure:   playwright.Bool(c.Secure),
			HttpOnly: playwright.Bool(c.HttpOnly),
			SameSite: sameSite,
		}

		if c.Expires > 0 {
			cookie.Expires = playwright.Float(c.Expires)
		}

		pwCookies = append(pwCookies, cookie)
	}

	if err := page.Context().AddCookies(pwCookies); err != nil {
		return fmt.Errorf("failed to inject cookies: %w", err)
	}

	slog.Debug("cookies_injected_into_page", slog.Int("count", len(pwCookies)))
	return nil
}

// GetCookieHeader returns a Cookie header string for HTTP requests.
// Used by the review fetcher to authenticate RPC calls.
func GetCookieHeader() string {
	cookies, err := LoadGoogleCookies()
	if err != nil || len(cookies) == 0 {
		return ""
	}
	return cookieHeaderFromEntries(cookies)
}

// cookieHeaderFromEntries serializes cookie entries into the RFC 6265
// `Name=Value; Name=Value; …` Cookie header format. Entries with empty
// Name or Value are skipped — emitting `Name=` is permitted by the RFC
// but some Google endpoints treat it as a malformed header and fall back
// to the unauthenticated response path.
func cookieHeaderFromEntries(cookies []CookieEntry) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Name == "" || c.Value == "" {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}
