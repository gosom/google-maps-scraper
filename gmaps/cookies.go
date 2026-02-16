package gmaps

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

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

var (
	googleCookies     []CookieEntry
	googleCookiesOnce sync.Once
	googleCookiesErr  error
	cookiesFilePath   string
)

// SetCookiesFile sets the path to the cookies JSON file.
// Must be called before any cookie loading happens.
func SetCookiesFile(path string) {
	cookiesFilePath = path
}

// LoadGoogleCookies loads cookies from the configured JSON file (once).
func LoadGoogleCookies() ([]CookieEntry, error) {
	googleCookiesOnce.Do(func() {
		if cookiesFilePath == "" {
			googleCookiesErr = fmt.Errorf("no cookies file configured")
			return
		}

		data, err := os.ReadFile(cookiesFilePath)
		if err != nil {
			googleCookiesErr = fmt.Errorf("failed to read cookies file %s: %w", cookiesFilePath, err)
			return
		}

		var cookies []CookieEntry
		if err := json.Unmarshal(data, &cookies); err != nil {
			googleCookiesErr = fmt.Errorf("failed to parse cookies file: %w", err)
			return
		}

		// Filter to only Google-related cookies
		var filtered []CookieEntry
		for _, c := range cookies {
			if strings.Contains(c.Domain, "google") {
				filtered = append(filtered, c)
			}
		}

		googleCookies = filtered
		slog.Info("google_cookies_loaded", slog.Int("total", len(cookies)), slog.Int("google_filtered", len(filtered)))
	})

	return googleCookies, googleCookiesErr
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
		sameSite := playwright.SameSiteAttributeNone // default
		switch strings.ToLower(c.SameSite) {
		case "lax":
			sameSite = playwright.SameSiteAttributeLax
		case "strict":
			sameSite = playwright.SameSiteAttributeStrict
		case "none", "no_restriction":
			sameSite = playwright.SameSiteAttributeNone
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

	var parts []string
	for _, c := range cookies {
		parts = append(parts, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}

	return strings.Join(parts, "; ")
}
