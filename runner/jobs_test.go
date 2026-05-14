package runner

import (
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
)

// TestCreateSeedJobs_PassesImagesPerPlaceToGmapJob verifies that the
// per-place image cap is propagated from SeedJobConfig through
// CreateSeedJobs into every spawned GmapJob. Replaces the legacy
// per-job-total ImageBudget pointer test (May 2026 — Cafe Schöneberg fix).
func TestCreateSeedJobs_PassesImagesPerPlaceToGmapJob(t *testing.T) {
	t.Parallel()

	cfg := SeedJobConfig{
		LangCode:       "en",
		Input:          strings.NewReader("pizza\nburger\n"),
		MaxDepth:       5,
		IncludeEmails:  false,
		Images:         true,
		ImagesPerPlace: 10,
		MaxResults:     10,
	}

	jobs, err := CreateSeedJobs(cfg)
	if err != nil {
		t.Fatalf("CreateSeedJobs failed: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 seed jobs, got %d", len(jobs))
	}

	for i, j := range jobs {
		gj, ok := j.(*gmaps.GmapJob)
		if !ok {
			t.Fatalf("seed job %d is not a *gmaps.GmapJob: %T", i, j)
		}
		if gj.ImagesPerPlace != 10 {
			t.Errorf("seed job %d ImagesPerPlace = %d, want 10", i, gj.ImagesPerPlace)
		}
	}
}

// TestCreateSeedJobs_ZeroImagesPerPlaceWhenNotSet verifies that without
// the per-place cap, GmapJob.ImagesPerPlace stays 0 and PlaceJob.Process
// drops the JSON-payload images entirely (toggle-off path).
func TestCreateSeedJobs_ZeroImagesPerPlaceWhenNotSet(t *testing.T) {
	t.Parallel()

	cfg := SeedJobConfig{
		LangCode:      "en",
		Input:         strings.NewReader("pizza\n"),
		MaxDepth:      5,
		IncludeEmails: false,
		Images:        false,
		MaxResults:    10,
		// ImagesPerPlace: 0 — explicit
	}

	jobs, err := CreateSeedJobs(cfg)
	if err != nil {
		t.Fatalf("CreateSeedJobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 seed job, got %d", len(jobs))
	}

	gj, ok := jobs[0].(*gmaps.GmapJob)
	if !ok {
		t.Fatalf("seed job is not a *gmaps.GmapJob: %T", jobs[0])
	}
	if gj.ImagesPerPlace != 0 {
		t.Errorf("ImagesPerPlace = %d, want 0 (no per-place cap configured)", gj.ImagesPerPlace)
	}
}
