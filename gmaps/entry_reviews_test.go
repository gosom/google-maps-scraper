package gmaps

import "testing"

func TestParseReviewsExtractsReviewerProfileLink(t *testing.T) {
	t.Parallel()

	reviewsI := []any{
		[]any{
			[]any{
				"Ci9-test-review-id",
				[]any{
					nil, nil, nil, nil,
					[]any{
						nil, nil, nil, nil, nil,
						[]any{
							"Alice",
							"https://lh3.googleusercontent.com/a-/ALV-UjX",
							[]any{"/url?q=https://www.google.com/maps/contrib/118241168709410234298/reviews?hl=en&opi=79508299"},
						},
					},
				},
				[]any{
					[]any{float64(5)},
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					[]any{[]any{"Great coffee"}},
				},
				[]any{
					nil,
					nil,
					nil,
					"2 months ago",
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					nil,
					[]any{[]any{"Thanks for visiting!"}},
				},
				[]any{
					nil,
					nil,
					nil,
					[]any{"https://www.google.com/maps/reviews/data=!4m7!3m6"},
				},
			},
		},
	}

	got := parseReviews(reviewsI)
	if len(got) != 1 {
		t.Fatalf("parseReviews() len = %d, want 1", len(got))
	}

	review := got[0]
	if review.ProfileLink != "https://www.google.com/maps/contrib/118241168709410234298/reviews?hl=en" {
		t.Fatalf("ProfileLink = %q, want cleaned contrib URL", review.ProfileLink)
	}
	if review.ReviewId != "Ci9-test-review-id" {
		t.Fatalf("ReviewId = %q, want %q", review.ReviewId, "Ci9-test-review-id")
	}
	if review.OwnerResponse != "Thanks for visiting!" {
		t.Fatalf("OwnerResponse = %q, want %q", review.OwnerResponse, "Thanks for visiting!")
	}
}
