package utils

import (
	"strings"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// durSec is a test helper that constructs a DurationSec from a time.Duration.
func durSec(d time.Duration) models.DurationSec { return models.DurationSec(d) }

// newValidJobData returns a JobData that satisfies every cap. Each test
// mutates a single field to assert the corresponding rejection branch.
func newValidJobData() *models.JobData {
	return &models.JobData{
		Keywords:   []string{"pizza"},
		Language:   "en",
		Depth:      5,
		MaxResults: 10,
		MaxReviews: 0,
		MaxImages:  0,
		MaxTime:    durSec(60 * time.Second),
	}
}

func TestValidateJobData_AcceptsValidPayload(t *testing.T) {
	t.Parallel()
	if err := ValidateJobData(newValidJobData()); err != nil {
		t.Fatalf("expected valid payload to pass, got: %v", err)
	}
}

// TestValidateJobData_AcceptsZeroMaxResults ensures MaxResults=0 is valid
// (meaning "no cap" — the scraper uses depth-based natural yield).
func TestValidateJobData_AcceptsZeroMaxResults(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxResults = 0
	err := ValidateJobData(d)
	if err != nil {
		t.Fatalf("expected max_results=0 to be valid (no cap), got error: %v", err)
	}
}

// TestApplyJobDataDefaults_FillsZeroFields locks in the API safety net:
// callers that omit optional fields get the documented conservative
// defaults. This is the function the HTTP handler calls between JSON
// decode and validate.Struct.
func TestApplyJobDataDefaults_FillsZeroFields(t *testing.T) {
	t.Parallel()
	d := &models.JobData{
		Keywords: []string{"pizza"},
		Language: "en",
		// Depth, MaxResults, MaxTime intentionally zero
	}
	ApplyJobDataDefaults(d)
	// MaxResults 0 means "no cap" — defaults should NOT override it.
	if d.MaxResults != 0 {
		t.Errorf("MaxResults: got %d, want 0 (no cap)", d.MaxResults)
	}
	if d.Depth != DefaultDepth {
		t.Errorf("Depth: got %d, want %d", d.Depth, DefaultDepth)
	}
	wantMaxTime := models.DurationSec(DefaultMaxTimeSeconds * time.Second)
	if d.MaxTime != wantMaxTime {
		t.Errorf("MaxTime: got %v, want %v", d.MaxTime, wantMaxTime)
	}
	// Toggle-off enrichments stay at zero — that IS the default.
	if d.MaxReviews != 0 {
		t.Errorf("MaxReviews should stay 0 (toggle off), got %d", d.MaxReviews)
	}
	if d.MaxImages != 0 {
		t.Errorf("MaxImages should stay 0 (toggle off), got %d", d.MaxImages)
	}
}

// TestApplyJobDataDefaults_PreservesNonZeroFields verifies the helper is
// idempotent on already-populated structs — calling it twice (or on a
// struct that the frontend filled explicitly) is a no-op.
func TestApplyJobDataDefaults_PreservesNonZeroFields(t *testing.T) {
	t.Parallel()
	d := &models.JobData{
		Keywords:   []string{"pizza"},
		Language:   "en",
		Depth:      12,
		MaxResults: 250,
		MaxTime:    durSec(900 * time.Second),
		MaxReviews: 30,
		MaxImages:  5000,
	}
	ApplyJobDataDefaults(d)
	if d.Depth != 12 || d.MaxResults != 250 || d.MaxTime != durSec(900*time.Second) || d.MaxReviews != 30 || d.MaxImages != 5000 {
		t.Errorf("ApplyJobDataDefaults overwrote a non-zero field: %+v", d)
	}
}

// TestApplyJobDataDefaults_NilSafe protects the helper from a nil pointer
// — callers may invoke it defensively without checking.
func TestApplyJobDataDefaults_NilSafe(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ApplyJobDataDefaults(nil) panicked: %v", r)
		}
	}()
	ApplyJobDataDefaults(nil)
}

// TestValidateJobData_RejectsMaxTimeAboveCap_OneHour locks in the new
// 1-hour ceiling on max_time (was 4 hours). Anything above 1h is a 400.
// See cap_params.go for the headless-browser reasoning.
func TestValidateJobData_RejectsMaxTimeAboveCap_OneHour(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxTime = durSec(2 * time.Hour)
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for max_time=2h, got nil")
	}
	if !strings.Contains(err.Error(), "max_time") {
		t.Errorf("expected error to mention max_time, got: %v", err)
	}
}

