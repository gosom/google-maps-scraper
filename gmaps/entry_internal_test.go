package gmaps

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExtractPlaceStatus(t *testing.T) {
	t.Run("business closed badge", func(t *testing.T) {
		darray := make([]any, 89)
		darray[88] = []any{"CLOSED", "SearchResult.TYPE_COFFEE", nil, "Song Coffee", nil}
		darray[34] = []any{nil, nil, nil, nil, []any{nil, nil, nil, nil, "Closed ⋅ Opens 8 am Mon"}}

		require.Equal(t, "closed", extractPlaceStatus(darray))
	})

	t.Run("open hours summary when no closure badge", func(t *testing.T) {
		darray := make([]any, 89)
		darray[88] = []any{nil, "SearchResult.TYPE_RESTAURANT", nil, "Kipriakon", nil}
		darray[34] = []any{nil, nil, nil, nil, []any{nil, nil, nil, nil, "Closed ⋅ Opens 12:30 pm Tue"}}

		require.Equal(t, "Closed ⋅ Opens 12:30 pm Tue", extractPlaceStatus(darray))
	})

	t.Run("localized closure badge", func(t *testing.T) {
		darray := make([]any, 89)
		darray[88] = []any{"ĐANG ĐÓNG CỬA", "SearchResult.TYPE_COFFEE", nil, "Song Coffee", nil}

		require.Equal(t, "closed", extractPlaceStatus(darray))
	})
}

func TestParseReviewsRelativeDatePaths(t *testing.T) {
	tests := []struct {
		name    string
		wrapped bool
		path    []int
		when    string
	}{
		{
			name:    "inline review metadata path",
			wrapped: true,
			path:    []int{1, 6},
			when:    "7 months ago",
		},
		{
			name: "direct review fallback path",
			path: []int{3, 3},
			when: "3 weeks ago",
		},
		{
			name: "nested review rpc path",
			path: []int{2, 1, 3, 8, 0},
			when: "4 years ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			review := newReviewElement()
			setNested(review, tt.when, tt.path...)

			item := any(review)
			if tt.wrapped {
				item = []any{review}
			}

			reviews := parseReviews([]any{item})

			require.Len(t, reviews, 1)
			require.Equal(t, tt.when, reviews[0].When)
			require.Equal(t, "Ada Lovelace", reviews[0].Name)
			require.Equal(t, 5, reviews[0].Rating)
			require.Equal(t, "Clear review text", reviews[0].Description)
		})
	}
}

func TestParseReviewsPublishedAtFromMicrosecondTimestamp(t *testing.T) {
	review := newReviewElement()
	setNested(review, "6 months ago", 1, 6)
	setNested(review, 1764184138462529.0, 1, 2)

	reviews := parseReviews([]any{review})

	require.Len(t, reviews, 1)
	require.NotNil(t, reviews[0].PublishedAt)
	require.Equal(t, "2025-11-26T19:08:58.462529Z", reviews[0].PublishedAt.Format(time.RFC3339Nano))
}

func TestParseReviewsPublishedAtFallsBackToSecondTimestampPath(t *testing.T) {
	review := newReviewElement()
	setNested(review, "6 months ago", 1, 6)
	setNested(review, 1764184138462529.0, 1, 3)

	reviews := parseReviews([]any{review})

	require.Len(t, reviews, 1)
	require.NotNil(t, reviews[0].PublishedAt)
	require.Equal(t, "2025-11-26T19:08:58.462529Z", reviews[0].PublishedAt.Format(time.RFC3339Nano))
}

func TestParseReviewsPublishedAtRejectsInvalidTimestamps(t *testing.T) {
	tests := []struct {
		name   string
		micros float64
	}{
		{
			name: "missing",
		},
		{
			name:   "before lower bound",
			micros: float64(time.Date(2006, time.December, 31, 23, 59, 59, 0, time.UTC).UnixMicro()),
		},
		{
			name:   "too far in future",
			micros: float64(time.Now().UTC().Add(48 * time.Hour).UnixMicro()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			review := newReviewElement()
			setNested(review, "6 months ago", 1, 6)

			if tt.micros != 0 {
				setNested(review, tt.micros, 1, 2)
			}

			reviews := parseReviews([]any{review})

			require.Len(t, reviews, 1)
			require.Nil(t, reviews[0].PublishedAt)
		})
	}
}

func TestParseReviewsSkipsItemsWithoutAuthor(t *testing.T) {
	review := make([]any, 4)
	setNested(review, "yesterday", 1, 6)
	setNested(review, 5.0, 2, 0, 0)

	reviews := parseReviews([]any{review})

	require.Empty(t, reviews)
}

func newReviewElement() []any {
	review := make([]any, 4)

	setNested(review, "Ada Lovelace", 1, 4, 5, 0)
	setNested(review, 5.0, 2, 0, 0)
	setNested(review, "Clear review text", 2, 15, 0, 0)

	return review
}

func setNested(root []any, value any, path ...int) {
	setNestedValue(root, value, path...)
}

func setNestedValue(current []any, value any, path ...int) []any {
	if len(path) == 0 {
		return current
	}

	index := path[0]
	current = ensureLen(current, index+1)

	if len(path) == 1 {
		current[index] = value

		return current
	}

	next, ok := current[index].([]any)
	if !ok {
		next = make([]any, path[1]+1)
	}

	current[index] = setNestedValue(next, value, path[1:]...)

	return current
}

func ensureLen(items []any, length int) []any {
	if len(items) >= length {
		return items
	}

	extended := make([]any, length)
	copy(extended, items)

	return extended
}
