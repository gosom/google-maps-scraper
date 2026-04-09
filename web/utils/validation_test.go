package utils

import (
	"strings"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// newValidJobData returns a JobData that satisfies every cap. Each test
// mutates a single field to assert the corresponding rejection branch.
func newValidJobData() *models.JobData {
	return &models.JobData{
		Keywords:   []string{"pizza"},
		Lang:       "en",
		Depth:      5,
		MaxResults: 10,
		ReviewsMax: 0,
		ImagesMax:  0,
		MaxTime:    60 * time.Second,
	}
}

func TestValidateJobData_AcceptsValidPayload(t *testing.T) {
	t.Parallel()
	if err := ValidateJobData(newValidJobData()); err != nil {
		t.Fatalf("expected valid payload to pass, got: %v", err)
	}
}

// TestValidateJobData_RejectsZeroMaxResults guards the SERVICE-LAYER
// behavior: ValidateJobData stays strict and rejects MaxResults=0 because
// the API entry point is responsible for calling ApplyJobDataDefaults
// first. A service-layer caller that constructs JobData by hand and
// forgets to set MaxResults is a programming error, not a missing-field
// API request.
func TestValidateJobData_RejectsZeroMaxResults(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxResults = 0
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for max_results=0, got nil")
	}
	if !strings.Contains(err.Error(), "max_results") {
		t.Errorf("expected error to mention max_results, got: %v", err)
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
		Lang:     "en",
		// Depth, MaxResults, MaxTime intentionally zero
	}
	ApplyJobDataDefaults(d)
	if d.MaxResults != DefaultMaxResults {
		t.Errorf("MaxResults: got %d, want %d", d.MaxResults, DefaultMaxResults)
	}
	if d.Depth != DefaultDepth {
		t.Errorf("Depth: got %d, want %d", d.Depth, DefaultDepth)
	}
	if d.MaxTime != time.Duration(DefaultMaxTimeSeconds) {
		t.Errorf("MaxTime: got %d, want %d", d.MaxTime, DefaultMaxTimeSeconds)
	}
	// Toggle-off enrichments stay at zero — that IS the default.
	if d.ReviewsMax != 0 {
		t.Errorf("ReviewsMax should stay 0 (toggle off), got %d", d.ReviewsMax)
	}
	if d.ImagesMax != 0 {
		t.Errorf("ImagesMax should stay 0 (toggle off), got %d", d.ImagesMax)
	}
}

// TestApplyJobDataDefaults_PreservesNonZeroFields verifies the helper is
// idempotent on already-populated structs — calling it twice (or on a
// struct that the frontend filled explicitly) is a no-op.
func TestApplyJobDataDefaults_PreservesNonZeroFields(t *testing.T) {
	t.Parallel()
	d := &models.JobData{
		Keywords:   []string{"pizza"},
		Lang:       "en",
		Depth:      12,
		MaxResults: 250,
		MaxTime:    time.Duration(900),
		ReviewsMax: 30,
		ImagesMax:  5000,
	}
	ApplyJobDataDefaults(d)
	if d.Depth != 12 || d.MaxResults != 250 || d.MaxTime != 900 || d.ReviewsMax != 30 || d.ImagesMax != 5000 {
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
	d.MaxTime = 2 * time.Hour
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
	d.MaxTime = 1 * time.Hour
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
	d.ImagesMax = 40_001
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
	d.ReviewsMax = 9999
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
	d.ReviewsMax = 0
	if err := ValidateJobData(d); err != nil {
		t.Fatalf("expected reviews_max=0 (skip) to pass, got: %v", err)
	}
}

func TestValidateJobData_RejectsImagesMaxAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.ImagesMax = 50_000
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
	d.ImagesMax = 0
	if err := ValidateJobData(d); err != nil {
		t.Fatalf("expected images_max=0 (skip) to pass, got: %v", err)
	}
}

func TestValidateJobData_RejectsLangNotInAllowlist(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.Lang = "xx"
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
	d.Lang = "EN"
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
	d.MaxTime = 90 * time.Minute
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
