package gmaps

import (
	"sync/atomic"
	"testing"
)

// TestWithImageBudget_AttachesPointer verifies that the WithImageBudget
// option installs the pointer on the GmapJob — this is the integration
// boundary between the runner and the scraper.
func TestWithImageBudget_AttachesPointer(t *testing.T) {
	t.Parallel()

	budget := &atomic.Int64{}
	budget.Store(15000)

	job := NewGmapJob("id-1", "en", "pizza", 5, false, true, 0, "", 0,
		WithImageBudget(budget),
	)

	if job.ImageBudget != budget {
		t.Errorf("GmapJob.ImageBudget != input pointer; expected the same instance")
	}
	if job.ImageBudget.Load() != 15000 {
		t.Errorf("GmapJob.ImageBudget.Load() = %d, want 15000", job.ImageBudget.Load())
	}
}

// TestWithImageBudget_NilByDefault verifies that without the option, the
// budget field is nil — this is the CLI/lambda code path that takes the
// unbounded image extraction route in PlaceJob.extractImages.
func TestWithImageBudget_NilByDefault(t *testing.T) {
	t.Parallel()

	job := NewGmapJob("id-1", "en", "pizza", 5, false, true, 0, "", 0)

	if job.ImageBudget != nil {
		t.Errorf("GmapJob.ImageBudget = %v, want nil (no option supplied)", job.ImageBudget)
	}
}

// TestWithPlaceJobImageBudget_AttachesPointer is the equivalent test for
// the PlaceJob option — PlaceJob.extractImages reads from this field.
func TestWithPlaceJobImageBudget_AttachesPointer(t *testing.T) {
	t.Parallel()

	budget := &atomic.Int64{}
	budget.Store(500)

	job := NewPlaceJob("parent", "en", "https://example.com/place", false, true, 0,
		WithPlaceJobImageBudget(budget),
	)

	if job.ImageBudget != budget {
		t.Errorf("PlaceJob.ImageBudget != input pointer; expected the same instance")
	}
}
