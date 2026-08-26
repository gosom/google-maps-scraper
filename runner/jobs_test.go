package runner_test

import (
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/grid"
	"github.com/gosom/google-maps-scraper/runner"
)

func TestCreateGridSeedJobsRejectsInvalidZoom(t *testing.T) {
	t.Parallel()

	bbox := grid.BoundingBox{
		MinLat: 40.30,
		MinLon: -3.80,
		MaxLat: 40.50,
		MaxLon: -3.60,
	}

	_, err := runner.CreateGridSeedJobs(
		"en",
		strings.NewReader("coffee"),
		10,
		false,
		bbox,
		1.0,
		0,
		nil,
		nil,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid zoom level") {
		t.Fatalf("expected invalid zoom level error, got %v", err)
	}
}

func TestCreateSeedJobsRejectsEmptyQueryBeforeCustomID(t *testing.T) {
	t.Parallel()

	_, err := runner.CreateSeedJobs(
		false,
		"en",
		strings.NewReader("  #!#my-id\n"),
		10,
		false,
		"",
		15,
		10000,
		nil,
		nil,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "empty query text") {
		t.Fatalf("expected empty query text error, got %v", err)
	}
}

func TestCreateGridSeedJobsRejectsEmptyQueryBeforeCustomID(t *testing.T) {
	t.Parallel()

	bbox := grid.BoundingBox{
		MinLat: 40.30,
		MinLon: -3.80,
		MaxLat: 40.50,
		MaxLon: -3.60,
	}

	_, err := runner.CreateGridSeedJobs(
		"en",
		strings.NewReader(" #!#my-id\n"),
		10,
		false,
		bbox,
		1.0,
		15,
		nil,
		nil,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "empty query text") {
		t.Fatalf("expected empty query text error, got %v", err)
	}
}

func TestCreateSeedJobsSkipsCompletedInputs(t *testing.T) {
	t.Parallel()

	jobs, err := runner.CreateSeedJobs(
		false,
		"en",
		strings.NewReader("coffee\ntea\n"),
		10,
		false,
		"",
		15,
		10000,
		nil,
		nil,
		false,
		runner.WithDeterministicSeedIDs(),
		runner.WithCompletedInputSkipper(func(inputID string) bool {
			return strings.HasPrefix(inputID, "resume:")
		}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(jobs) != 0 {
		t.Fatalf("expected completed deterministic inputs to be skipped, got %d jobs", len(jobs))
	}
}

func TestCreateSeedJobsAddsCompletionTracker(t *testing.T) {
	t.Parallel()

	tracker := &recordingSeedTracker{}

	jobs, err := runner.CreateSeedJobs(
		false,
		"en",
		strings.NewReader("coffee\n"),
		10,
		false,
		"",
		15,
		10000,
		nil,
		nil,
		false,
		runner.WithCompletionTracker(tracker),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job, ok := jobs[0].(*gmaps.GmapJob)
	if !ok {
		t.Fatalf("expected *gmaps.GmapJob, got %T", jobs[0])
	}

	if job.CompletionTracker != tracker {
		t.Fatalf("completion tracker was not attached")
	}
}

type recordingSeedTracker struct{}

func (t *recordingSeedTracker) SeedDiscovered(string, int) error {
	return nil
}
