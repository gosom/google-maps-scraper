package utils

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// Local aliases for the cap constants — the actual source of truth lives in
// cap_params.go. These exist so future readers of validation.go don't need
// to chase the value through cap_params.go.
const (
	minDepth      = 1
	maxKeywords   = 5
	maxKeywordLen = 200
	minMaxTime    = 60 * time.Second
	maxMaxTime    = time.Duration(CapMaxTimeSeconds) * time.Second

	// maxProxiesPerJob caps the number of proxy URLs in a single job
	// request. The struct tag in models/job.go enforces the same value;
	// this constant is the source of truth and the service-layer check
	// is defensive (catches non-HTTP callers like CLI/workers).
	maxProxiesPerJob = 100

	// maxProxyURLLen caps each individual proxy URL string length. Same
	// rationale as the struct tag — bound the worst-case length so we
	// don't pass arbitrarily large strings to url.Parse and the
	// downstream proxy library.
	maxProxyURLLen = 2048
)

// allowedProxySchemes is the closed set of URL schemes the scraper
// accepts as proxies. Anything else is rejected at validation time.
//
// Why these four:
//   - http/https: standard HTTP proxies (CONNECT-tunneled HTTPS)
//   - socks5:     SOCKS5 with client-side DNS
//   - socks5h:    SOCKS5 with proxy-side DNS resolution
//
// Notably absent: file://, gopher://, javascript:, data:, ftp://. None
// of these have a meaningful "proxy" interpretation, but a relaxed
// scheme check would let an attacker funnel internal file reads through
// the scraper. This is the proxy-side equivalent of the URL scheme
// check the audit plan also calls out for webhook URLs.
var allowedProxySchemes = map[string]struct{}{
	"http":    {},
	"https":   {},
	"socks5":  {},
	"socks5h": {},
}

