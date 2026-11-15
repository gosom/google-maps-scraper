package gmaps

const reviewReasonLimited = "limited_view"

// ReviewCollection describes the outcome of an explicitly requested extra-review
// collection. Expected includes Google's ratings-only reviews as well as text reviews.
type ReviewCollection struct {
	Status    string `json:"status"`
	Method    string `json:"method"`
	Collected int    `json:"collected"`
	Expected  int    `json:"expected"`
	Reason    string `json:"reason,omitempty"`
}

type reviewExtractionState struct {
	capture *browserReviewCapture
	limited bool
	reason  string
}

func (e *Entry) finishReviewCollection(outcome ReviewCollection) {
	outcome.Expected = e.ReviewCount
	outcome.Collected = len(e.UserReviews) + len(e.UserReviewsExtended)
	outcome.Status = "partial"

	switch {
	case outcome.Collected >= outcome.Expected:
		outcome.Status = "complete"
		outcome.Reason = ""
	case outcome.Reason == reviewReasonLimited:
		outcome.Status = "limited"
	case outcome.Reason == "":
		outcome.Reason = "no_more_reviews"
	}

	e.ReviewCollection = &outcome
}

// appendUniqueReviews keeps first-seen ordering while upgrading duplicate IDs
// when a later page contains expanded text or richer structured metadata.
func appendUniqueReviews(existing, incoming []Review) []Review {
	byID := make(map[string]int, len(existing))
	for i := range existing {
		if existing[i].ReviewID != "" {
			byID[existing[i].ReviewID] = i
		}
	}

	for i := range incoming {
		review := &incoming[i]
		if index, exists := byID[review.ReviewID]; review.ReviewID != "" && exists {
			upgradeReview(&existing[index], review)

			continue
		}

		if review.ReviewID != "" {
			byID[review.ReviewID] = len(existing)
		}

		existing = append(existing, *review)
	}

	return existing
}

func enrichKnownReviews(existing, incoming []Review) {
	byID := make(map[string]int, len(existing))
	for i := range existing {
		if existing[i].ReviewID != "" {
			byID[existing[i].ReviewID] = i
		}
	}

	for i := range incoming {
		if index, exists := byID[incoming[i].ReviewID]; exists {
			upgradeReview(&existing[index], &incoming[i])
		}
	}
}

func upgradeReview(existing, incoming *Review) {
	previousText, previousImages := existing.Description, existing.Images

	previousRating := existing.Rating
	if existing.PostedAtUnixMicros == 0 && incoming.PostedAtUnixMicros != 0 {
		*existing = *incoming
	}

	if len(incoming.Description) > len(existing.Description) {
		existing.Description = incoming.Description
	}

	if len(previousText) > len(existing.Description) {
		existing.Description = previousText
	}

	if len(incoming.Images) > len(existing.Images) {
		existing.Images = incoming.Images
	}

	if len(previousImages) > len(existing.Images) {
		existing.Images = previousImages
	}

	if existing.Rating == 0 {
		existing.Rating = incoming.Rating
		if existing.Rating == 0 {
			existing.Rating = previousRating
		}
	}

	if existing.AuthorURL == "" {
		existing.AuthorURL = incoming.AuthorURL
	}
}

type reviewAuthorText struct{ author, text string }

type domReviewSet struct {
	byID   map[string]int
	byText map[reviewAuthorText]struct{}
}

func (s *domReviewSet) add(reviews, incoming []DOMReview, limit int) []DOMReview {
	if s.byID == nil {
		s.byID = make(map[string]int)
		s.byText = make(map[reviewAuthorText]struct{})
	}

	for i := range incoming {
		review := &incoming[i]
		if review.AuthorName == "" {
			continue
		}

		key := reviewAuthorText{review.AuthorName, review.Text}
		if review.ReviewID != "" {
			if index, exists := s.byID[review.ReviewID]; exists {
				if len(review.Text) > len(reviews[index].Text) {
					reviews[index] = *review
				}

				continue
			}
		} else if _, exists := s.byText[key]; exists {
			continue
		}

		if limit > 0 && len(reviews) >= limit {
			continue
		}

		if review.ReviewID != "" {
			s.byID[review.ReviewID] = len(reviews)
		}

		s.byText[key] = struct{}{}

		reviews = append(reviews, *review)
	}

	return reviews
}

func decodeReviewSnapshot(value any) ([]DOMReview, bool) {
	limited := false
	if result, ok := value.(map[string]any); ok {
		limited, _ = result["limited"].(bool)
		value = result["reviews"]
	}

	raw, _ := value.([]any)

	return decodeDOMReviews(raw), limited
}
