package gmaps

import "testing"

func TestMergeUniqueReviews_DedupesByReviewID(t *testing.T) {
	t.Parallel()

	existing := []Review{
		{ReviewId: "Ci9-1", Name: "Alice", Description: "Good"},
	}
	incoming := []Review{
		{ReviewId: "Ci9-1", Name: "Alice", Description: "Good"},
		{ReviewId: "Ci9-2", Name: "Bob", Description: "Great"},
	}

	merged := mergeUniqueReviews(existing, incoming, 0)
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2", len(merged))
	}
}

func TestMergeUniqueReviews_DedupesByFallbackKey(t *testing.T) {
	t.Parallel()

	existing := []Review{
		{Name: "Alice", When: "2 months ago", Description: "Good"},
	}
	incoming := []Review{
		{Name: "Alice", When: "2 months ago", Description: "Good"},
		{Name: "Alice", When: "3 months ago", Description: "Good"},
	}

	merged := mergeUniqueReviews(existing, incoming, 0)
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2", len(merged))
	}
}

func TestMergeUniqueReviews_RespectsMaxReviews(t *testing.T) {
	t.Parallel()

	incoming := []Review{
		{ReviewId: "r1", Name: "A"},
		{ReviewId: "r2", Name: "B"},
		{ReviewId: "r3", Name: "C"},
	}

	merged := mergeUniqueReviews(nil, incoming, 2)
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2", len(merged))
	}
}
