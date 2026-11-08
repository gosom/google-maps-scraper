//nolint:testpackage // we need to test unexported functions in the same package
package gmaps

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gosom/scrapemate"
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

type mockBrowserPage struct {
	evalFn            func(js string, args ...any) (any, error)
	waitForSelectorFn func(selector string, timeout time.Duration) error
}

func (m *mockBrowserPage) Goto(url string, _ scrapemate.WaitUntilState) (*scrapemate.PageResponse, error) {
	return &scrapemate.PageResponse{URL: url, StatusCode: 200}, nil
}

func (m *mockBrowserPage) URL() string {
	return "https://www.google.com/maps/place/test"
}

func (m *mockBrowserPage) Content() (string, error) {
	return "<html></html>", nil
}

func (m *mockBrowserPage) Reload(_ scrapemate.WaitUntilState) error {
	return nil
}

func (m *mockBrowserPage) Screenshot(_ bool) ([]byte, error) {
	return nil, nil
}

func (m *mockBrowserPage) Eval(js string, args ...any) (any, error) {
	if m.evalFn != nil {
		return m.evalFn(js, args...)
	}

	return nil, nil
}

func (m *mockBrowserPage) WaitForURL(_ string, _ time.Duration) error {
	return nil
}

func (m *mockBrowserPage) WaitForSelector(selector string, timeout time.Duration) error {
	if m.waitForSelectorFn != nil {
		return m.waitForSelectorFn(selector, timeout)
	}

	return nil
}

func (m *mockBrowserPage) WaitForTimeout(_ time.Duration) {}

func (m *mockBrowserPage) Locator(_ string) scrapemate.Locator {
	return nil
}

func (m *mockBrowserPage) Close() error {
	return nil
}

func (m *mockBrowserPage) Unwrap() any {
	return nil
}

func Test_extractReviewsFromPage_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mock := &mockBrowserPage{}
	reviews, err := extractReviewsFromPage(ctx, mock, 10)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, reviews)
}

func Test_extractReviewsFromPage_TargetLimit(t *testing.T) {
	callCount := 0
	mock := &mockBrowserPage{
		evalFn: func(js string, _ ...any) (any, error) {
			if strings.Contains(js, "reviewElements") {
				callCount++
				// Return 5 unique reviews per call
				res := make([]any, 0, 5)
				for i := 0; i < 5; i++ {
					idx := (callCount-1)*5 + i
					res = append(res, map[string]any{
						"review_id":   fmt.Sprintf("review-%d", idx),
						"author_name": fmt.Sprintf("Author %d", idx),
						"rating":      5,
						"text":        "Great place!",
					})
				}

				return res, nil
			}

			return true, nil
		},
	}

	ctx := context.Background()
	// Request 12 reviews, which should stop once 15 reviews (3 batches of 5) are extracted >= 12
	reviews, err := extractReviewsFromPage(ctx, mock, 12)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(reviews), 12)
	assert.Equal(t, 15, len(reviews))
}

func Test_extractReviewsFromPage_Deduplication(t *testing.T) {
	evalPass := 0
	mock := &mockBrowserPage{
		evalFn: func(js string, _ ...any) (any, error) {
			if strings.Contains(js, "reviewElements") {
				evalPass++
				// First pass: 3 reviews
				if evalPass == 1 {
					return []any{
						map[string]any{"review_id": "r1", "author_name": "A", "rating": 5, "text": "Good"},
						map[string]any{"review_id": "r2", "author_name": "B", "rating": 4, "text": "Nice"},
						map[string]any{"review_id": "", "author_name": "C", "rating": 5, "text": "Same text"},
					}, nil
				}
				// Second pass: r1 and r2 are duplicates, r3 is new, C is duplicate text
				return []any{
					map[string]any{"review_id": "r1", "author_name": "A", "rating": 5, "text": "Good"},
					map[string]any{"review_id": "r2", "author_name": "B", "rating": 4, "text": "Nice"},
					map[string]any{"review_id": "r3", "author_name": "D", "rating": 3, "text": "Okay"},
					map[string]any{"review_id": "", "author_name": "C", "rating": 5, "text": "Same text"},
				}, nil
			}

			return true, nil
		},
	}

	ctx := context.Background()
	reviews, err := extractReviewsFromPage(ctx, mock, 4)
	require.NoError(t, err)
	// Should have exactly 4 unique reviews: r1, r2, C (no id), r3
	assert.Len(t, reviews, 4)
	assert.Equal(t, "r1", reviews[0].ReviewID)
	assert.Equal(t, "r2", reviews[1].ReviewID)
	assert.Equal(t, "", reviews[2].ReviewID)
	assert.Equal(t, "C", reviews[2].AuthorName)
	assert.Equal(t, "r3", reviews[3].ReviewID)
}

func Test_extractReviewsFromPage_StuckDetection(t *testing.T) {
	mock := &mockBrowserPage{
		evalFn: func(js string, _ ...any) (any, error) {
			if strings.Contains(js, "reviewElements") {
				// Always return the exact same 2 reviews
				return []any{
					map[string]any{"review_id": "r1", "author_name": "A", "rating": 5, "text": "Good"},
					map[string]any{"review_id": "r2", "author_name": "B", "rating": 4, "text": "Nice"},
				}, nil
			}

			return true, nil
		},
	}

	ctx := context.Background()
	// Ask for 50 reviews, but feed never yields more than 2
	reviews, err := extractReviewsFromPage(ctx, mock, 50)
	require.NoError(t, err)
	// Must exit gracefully after stuck threshold without infinite loop
	assert.Len(t, reviews, 2)
}

func Test_FetchReviewsWithFallback_DOMFallbackOnRPCFailure(t *testing.T) {
	mock := &mockBrowserPage{
		evalFn: func(js string, _ ...any) (any, error) {
			if strings.Contains(js, "reviewElements") {
				return []any{
					map[string]any{"review_id": "r1", "author_name": "Reviewer 1", "rating": 5, "text": "Loved it"},
				}, nil
			}
			// When fetchWithBrowser tries to call fetch(), it returns error HTTP 403
			if strings.Contains(js, "credentials") {
				return map[string]any{"error": "HTTP 403"}, nil
			}

			return true, nil
		},
	}

	ctx := context.Background()
	params := fetchReviewsParams{
		page:        mock,
		mapURL:      "https://www.google.com/maps/place/test/data=!4m7!3m6!1s0x123:0x456!8m2!3d40.1!4d-73.1",
		reviewCount: 1,
	}

	rpcResp, domReviews, err := FetchReviewsWithFallback(ctx, params)
	require.NoError(t, err)
	assert.Empty(t, rpcResp.pages)
	require.Len(t, domReviews, 1)
	assert.Equal(t, "r1", domReviews[0].ReviewID)
	assert.Equal(t, "Reviewer 1", domReviews[0].AuthorName)
}
