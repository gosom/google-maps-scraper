package gmaps

import "testing"

func TestParseReviewCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "aria label", in: "More reviews (366)", want: 366},
		{name: "plain reviews", in: "1,234 reviews", want: 1234},
		{name: "german paren", in: "Rezensionen (2.345)", want: 2345},
		{name: "no count", in: "More details", want: 0},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseReviewCount(tt.in)
			if got != tt.want {
				t.Fatalf("parseReviewCount(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestConvertDOMReviewsToReviews(t *testing.T) {
	t.Parallel()

	got := convertDOMReviewsToReviews([]domReview{
		{
			ID:                "Ci9abc",
			Author:            "Alice",
			ProfileLink:       "https://www.google.com/maps/contrib/123/reviews?hl=en",
			ProfilePicture:    "https://example.com/profile.jpg",
			Rating:            5,
			Time:              "2 months ago",
			Text:              "Great coffee",
			Images:            []string{"https://example.com/1.jpg", "https://example.com/2.jpg"},
			ReviewURL:         "https://maps.google.com/review/abc",
			OwnerResponse:     "Thanks!",
			OwnerResponseTime: "1 month ago",
		},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 converted review, got %d", len(got))
	}

	review := got[0]
	if review.ReviewId != "Ci9abc" {
		t.Fatalf("ReviewId = %q, want %q", review.ReviewId, "Ci9abc")
	}
	if review.ProfileLink != "https://www.google.com/maps/contrib/123/reviews?hl=en" {
		t.Fatalf("ProfileLink = %q, want %q", review.ProfileLink, "https://www.google.com/maps/contrib/123/reviews?hl=en")
	}
	if review.OwnerResponse != "Thanks!" {
		t.Fatalf("OwnerResponse = %q, want %q", review.OwnerResponse, "Thanks!")
	}
	if review.OwnerResponseTime != "1 month ago" {
		t.Fatalf("OwnerResponseTime = %q, want %q", review.OwnerResponseTime, "1 month ago")
	}
	if review.ReviewUrl != "https://maps.google.com/review/abc" {
		t.Fatalf("ReviewUrl = %q, want %q", review.ReviewUrl, "https://maps.google.com/review/abc")
	}
}
