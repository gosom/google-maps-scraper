//nolint:testpackage // exercise review merging and anonymous access outcomes
package gmaps

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"
)

func TestReviewCollectionOutcomes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		expected int
		reason   string
		status   string
	}{
		{"complete", 1, "", "complete"},
		{"limited", 92, reviewReasonLimited, "limited"},
		{"stalled", 92, "no_more_reviews", "partial"},
		{"timeout", 92, "timeout", "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entry := Entry{ReviewCount: tc.expected, UserReviews: []Review{{ReviewID: "one"}}}
			entry.finishReviewCollection(ReviewCollection{Method: "browser", Reason: tc.reason})
			require.Equal(t, tc.status, entry.ReviewCollection.Status)
			require.Equal(t, 1, entry.ReviewCollection.Collected)
			require.Equal(t, tc.expected, entry.ReviewCollection.Expected)
		})
	}
}

func TestDOMReviewSetKeepsExpandedTextAcrossScrollWindows(t *testing.T) {
	t.Parallel()

	var seen domReviewSet

	reviews := seen.add(nil, []DOMReview{{ReviewID: "one", AuthorName: "A", Text: "Short"}}, 2)
	reviews = seen.add(reviews, []DOMReview{
		{ReviewID: "one", AuthorName: "A", Text: "Short review expanded to its complete text"},
		{ReviewID: "two", AuthorName: "B", Text: "Next window"},
	}, 2)
	require.Len(t, reviews, 2)
	require.Contains(t, reviews[0].Text, "complete text")
}

func TestReviewMergePreservesExpandedTextAndStructuredMetadata(t *testing.T) {
	t.Parallel()

	inline := []Review{{ReviewID: "one", Name: "A", Description: "A long expanded review description", Rating: 5}}
	network := []Review{{ReviewID: "one", Name: "A", Description: "A long", PostedAtUnixMicros: 123, ReplyText: "Thank you"}}
	enrichKnownReviews(inline, network)
	require.Equal(t, "A long expanded review description", inline[0].Description)
	require.Equal(t, int64(123), inline[0].PostedAtUnixMicros)
	require.Equal(t, "Thank you", inline[0].ReplyText)
	require.Equal(t, 5, inline[0].Rating)
	// A DOM snapshot may have an expanded text but no structured metadata.
	enrichKnownReviews(inline, []Review{{ReviewID: "one", Description: "A long expanded review description with the remaining text", Rating: 5}})
	require.Contains(t, inline[0].Description, "remaining text")
	require.Equal(t, int64(123), inline[0].PostedAtUnixMicros)
	require.Equal(t, "Thank you", inline[0].ReplyText)

	merged := appendUniqueReviews(network, []Review{network[0], {ReviewID: "two", Name: "B"}})
	require.Len(t, merged, 2)
}

func TestLimitedViewStopsWithoutPaginationRetries(t *testing.T) {
	t.Parallel()

	extractions := 0
	page := &mockBrowserPage{evalFn: func(js string, _ ...any) (any, error) {
		if strings.Contains(js, "reviewElements") {
			extractions++

			return map[string]any{
				"limited": true,
				"reviews": []any{map[string]any{"review_id": "one", "author_name": "A", "rating": 5}},
			}, nil
		}

		return true, nil
	}}
	response, reviews, err := FetchReviewsWithFallback(context.Background(), fetchReviewsParams{page: page, reviewCount: 92})
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Equal(t, reviewReasonLimited, response.collection.Reason)
	require.Equal(t, 1, extractions)
}

func TestPlaceProcessMergesNetworkAndDOMReviewsAndReportsLimitedAccess(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../testdata/raw.json")
	require.NoError(t, err)

	var placeData, payload []any
	require.NoError(t, json.Unmarshal(raw, &placeData))
	require.NoError(t, json.Unmarshal([]byte(reviewFixturePayload(t, true)), &payload))
	primary := getNthElementAndCast[[]any](payload, 2)
	getNthElementAndCast[[]any](primary, 0, 0)[0] = "inline-primary"
	block := make([]any, 10)
	block[9] = []any{[]any{primary}}
	getNthElementAndCast[[]any](placeData, 6)[175] = block
	raw, err = json.Marshal(placeData)
	require.NoError(t, err)

	base, err := EntryFromJSON(raw)
	require.NoError(t, err)
	require.NotEmpty(t, base.UserReviews)

	review := base.UserReviews[0]
	job := NewPlaceJob("input", "en", "https://www.google.com/maps/place/test", false, true)
	result, next, err := job.Process(context.Background(), &scrapemate.Response{Meta: map[string]any{
		"json":              raw,
		"reviews_raw":       FetchReviewsResponse{pages: [][]byte{[]byte(reviewFixturePayload(t, true))}},
		"dom_reviews":       []DOMReview{{ReviewID: review.ReviewID, AuthorName: review.Name, Rating: review.Rating}},
		"review_collection": ReviewCollection{Method: "browser", Reason: reviewReasonLimited},
	}})
	require.NoError(t, err)
	require.Empty(t, next)

	entry, ok := result.(*Entry)
	require.True(t, ok)
	require.Equal(t, "limited", entry.ReviewCollection.Status)
	require.Equal(t, len(base.UserReviews)+1, entry.ReviewCollection.Collected)
	require.Len(t, entry.UserReviewsExtended, 1)
	require.Equal(t, len(entry.CsvHeaders()), len(entry.CsvRow()))
	require.Equal(t, "review_collection", entry.CsvHeaders()[len(entry.CsvHeaders())-1])

	encoded, err := json.Marshal(entry)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"status":"limited"`)
}

func TestReviewMetadataOmittedWithoutExtraReviewRequest(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(Entry{})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "review_collection")
}
