// Tests the isCompletePlacePayload classifier — the structural signal
// extractJSON uses to detect the "search-preview payload returned instead
// of place-detail payload" race in window.APP_INITIALIZATION_STATE.
//
// Verified empirically: across 8 captured raw dumps, payloads with
// len(jd[6][4]) >= 9 had ReviewCount > 0; payloads with len(jd[6][4]) == 8
// had ReviewCount == 0 while rating was populated. Sizes range 18 KB
// (partial) to 200 KB (full).
package gmaps

import (
	"encoding/json"
	"testing"
)

func TestIsCompletePlacePayload(t *testing.T) {
	// Partial preview payload: jd[6][4] has length 8 (rating at [7]; no [8]).
	partial := mustMarshal(t, partialPayload(4.8))
	if isCompletePlacePayload(partial) {
		t.Errorf("partial preview misclassified as complete")
	}

	// Full place-detail payload: jd[6][4] has length 11 (rating + count + more).
	full := mustMarshal(t, fullPayload(4.8, 267))
	if !isCompletePlacePayload(full) {
		t.Errorf("full place-detail misclassified as partial")
	}

	// Defensive cases — none should classify as complete.
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not-json", "garbage"},
		{"too-short-top-level", "[1,2,3]"},
		{"jd6-not-array", `[null,null,null,null,null,null,"string-at-6"]`},
		{"jd6-too-short", `[null,null,null,null,null,null,[null,null]]`},
		{"jd6-4-not-array", `[null,null,null,null,null,null,[null,null,null,null,"not-array"]]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isCompletePlacePayload([]byte(tc.raw)) {
				t.Errorf("%s misclassified as complete", tc.name)
			}
		})
	}
}

// TestPartialPayloadProducesZeroReviewCount documents the corruption
// pattern: when extractJSON accepts a partial payload, EntryFromJSON
// extracts rating but leaves ReviewCount at 0. This is the invariant Fix
// A is designed to prevent — by refusing to accept partial payloads in
// the first place.
func TestPartialPayloadProducesZeroReviewCount(t *testing.T) {
	partial := mustMarshal(t, partialPayload(4.8))

	entry, err := EntryFromJSON(partial)
	if err != nil {
		t.Fatalf("EntryFromJSON on partial payload: %v", err)
	}

	if entry.ReviewRating != 4.8 {
		t.Errorf("rating: want 4.8, got %v", entry.ReviewRating)
	}
	if entry.ReviewCount != 0 {
		t.Errorf("review_count: want 0 (proving the corruption), got %d", entry.ReviewCount)
	}
}

// partialPayload mimics the structure of a Google Maps search-preview
// payload at jd[6]: 260 slots, with [4] = length-8 rating cluster (no
// review count slot).
func partialPayload(rating float64) []any {
	d6 := make([]any, 260)
	d6[4] = []any{nil, nil, nil, nil, nil, nil, nil, rating} // len=8
	jd := make([]any, 44)
	jd[6] = d6
	return jd
}

// fullPayload mimics a Google Maps place-detail payload at jd[6] with
// [4] = length-11 rating cluster (count at [8]).
func fullPayload(rating float64, reviewCount int) []any {
	d6 := make([]any, 260)
	d6[4] = []any{nil, nil, nil, nil, nil, nil, nil, rating, float64(reviewCount), nil, nil} // len=11
	jd := make([]any, 32)
	jd[6] = d6
	return jd
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}
