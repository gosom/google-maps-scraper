package webrunner

import (
	"errors"
	"testing"
)

// TestSanitizeSeedError covers each documented mapping plus the generic
// fallback. Each test pins both the input pattern AND the exact output
// string — log aggregators and support runbooks match on these strings,
// so they should not drift silently.
func TestSanitizeSeedError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   error
		want string
	}{
		// nil
		{"nil error → empty", nil, ""},

		// Recognized Chromium net:: codes
		{
			name: "ERR_PROXY_CONNECTION_FAILED",
			in:   errors.New("Frame.Goto https://www.google.com/maps/search/x?hl=en: playwright: net::ERR_PROXY_CONNECTION_FAILED at https://www.google.com/maps/search/x?hl=en"),
			want: "Scraping aborted: proxy connection failed",
		},
		{
			name: "ERR_TUNNEL_CONNECTION_FAILED",
			in:   errors.New("playwright: net::ERR_TUNNEL_CONNECTION_FAILED at https://x"),
			want: "Scraping aborted: proxy tunnel failed",
		},
		{
			name: "ERR_NAME_NOT_RESOLVED",
			in:   errors.New("playwright: net::ERR_NAME_NOT_RESOLVED at https://x"),
			want: "Scraping aborted: DNS resolution failed",
		},
		{
			name: "ERR_CONNECTION_REFUSED",
			in:   errors.New("playwright: net::ERR_CONNECTION_REFUSED at https://x"),
			want: "Scraping aborted: target connection refused",
		},
		{
			name: "ERR_CONNECTION_RESET",
			in:   errors.New("playwright: net::ERR_CONNECTION_RESET at https://x"),
			want: "Scraping aborted: target connection reset",
		},
		{
			name: "ERR_CONNECTION_TIMED_OUT",
			in:   errors.New("playwright: net::ERR_CONNECTION_TIMED_OUT at https://x"),
			want: "Scraping aborted: connection timed out",
		},
		{
			name: "ERR_TIMED_OUT (alias)",
			in:   errors.New("playwright: net::ERR_TIMED_OUT at https://x"),
			want: "Scraping aborted: connection timed out",
		},
		{
			name: "ERR_INTERNET_DISCONNECTED",
			in:   errors.New("playwright: net::ERR_INTERNET_DISCONNECTED at https://x"),
			want: "Scraping aborted: network unavailable",
		},
		{
			name: "ERR_CERT_AUTHORITY_INVALID",
			in:   errors.New("playwright: net::ERR_CERT_AUTHORITY_INVALID at https://x"),
			want: "Scraping aborted: TLS/certificate error",
		},

		// Unknown net:: code → token extracted
		{
			name: "unknown net:: token extracted",
			in:   errors.New("playwright: net::ERR_HTTP2_PROTOCOL_ERROR at https://x"),
			want: "Scraping aborted: network error (ERR_HTTP2_PROTOCOL_ERROR)",
		},

		// Playwright nav with no net:: code
		{
			name: "Frame.Goto without net:: code",
			in:   errors.New("Frame.Goto https://x: page closed"),
			want: "Scraping aborted: page failed to load",
		},

		// Generic fallback — never leak raw err.Error()
		{
			name: "unknown error → generic fallback",
			in:   errors.New("something completely unexpected with proxy details: socks5://user:pw@host:1234"),
			want: "Scraping aborted: scrape engine error",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeSeedError(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeSeedError(%v):\n  want %q\n  got  %q", tc.in, tc.want, got)
			}
		})
	}
}

// TestSanitizeSeedError_NeverLeaksRawError is a guard: no matter what the
// input contains (URLs, credentials, stack traces), the output must never
// equal err.Error() verbatim — that would indicate the catch-all branch
// regressed to leaking raw errors into failure_reason.
func TestSanitizeSeedError_NeverLeaksRawError(t *testing.T) {
	t.Parallel()
	leaky := errors.New("playwright: socks5://USER:SECRET@gate.decodo.com:10001 connection refused\n\tstack trace ...")
	got := sanitizeSeedError(leaky)
	if got == leaky.Error() {
		t.Errorf("sanitizer leaked raw error verbatim into failure_reason: %q", got)
	}
	if got == "" {
		t.Errorf("sanitizer returned empty string for non-nil error (callers store this in DB and display in UI — must be non-empty)")
	}
}
