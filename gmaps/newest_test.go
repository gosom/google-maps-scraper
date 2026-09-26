//nolint:testpackage // tests the newest-only path's unexported internals
package gmaps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func dated(id, when string) DOMReview {
	return DOMReview{ReviewID: id, RelativeTimeDescription: when, Text: "text " + id}
}

func TestRelativeAgesAreReadInHours(t *testing.T) {
	for desc, want := range map[string]float64{
		"8 hours ago": 8, "an hour ago": 1, "a day ago": 24, "3 days ago": 72,
		"a week ago": 168, "2 months ago": 1440, "a year ago": 8760, "5 minutes ago": 5.0 / 60,
	} {
		got, ok := relativeAgeHours(desc)
		assert.True(t, ok, desc)
		assert.InDelta(t, want, got, 1e-9, desc)
	}

	for _, desc := range []string{"", "Edited 2 months ago", "vor 2 Tagen", "NEW"} {
		_, ok := relativeAgeHours(desc)
		assert.False(t, ok, desc)
	}
}

func TestNewestFirstIsJudgedByTheReviewsOwnDates(t *testing.T) {
	// What the Newest sort served on Green Park Dental, 27 Sep 2026.
	assert.True(t, newestOrdered([]DOMReview{dated("a", "8 hours ago"), dated("b", "12 hours ago"),
		dated("c", "a day ago"), dated("d", "a day ago"), dated("e", "a day ago")}))
	// Its "most relevant" order, which must never pass for the newest.
	assert.False(t, newestOrdered([]DOMReview{dated("a", "4 months ago"), dated("b", "5 months ago"),
		dated("c", "2 months ago"), dated("d", "3 months ago"), dated("e", "7 months ago")}))
	// An edited review is left out of the comparison, not counted against the order.
	assert.True(t, newestOrdered([]DOMReview{dated("a", "2 hours ago"), dated("b", "Edited a year ago"),
		dated("c", "3 days ago")}))
	// Dates it cannot read prove nothing.
	assert.False(t, newestOrdered([]DOMReview{dated("a", ""), dated("b", "")}))
	// A place with one review, or none.
	assert.True(t, newestOrdered([]DOMReview{dated("a", "")}))
	assert.True(t, newestOrdered(nil))
}

func TestANewestOnlyCollectionReadsTheSortedListAndIsNeverComplete(t *testing.T) {
	rpc := &scriptedFetcher{steps: map[string][]step{}}
	c, _, _ := collector(ReviewConfig{NewestOnly: true}, rpc, rpc, nil)
	c.newest = func(context.Context, int) ([]DOMReview, bool) {
		return []DOMReview{dated("n1", "8 hours ago"), dated("n2", "a day ago")}, true
	}

	res := c.run(context.Background(), 3109)
	report := settle(res, nil)

	assert.Equal(t, []string{"n1", "n2"}, ids(res.Rows))
	assert.Equal(t, stopNewest, report.StopReason)
	assert.False(t, report.Complete, "five newest of 3,109 is not the whole list")
	assert.Equal(t, []string{stageDOM}, report.Stages)
	assert.Equal(t, 2, report.DOMReviews)
	assert.Empty(t, rpc.asked, "the RPC stages are skipped")
}

func TestANewestOnlyCollectionWhoseSortDidNotTakeKeepsNothing(t *testing.T) {
	c, _, _ := collector(ReviewConfig{NewestOnly: true}, nil, nil, nil)
	c.newest = func(context.Context, int) ([]DOMReview, bool) {
		return []DOMReview{dated("old", "4 months ago")}, false
	}

	res := c.run(context.Background(), 3109)
	report := settle(res, nil)

	assert.Empty(t, res.Rows)
	assert.Equal(t, stopUnsorted, report.StopReason)
	assert.False(t, report.Complete)
}

func TestANewestOnlyCollectionWithoutAPageIsAnError(t *testing.T) {
	c, _, _ := collector(ReviewConfig{NewestOnly: true}, nil, nil, nil)

	report := settle(c.run(context.Background(), 10), nil)

	assert.Equal(t, stopError, report.StopReason)
}