// TestValidateJobData_AcceptsMaxTimeAtCap_OneHour confirms 1 hour exactly
// is at the boundary (inclusive) and passes.
func TestValidateJobData_AcceptsMaxTimeAtCap_OneHour(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxTime = durSec(1 * time.Hour)
	if err := ValidateJobData(d); err != nil {
		t.Errorf("expected max_time=1h to pass at the cap boundary, got: %v", err)
	}
}

// TestValidateJobData_RejectsImagesMaxAbove40k_NewCeiling locks the new
// 40 000 ceiling for images_max (was 20 000). Sized for production
// concurrency 8 × max_time 1h ≈ 480 places × ~80 images/place.
func TestValidateJobData_RejectsImagesMaxAbove40k_NewCeiling(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxImages = 40_001
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for images_max=40001, got nil")
	}
	if !strings.Contains(err.Error(), "40000") {
		t.Errorf("expected error to mention cap 40000, got: %v", err)
	}
}

func TestValidateJobData_RejectsMaxResultsAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxResults = 1000
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for max_results=1000, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention cap 500, got: %v", err)
	}
}

func TestValidateJobData_RejectsReviewsMaxAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxReviews = 9999
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for reviews_max=9999, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention cap 500, got: %v", err)
	}
}

func TestValidateJobData_AcceptsZeroReviewsMaxAsSkip(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxReviews = 0
	if err := ValidateJobData(d); err != nil {
		t.Fatalf("expected reviews_max=0 (skip) to pass, got: %v", err)
	}
}

func TestValidateJobData_RejectsImagesMaxAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxImages = 50_000
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for images_max=50000, got nil")
	}
	if !strings.Contains(err.Error(), "40000") {
		t.Errorf("expected error to mention cap 40000, got: %v", err)
	}
}

func TestValidateJobData_AcceptsZeroImagesMaxAsSkip(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxImages = 0
	if err := ValidateJobData(d); err != nil {
		t.Fatalf("expected images_max=0 (skip) to pass, got: %v", err)
	}
}

func TestValidateJobData_RejectsLangNotInAllowlist(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Language = "xx"
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for lang=xx, got nil")
	}
	if !strings.Contains(err.Error(), "lang") {
		t.Errorf("expected error to mention lang, got: %v", err)
	}
}

func TestValidateJobData_RejectsLangWrongCase(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Language = "EN"
	if err := ValidateJobData(d); err == nil {
		t.Fatal("expected error for lang=EN (uppercase), got nil")
	}
}

func TestValidateJobData_RejectsLatOutOfRange(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Lat = "100.0"
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for lat=100.0, got nil")
	}
	if !strings.Contains(err.Error(), "lat") {
		t.Errorf("expected error to mention lat, got: %v", err)
	}
}

func TestValidateJobData_RejectsLonOutOfRange(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Lon = "200.0"
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for lon=200.0, got nil")
	}
	if !strings.Contains(err.Error(), "lon") {
		t.Errorf("expected error to mention lon, got: %v", err)
	}
}

func TestValidateJobData_RejectsRadiusAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Radius = 60000
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for radius=60000, got nil")
	}
	if !strings.Contains(err.Error(), "50000") {
		t.Errorf("expected error to mention cap 50000, got: %v", err)
	}
}

func TestValidateJobData_RejectsDepthAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Depth = 21
	if err := ValidateJobData(d); err == nil {
		t.Fatal("expected error for depth=21, got nil")
	}
}

func TestValidateJobData_RejectsZeroMaxTime(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxTime = 0
	if err := ValidateJobData(d); err == nil {
		t.Fatal("expected error for max_time=0, got nil")
	}
}

// TestValidateJobData_RejectsMaxTimeAboveCap covers the post-Task-2.4
// 1-hour ceiling. The previous 4-hour ceiling was unrealistic for headless
// Chromium scraping Google Maps — see cap_params.go for the rationale.
func TestValidateJobData_RejectsMaxTimeAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxTime = durSec(90 * time.Minute)
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for max_time=90m, got nil")
	}
	if !strings.Contains(err.Error(), "max_time") {
		t.Errorf("expected error to mention max_time, got: %v", err)
	}
}

func TestValidateJobData_RejectsEmptyKeyword(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Keywords = []string{""}
	if err := ValidateJobData(d); err == nil {
		t.Fatal("expected error for empty keyword, got nil")
	}
}

func TestValidateJobData_RejectsOverlongKeyword(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Keywords = []string{strings.Repeat("a", 201)}
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for 201-byte keyword, got nil")
	}
	if !strings.Contains(err.Error(), "200") {
		t.Errorf("expected error to mention 200-byte cap, got: %v", err)
	}
}

func TestValidateJobData_RejectsTooManyKeywords(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Keywords = []string{"a", "b", "c", "d", "e", "f"}
	if err := ValidateJobData(d); err == nil {
		t.Fatal("expected error for 6 keywords, got nil")
	}
}

