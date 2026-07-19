package scrapemate

import (
	"net/http"
	"time"
)

// WaitUntilState represents when navigation is considered complete.
type WaitUntilState string

const (
	// WaitUntilLoad waits until the load event is fired.
	WaitUntilLoad WaitUntilState = "load"
	// WaitUntilDOMContentLoaded waits until the DOMContentLoaded event is fired.
	WaitUntilDOMContentLoaded WaitUntilState = "domcontentloaded"
	// WaitUntilNetworkIdle waits until there are no network connections for at least 500 ms.
	WaitUntilNetworkIdle WaitUntilState = "networkidle"
)

// PageResponse contains the response information from a page navigation.
type PageResponse struct {
	// URL is the final URL after any redirects.
	URL string
	// StatusCode is the HTTP status code of the response.
	StatusCode int
	// Headers contains the response headers.
	Headers http.Header
	// Body contains the response body bytes.
	Body []byte
}

// Locator represents an element locator for finding elements on the page.
type Locator interface {
	// Click clicks on the first matching element.
	Click(timeout time.Duration) error
	// Count returns the number of matching elements.
	Count() (int, error)
	// First returns a locator for the first matching element.
	First() Locator
}

// BrowserPage is an abstraction over browser page implementations.
// It provides a common interface for browser automation libraries
// such as Playwright.
type BrowserPage interface {
	// Goto navigates to a URL and waits for the specified state.
	// Returns the page response with status code, headers, and body.
	Goto(url string, waitUntil WaitUntilState) (*PageResponse, error)

	// URL returns the current page URL.
	URL() string

	// Content returns the full HTML content of the page.
	Content() (string, error)

	// Reload reloads the current page.
	Reload(waitUntil WaitUntilState) error

	// Screenshot takes a screenshot of the page.
	// If fullPage is true, captures the entire scrollable page.
	Screenshot(fullPage bool) ([]byte, error)

	// Eval executes JavaScript in the page context and returns the result.
	Eval(js string, args ...any) (any, error)

	// WaitForURL waits until the page URL matches the given pattern.
	WaitForURL(url string, timeout time.Duration) error

	// WaitForSelector waits for an element matching the selector to appear.
	WaitForSelector(selector string, timeout time.Duration) error

	// WaitForTimeout waits for the specified duration.
	// Note: This is generally discouraged in favor of waiting for specific conditions.
	WaitForTimeout(timeout time.Duration)

	// Locator creates a locator for finding elements matching the selector.
	Locator(selector string) Locator

	// Close closes the page and releases resources.
	Close() error

	// Unwrap returns the underlying page object (e.g., playwright.Page).
	// This allows users to access library-specific features when needed.
	Unwrap() any
}
