package gmaps

import (
	"testing"
)

// These tests pin the option boundary between the runner and the scraper
// for the per-place image cap. The legacy *atomic.Int64 per-job-total
// budget was replaced in May 2026 (Cafe Schöneberg fix) — see
// applyPerPlaceImageCap for the contract.

func TestWithImagesPerPlace_AttachesValue(t *testing.T) {
	t.Parallel()

	job := NewGmapJob("id-1", "en", "pizza", 5, false, true, 0, "", 0,
		WithImagesPerPlace(10),
	)

	if job.ImagesPerPlace != 10 {
		t.Errorf("GmapJob.ImagesPerPlace = %d, want 10", job.ImagesPerPlace)
	}
}

func TestWithImagesPerPlace_ZeroByDefault(t *testing.T) {
	t.Parallel()

	job := NewGmapJob("id-1", "en", "pizza", 5, false, true, 0, "", 0)

	if job.ImagesPerPlace != 0 {
		t.Errorf("GmapJob.ImagesPerPlace = %d, want 0 (no option supplied)", job.ImagesPerPlace)
	}
}

func TestWithPlaceJobImagesPerPlace_AttachesValue(t *testing.T) {
	t.Parallel()

	job := NewPlaceJob("parent", "en", "https://example.com/place", false, true, 0,
		WithPlaceJobImagesPerPlace(10),
	)

	if job.ImagesPerPlace != 10 {
		t.Errorf("PlaceJob.ImagesPerPlace = %d, want 10", job.ImagesPerPlace)
	}
}
