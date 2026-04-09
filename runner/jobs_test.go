package runner

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
)

// TestCreateSeedJobs_PassesImageBudgetToGmapJob verifies that the per-job
// total image budget pointer is propagated from SeedJobConfig through
// CreateSeedJobs into every spawned GmapJob. The pointer must be the same
// instance — sharing it across PlaceJobs is the entire point of the
// cross-place enforcement (see gmaps.PlaceJob.extractImages).
func TestCreateSeedJobs_PassesImageBudgetToGmapJob(t *testing.T) {
	t.Parallel()

	budget := &atomic.Int64{}
	budget.Store(20000)

	cfg := SeedJobConfig{
		LangCode:    "en",
		Input:       strings.NewReader("pizza\nburger\n"),
		MaxDepth:    5,
		Email:       false,
		Images:      true,
		ImageBudget: budget,
		MaxResults:  10,
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
		if gj.ImageBudget == nil {
			t.Errorf("seed job %d has nil ImageBudget; expected the shared budget pointer", i)
			continue
		}
		if gj.ImageBudget != budget {
			t.Errorf("seed job %d has a different ImageBudget pointer than the one passed in", i)
		}
		if got := gj.ImageBudget.Load(); got != 20000 {
			t.Errorf("seed job %d budget = %d, want 20000", i, got)
		}
	}
}

// TestCreateSeedJobs_NilImageBudgetWhenNotSet verifies that when no image
// budget is configured (the CLI/lambda case), GmapJob.ImageBudget stays
// nil and PlaceJob.extractImages takes the unbounded path.
func TestCreateSeedJobs_NilImageBudgetWhenNotSet(t *testing.T) {
	t.Parallel()

	cfg := SeedJobConfig{
		LangCode:   "en",
		Input:      strings.NewReader("pizza\n"),
		MaxDepth:   5,
		Email:      false,
		Images:     true,
		MaxResults: 10,
		// ImageBudget: nil — explicit
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
	if gj.ImageBudget != nil {
		t.Error("expected nil ImageBudget when not configured, got non-nil")
	}
}

// TestCreateSeedJobs_BudgetSharedAcrossAllSeeds is the key test for the
// per-job-total semantic: every seed in a multi-keyword job must share
// the SAME counter pointer. Decrementing the counter from one seed must
// be visible to all sibling seeds.
func TestCreateSeedJobs_BudgetSharedAcrossAllSeeds(t *testing.T) {
	t.Parallel()

	budget := &atomic.Int64{}
	budget.Store(1000)

	cfg := SeedJobConfig{
		LangCode:    "en",
		Input:       strings.NewReader("pizza\nburger\nsushi\n"),
		MaxDepth:    5,
		ImageBudget: budget,
		MaxResults:  10,
	}

	jobs, err := CreateSeedJobs(cfg)
	if err != nil {
		t.Fatalf("CreateSeedJobs failed: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 seed jobs, got %d", len(jobs))
	}

	// Decrement the budget from the first seed and verify all siblings
	// observe the change. This proves the counter is shared by pointer,
	// not copied per seed.
	jobs[0].(*gmaps.GmapJob).ImageBudget.Add(-300)
	for i, j := range jobs {
		got := j.(*gmaps.GmapJob).ImageBudget.Load()
		if got != 700 {
			t.Errorf("seed %d: budget = %d after decrement, want 700 (shared counter)", i, got)
		}
	}
}
