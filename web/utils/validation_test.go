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
	d.ImagesMax = 30000
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for images_max=30000, got nil")
	}
	if !strings.Contains(err.Error(), "20000") {
		t.Errorf("expected error to mention cap 20000, got: %v", err)
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

func TestValidateJobData_RejectsMaxTimeAboveCap(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxTime = 5 * time.Hour
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for max_time=5h, got nil")
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
