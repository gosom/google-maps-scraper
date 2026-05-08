package webrunner

import (
	"fmt"
	"strings"
)

// sanitizeSeedError translates a raw seed-level scrape error into a short,
// user-facing failure_reason that's safe to surface in the UI and useful for
// support triage (the user can read it back; on-call can correlate to the
// raw error in Loki via job_id). Always log the raw error separately at
// ERROR with the job ID — this function is for the failure_reason field
// that lands in the jobs table and shows in the frontend.
//
// The sanitization rules favour:
//   - SHORT (one line, no stack traces, no full URLs)
//   - DOMAIN-RECOGNIZABLE (mention "proxy", "DNS", "timeout" rather than
//     leaking internal library names like "playwright" or "Frame.Goto")
//   - DETERMINISTIC (a given error class always maps to the same string —
//     so log aggregators and support runbooks can match)
//
// Unrecognized errors fall through to a generic "Scraping aborted: scrape
// engine error" — never the raw err.Error() (which can leak proxy URLs,
// internal hostnames, or stack-trace fragments).
func sanitizeSeedError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "ERR_PROXY_CONNECTION_FAILED"):
		return "Scraping aborted: proxy connection failed"
	case strings.Contains(s, "ERR_TUNNEL_CONNECTION_FAILED"):
		return "Scraping aborted: proxy tunnel failed"
	case strings.Contains(s, "ERR_NAME_NOT_RESOLVED"):
		return "Scraping aborted: DNS resolution failed"
	case strings.Contains(s, "ERR_CONNECTION_REFUSED"):
		return "Scraping aborted: target connection refused"
	case strings.Contains(s, "ERR_CONNECTION_RESET"):
		return "Scraping aborted: target connection reset"
	case strings.Contains(s, "ERR_CONNECTION_TIMED_OUT"),
		strings.Contains(s, "ERR_TIMED_OUT"):
		return "Scraping aborted: connection timed out"
	case strings.Contains(s, "ERR_INTERNET_DISCONNECTED"):
		return "Scraping aborted: network unavailable"
	case strings.Contains(s, "ERR_CERT_"):
		return "Scraping aborted: TLS/certificate error"
	case strings.Contains(s, "playwright: net::"):
		// Other Chromium net:: codes — extract the ERR_* token without
		// leaking surrounding URL/path detail.
		if i := strings.Index(s, "net::"); i >= 0 {
			tail := s[i+len("net::"):]
			// ERR_* tokens are word characters and underscores; cut at the
			// first whitespace, " at ", or end of string.
			end := len(tail)
			for k, r := range tail {
				if r == ' ' || r == '\t' || r == '\n' {
					end = k
					break
				}
			}
			tok := tail[:end]
			if tok != "" {
				return fmt.Sprintf("Scraping aborted: network error (%s)", tok)
			}
		}
		return "Scraping aborted: network error"
	case strings.Contains(s, "Frame.Goto"),
		strings.Contains(s, "page.goto"):
		// Generic playwright navigation failure with no recognizable net::
		// code (e.g. browser context closed, page crashed mid-navigation).
		return "Scraping aborted: page failed to load"
	default:
		return "Scraping aborted: scrape engine error"
	}
}