// ValidateProxyURL parses a proxy URL string and rejects it if:
//
//   - the URL is malformed or missing a scheme/host
//   - the scheme is not in allowedProxySchemes
//   - the host (after DNS resolution) lands in any private, loopback,
//     link-local, unspecified, or cloud-metadata range
//   - the URL exceeds maxProxyURLLen bytes
//
// The SSRF defense reuses CheckIPBlocklist via AssertPublicHost from
// private_ip.go, keeping the proxy and webhook validators in sync.
//
// Known limitation — DNS TOCTOU: this validator resolves the host at
// validation time. An attacker who controls a DNS record can return a
// public IP at validation time and 169.254.169.254 at scrape time,
// bypassing this check. The complete fix lives at the HTTP transport
// layer (a custom Dialer.Control hook that re-validates the resolved
// IP just before the TCP connect). That layer lives inside the
// scrapemate library, not in our code — we cannot fix it here without
// forking. Track the upstream issue in audit plan §3.5.
//
// The validation-time check is still valuable: it blocks the naive
// attack and forces the adversary into an active DNS attack rather
// than a passive one. The remaining defense-in-depth recommendation
// is to disable the `proxies` field entirely for free-tier users via
// a feature flag.
func ValidateProxyURL(raw string) error {
	if raw == "" {
		return errors.New("proxy URL is empty")
	}
	if len(raw) > maxProxyURLLen {
		return fmt.Errorf("proxy URL exceeds %d bytes", maxProxyURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if _, ok := allowedProxySchemes[scheme]; !ok {
		return fmt.Errorf("proxy scheme %q is not allowed (allowed: http, https, socks5, socks5h)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("proxy URL is missing a host")
	}
	// AssertPublicHost does DNS resolution + per-IP CheckIPBlocklist
	// against EVERY resolved address. A dual-homed host that returns
	// [8.8.8.8, 127.0.0.1] is correctly rejected.
	if _, err := AssertPublicHost(host); err != nil {
		return fmt.Errorf("proxy host %q rejected: %w", host, err)
	}
	return nil
}

// ApplyJobDataDefaults fills in safe defaults for any zero-valued OPTIONAL
// fields. Call this at the API entry point AFTER JSON decode and BEFORE
// struct-tag validation. Required fields (Keywords, Lang) are not touched —
// missing them is still a 400.
//
// Defaults are conservative (REST best practice / OWASP API4:2023). The
// frontend is responsible for sending generous "no cap" values when the
// user picks the corresponding UX toggles. See cap_params.go for the
// full design rationale.
//
// MaxTime uses the DurationSec custom type which auto-converts between
// seconds (JSON wire format) and nanoseconds (internal time.Duration).
// No manual multiplication is needed in the HTTP handler.
//
// Idempotent: applying defaults to an already-populated struct is a no-op.
func ApplyJobDataDefaults(d *models.JobData) {
	if d == nil {
		return
	}
	if d.MaxResults == 0 {
		d.MaxResults = DefaultMaxResults
	}
	if d.Depth == 0 {
		d.Depth = DefaultDepth
	}
	if d.MaxTime == 0 {
		d.MaxTime = models.DurationSec(DefaultMaxTimeSeconds * time.Second)
	}
	// ReviewsMax, ImagesMax, Radius default to 0 (toggle-off / no
	// constraint), so a zero value is the intended default — no fill.
}

// ValidateJob validates a job payload.
func ValidateJob(j *models.Job) error {
	if j.ID == "" {
		return errors.New("missing id")
	}
	if j.Name == "" {
		return errors.New("missing name")
	}
	if j.Status == "" {
		return errors.New("missing status")
	}
	if j.Date.IsZero() {
		return errors.New("missing date")
	}
	return ValidateJobData(&j.Data)
}

// ValidateJobData enforces resource consumption limits (CWE-400) at the
// service layer in addition to the struct-tag validation performed at the
// HTTP layer, so that non-HTTP callers (CLI, workers, internal queues) are
// also protected. All caps are sourced from cap_params.go. There is NO
// "unlimited" sentinel — every integer field has a strict min and max.
func ValidateJobData(d *models.JobData) error {
	// Keywords
	if len(d.Keywords) == 0 {
		return errors.New("missing keywords")
	}
	if len(d.Keywords) > maxKeywords {
		return fmt.Errorf("too many keywords: maximum is %d", maxKeywords)
	}
	for i, kw := range d.Keywords {
		if kw == "" {
			return fmt.Errorf("keyword[%d] is empty", i)
		}
		// validator's `max=200` counts BYTES via len(), not runes.
		// Mirror that here so the boundary is identical at both layers.
		if len(kw) > maxKeywordLen {
			return fmt.Errorf("keyword[%d] exceeds %d bytes", i, maxKeywordLen)
		}
	}

	// Lang — must be in the launch allowlist.
	if d.Lang == "" {
		return errors.New("missing lang")
	}
	if len(d.Lang) != 2 {
		return errors.New("invalid lang: expected 2-character ISO 639-1 code")
	}
	if !IsSupportedLang(d.Lang) {
		return fmt.Errorf("unsupported lang %q: must be a 2-character ISO 639-1 code in the supported allowlist", d.Lang)
	}

	// Depth
	if d.Depth < minDepth {
		return fmt.Errorf("depth must be >= %d", minDepth)
	}
	if d.Depth > CapDepth {
		return fmt.Errorf("depth exceeds maximum of %d", CapDepth)
	}

	// MaxTime — required, bounded both directions.
	if d.MaxTime == 0 {
		return errors.New("missing max_time")
	}
	if d.MaxTime.Duration() < minMaxTime {
		return fmt.Errorf("max_time must be >= %s", minMaxTime)
	}
	if d.MaxTime.Duration() > maxMaxTime {
		return fmt.Errorf("max_time exceeds maximum of %s", maxMaxTime)
	}

	// FastMode requires geo coordinates.
	if d.FastMode && (d.Lat == "" || d.Lon == "") {
		return errors.New("missing geo coordinates: fast_mode requires lat and lon")
	}

	// Lat/Lon range — the struct tag handles `latitude`/`longitude` parse,
	// but service-layer callers may not run the validator, so re-check here.
	if d.Lat != "" {
		lat, err := strconv.ParseFloat(d.Lat, 64)
		if err != nil {
			return fmt.Errorf("invalid lat: %v", err)
		}
		if lat < -90 || lat > 90 {
			return errors.New("lat must be in [-90, 90]")
		}
	}
	if d.Lon != "" {
		lon, err := strconv.ParseFloat(d.Lon, 64)
		if err != nil {
			return fmt.Errorf("invalid lon: %v", err)
		}
		if lon < -180 || lon > 180 {
			return errors.New("lon must be in [-180, 180]")
		}
	}

	// MaxResults — strict minimum 1, no more 0=unlimited.
	if d.MaxResults < 1 {
		return errors.New("max_results must be >= 1 (no unlimited sentinel)")
	}
	if d.MaxResults > CapMaxResults {
		return fmt.Errorf("max_results exceeds maximum of %d", CapMaxResults)
	}

	// ReviewsMax — per place. 0 means "skip reviews".
	if d.ReviewsMax < 0 {
		return errors.New("reviews_max cannot be negative")
	}
	if d.ReviewsMax > CapReviewsMax {
		return fmt.Errorf("reviews_max exceeds maximum of %d (per place)", CapReviewsMax)
	}

	// ImagesMax — per-job total across all places. 0 means "skip images".
	if d.ImagesMax < 0 {
		return errors.New("images_max cannot be negative")
	}
	if d.ImagesMax > CapImagesMaxTotal {
		return fmt.Errorf("images_max exceeds maximum of %d (per-job total)", CapImagesMaxTotal)
	}

	// Radius — bounded both directions.
	if d.Radius < 0 {
		return errors.New("radius cannot be negative")
	}
	if d.Radius > CapRadiusMeters {
		return fmt.Errorf("radius exceeds maximum of %d meters", CapRadiusMeters)
	}

	// Proxies — element count cap and per-element SSRF + scheme check.
	// The struct tag in models/job.go enforces the count and per-element
	// length at the HTTP boundary; the service-layer check below catches
	// non-HTTP callers (CLI, workers, internal queues) and runs the SSRF
	// defense which the validator/v10 tag layer cannot do (no DNS access).
	if len(d.Proxies) > maxProxiesPerJob {
		return fmt.Errorf("proxies exceeds maximum of %d", maxProxiesPerJob)
	}
	for i, p := range d.Proxies {
		if err := ValidateProxyURL(p); err != nil {
			return fmt.Errorf("proxies[%d]: %w", i, err)
		}
	}

	return nil
}