// ───────────────────── Task 3.5: proxies SSRF + caps ─────────────────────

// TestValidateJobData_RejectsTooManyProxies locks the per-job element
// cap. Without this, a client can submit a 10 000-element proxies array
// and stall the validator on per-element DNS lookups.
func TestValidateJobData_RejectsTooManyProxies(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Proxies = make([]string, 101)
	for i := range d.Proxies {
		d.Proxies[i] = "http://proxy.example.com:8080"
	}
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for 101 proxies, got nil")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("expected error to mention the 100-element cap, got: %v", err)
	}
}

// TestValidateProxyURL_RejectsPrivateIPs is the core SSRF defense.
// Each of these would be devastating if accepted: 127.0.0.1 hits any
// service on the scraper host, 169.254.169.254 hits AWS instance
// metadata (and would expose IAM credentials), 10.x/192.168.x reach
// the internal VPC. localhost resolves to a loopback address through
// the system DNS resolver — same outcome.
func TestValidateProxyURL_RejectsPrivateIPs(t *testing.T) {
	t.Parallel()
	cases := []string{
		"http://127.0.0.1:5432",
		"http://localhost:5432",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1:8080",
		"http://192.168.1.1:8080",
		"http://[::1]:8080",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			if err := ValidateProxyURL(p); err == nil {
				t.Errorf("expected %q to be rejected as private/internal", p)
			}
		})
	}
}

// TestValidateProxyURL_RejectsBadSchemes covers the scheme allowlist.
// file:// and gopher:// have no meaningful "proxy" interpretation but
// would let an attacker funnel internal file reads through the scraper
// if accepted. The scheme check is the cheap defense — it short-circuits
// before any DNS lookup.
func TestValidateProxyURL_RejectsBadSchemes(t *testing.T) {
	t.Parallel()
	cases := []string{
		"file:///etc/passwd",
		"gopher://attacker.example.com/",
		"javascript:alert(1)",
		"data:text/plain,hi",
		"ftp://example.com/",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			err := ValidateProxyURL(p)
			if err == nil {
				t.Errorf("expected %q to be rejected", p)
			}
			// Specifically expect the scheme-allowlist message — if we
			// accidentally fall through to a different validator path,
			// the error mentions something else and this assertion
			// catches the regression.
			if err != nil && !strings.Contains(err.Error(), "scheme") && !strings.Contains(err.Error(), "host") {
				t.Errorf("%q rejected for unexpected reason: %v", p, err)
			}
		})
	}
}

// TestValidateProxyURL_AcceptsValidPublicProxies — the positive path.
// Authentication credentials in the URL (user:pass@host) and SOCKS5
// schemes both pass.
//
// Uses literal public IPs (8.8.8.8 = Google DNS) and example.com (RFC
// 2606 reserved domain that's been live since 1999) to avoid depending
// on a flaky test-environment DNS for the canonical "should accept"
// path. Skips in -short mode for total isolation.
func TestValidateProxyURL_AcceptsValidPublicProxies(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("requires DNS resolution; skipped in short mode")
	}
	cases := []string{
		"http://user:pass@8.8.8.8:8080",
		"socks5://8.8.8.8:1080",
		"socks5h://1.1.1.1:1080",
		"https://example.com:8443",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			if err := ValidateProxyURL(p); err != nil {
				t.Errorf("expected %q to be accepted, got: %v", p, err)
			}
		})
	}
}

// TestValidateProxyURL_RejectsOversizeURL covers the byte-length cap.
func TestValidateProxyURL_RejectsOversizeURL(t *testing.T) {
	t.Parallel()
	long := "http://" + strings.Repeat("a", 2050) + ".example.com/"
	err := ValidateProxyURL(long)
	if err == nil {
		t.Fatal("expected error for oversize proxy URL, got nil")
	}
	if !strings.Contains(err.Error(), "2048") {
		t.Errorf("expected error to mention 2048-byte cap, got: %v", err)
	}
}

// TestValidateProxyURL_RejectsEmptyHost catches the URL parser
// accepting strings like "http:///path" with no hostname.
func TestValidateProxyURL_RejectsEmptyHost(t *testing.T) {
	t.Parallel()
	if err := ValidateProxyURL("http:///path"); err == nil {
		t.Fatal("expected error for missing host, got nil")
	}
}

// TestValidateProxyURL_RejectsEmpty catches the empty-string case so
// the loop in ValidateJobData doesn't silently accept padding.
func TestValidateProxyURL_RejectsEmpty(t *testing.T) {
	t.Parallel()
	if err := ValidateProxyURL(""); err == nil {
		t.Fatal("expected error for empty proxy URL, got nil")
	}
}
