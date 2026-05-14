package gmaps

import (
	"testing"
)

// These tests pin the per-place image cap semantics introduced after the
// Cafe Schöneberg 504-images-on-a-10-per-place job bug (May 2026).
//
// Contract:
//   - cap == 0  → entry.Images must be empty (no JSON bleed, no charge).
//   - cap >  0  → entry.Images length must be ≤ cap. Truncation is
//                 deterministic from the front; the JSON page payload
//                 already arrives in Google's preferred order.
//   - len(entry.Images) ≤ cap is the invariant the database is allowed
//     to assume — every charge event downstream reads jsonb_array_length
//     on this field.

func TestApplyPerPlaceImageCap_ZeroDropsAll(t *testing.T) {
	t.Parallel()

	entry := &Entry{Images: []Image{
		{Title: "all", Image: "https://example.com/1.jpg"},
		{Title: "all", Image: "https://example.com/2.jpg"},
		{Title: "all", Image: "https://example.com/3.jpg"},
	}}

	applyPerPlaceImageCap(entry, 0)

	if len(entry.Images) != 0 {
		t.Errorf("cap=0 must clear images, got %d", len(entry.Images))
	}
}

func TestApplyPerPlaceImageCap_TruncatesToCap(t *testing.T) {
	t.Parallel()

	imgs := make([]Image, 30)
	for i := range imgs {
		imgs[i] = Image{Title: "all", Image: "https://example.com/x.jpg"}
	}
	entry := &Entry{Images: imgs}

	applyPerPlaceImageCap(entry, 10)

	if len(entry.Images) != 10 {
		t.Errorf("cap=10 with 30 images must truncate to 10, got %d", len(entry.Images))
	}
}

func TestApplyPerPlaceImageCap_UnderCapUnchanged(t *testing.T) {
	t.Parallel()

	imgs := []Image{
		{Title: "all", Image: "https://example.com/1.jpg"},
		{Title: "all", Image: "https://example.com/2.jpg"},
		{Title: "all", Image: "https://example.com/3.jpg"},
	}
	entry := &Entry{Images: imgs}

	applyPerPlaceImageCap(entry, 10)

	if len(entry.Images) != 3 {
		t.Errorf("3 images under cap=10 must stay 3, got %d", len(entry.Images))
	}
}

// remainingImageBudget returns how many more images the browser extractor
// is permitted to add. This is the second half of the contract: after the
// JSON-payload images have been truncated, the browser path must only top
// up to the cap, never exceed it.
func TestRemainingImageBudget_BelowCap(t *testing.T) {
	t.Parallel()

	got := remainingImageBudget(5, 10) // already have 5, cap is 10
	if got != 5 {
		t.Errorf("have=5 cap=10 → remaining=5, got %d", got)
	}
}

func TestRemainingImageBudget_AtCap(t *testing.T) {
	t.Parallel()

	got := remainingImageBudget(10, 10)
	if got != 0 {
		t.Errorf("have=10 cap=10 → remaining=0, got %d", got)
	}
}

func TestRemainingImageBudget_OverCap(t *testing.T) {
	t.Parallel()

	got := remainingImageBudget(15, 10) // shouldn't happen but be safe
	if got != 0 {
		t.Errorf("have=15 cap=10 → remaining=0, got %d", got)
	}
}

func TestRemainingImageBudget_CapZero(t *testing.T) {
	t.Parallel()

	got := remainingImageBudget(0, 0)
	if got != 0 {
		t.Errorf("cap=0 → remaining=0, got %d", got)
	}
}

// TestApplyPerPlaceImageCap_ReleasesBackingArray verifies that the helper
// CLONES rather than slice-truncates — the dropped tail (URL strings and
// all) becomes unreachable as soon as the entry's slice header is replaced,
// not when the entry itself is GC'd.
func TestApplyPerPlaceImageCap_ReleasesBackingArray(t *testing.T) {
	t.Parallel()

	orig := make([]Image, 80)
	for i := range orig {
		orig[i] = Image{Title: "all", Image: "https://example.com/x.jpg"}
	}
	entry := &Entry{Images: orig}

	applyPerPlaceImageCap(entry, 10)

	if len(entry.Images) != 10 {
		t.Fatalf("len = %d, want 10", len(entry.Images))
	}
	// Post-clone, cap(entry.Images) must equal len — proving the slice
	// no longer references the 80-element backing array.
	if c := cap(entry.Images); c != 10 {
		t.Errorf("cap(entry.Images) = %d, want 10 — slice still references the original 80-element backing array (memory leak)", c)
	}
}

// TestRemainingImageBudget_NamedLimitParameter is a compile-time guard:
// if a future refactor reintroduces `cap` as a parameter name, this test
// won't actually fail at runtime but the renamed-symbol nature of the
// fix is preserved here as documentation.
func TestRemainingImageBudget_NamedLimitParameter(t *testing.T) {
	t.Parallel()

	// Sanity: function still behaves correctly with the renamed `limit` param.
	if remainingImageBudget(3, 7) != 4 {
		t.Errorf("remainingImageBudget(3, 7) != 4 — rename regression?")
	}
}

// PlaceJob must carry the per-place cap forward — that's how the runner
// configures each place. The old ImageBudget *atomic.Int64 is replaced
// by a plain int because the cap is now per-place, not shared across
// the job.
func TestPlaceJob_ImagesPerPlaceFieldPropagates(t *testing.T) {
	t.Parallel()

	job := NewPlaceJob("parent", "en", "https://example.com/place", false, true, 0,
		WithPlaceJobImagesPerPlace(10),
	)

	if job.ImagesPerPlace != 10 {
		t.Errorf("PlaceJob.ImagesPerPlace = %d, want 10", job.ImagesPerPlace)
	}
}
