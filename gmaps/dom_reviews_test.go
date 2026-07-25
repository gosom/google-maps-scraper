//nolint:testpackage // we need to test unexported functions in the same package
package gmaps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_decodeDOMReviews(t *testing.T) {
	const reviewID = "ChZDSUhNMG9nS0VJQ0FnSUR2c0tYWVh3EAE"

	tests := []struct {
		name string
		raw  map[string]any
		want DOMReview
	}{
		{"int rating", map[string]any{"rating": 5}, DOMReview{Rating: 5}},
		{"float rating", map[string]any{"rating": 4.0}, DOMReview{Rating: 4}},
		{"rating above scale", map[string]any{"rating": 12}, DOMReview{}},
		{"negative rating", map[string]any{"rating": -3}, DOMReview{}},
		{"string rating", map[string]any{"rating": "5"}, DOMReview{}},
		{"bool rating", map[string]any{"rating": true}, DOMReview{}},
		{"nil rating", map[string]any{"rating": nil}, DOMReview{}},
		{"missing rating", map[string]any{}, DOMReview{}},
		{"review id", map[string]any{"review_id": reviewID}, DOMReview{ReviewID: reviewID}},
		{"empty review id", map[string]any{"review_id": ""}, DOMReview{}},
		{"missing review id", map[string]any{"rating": 3}, DOMReview{Rating: 3}},
		{
			"author and relative time",
			map[string]any{"author_name": "Reviewer A", "relative_time_description": "a year ago"},
			DOMReview{AuthorName: "Reviewer A", RelativeTimeDescription: "a year ago"},
		},
	}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got := decodeDOMReviews([]any{tt.raw})
			require.Len(t, got, 1)
			assert.Equal(t, tt.want, got[0])
		})
	}
}

func Test_ConvertDOMReviewsToReviews(t *testing.T) {
	const reviewID = "ChZDSUhNMG9nS0VJQ0FnSUR2c0tYWVh3EAE"

	got := ConvertDOMReviewsToReviews([]DOMReview{
		{
			ReviewID:                reviewID,
			AuthorName:              "Reviewer A",
			Rating:                  5,
			RelativeTimeDescription: "3 months ago",
		},
		{Rating: 4},
	})

	require.Len(t, got, 1)
	assert.Equal(t, reviewID, got[0].ReviewID)
	assert.Equal(t, 5, got[0].Rating)
	assert.Equal(t, "Reviewer A", got[0].Name)
	assert.Equal(t, "3 months ago", got[0].When)
}

func Test_dedupeDOMReviewsAgainstPrimary(t *testing.T) {
	tests := []struct {
		name    string
		primary []Review
		dom     []Review
		want    []Review
	}{
		{
			"matching id dropped",
			[]Review{{ReviewID: "id-1"}, {ReviewID: "id-2"}},
			[]Review{{ReviewID: "id-1"}, {ReviewID: "id-3"}},
			[]Review{{ReviewID: "id-3"}},
		},
		{
			"unmatched ids kept",
			[]Review{{ReviewID: "id-1"}},
			[]Review{{ReviewID: "id-2"}, {ReviewID: "id-3"}},
			[]Review{{ReviewID: "id-2"}, {ReviewID: "id-3"}},
		},
		{
			"empty ids never match",
			[]Review{{ReviewID: ""}, {ReviewID: "id-1"}},
			[]Review{{ReviewID: ""}, {ReviewID: ""}},
			[]Review{{ReviewID: ""}, {ReviewID: ""}},
		},
	}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, dedupeDOMReviewsAgainstPrimary(tt.primary, tt.dom))
		})
	}
}
