package utils

import (
	"strings"
	"testing"
)

// After the Cafe Schöneberg bug (May 2026), max_images switched from a
// per-job total to a per-place cap. The new ceiling is 500 to match the
// frontend presets ([50, 100, 200, 500]). The previous 40k ceiling made
// no sense per-place — a single Google Maps business doesn't have 40k
// photos.

func TestValidateJobData_RejectsImagesMaxAbove500_PerPlace(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxImages = 501
	err := ValidateJobData(d)
	if err == nil {
		t.Fatal("expected error for max_images=501 (above per-place cap), got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error message to mention cap 500, got: %v", err)
	}
	if !strings.Contains(err.Error(), "per place") {
		t.Errorf("expected error message to clarify 'per place' semantics, got: %v", err)
	}
}

func TestValidateJobData_AcceptsImagesMaxAt500_PerPlaceCapBoundary(t *testing.T) {
	t.Parallel()
	d := newValidJobData()
	d.MaxImages = 500
	if err := ValidateJobData(d); err != nil {
		t.Fatalf("expected max_images=500 (cap boundary) to pass, got: %v", err)
	}
}
