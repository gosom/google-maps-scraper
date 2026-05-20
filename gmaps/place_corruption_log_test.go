package gmaps

import (
	"testing"
)

// TestPlacePayloadInconsistencyCanary_FiresOnRatingWithoutCount documents
// the invariant: a Google Maps place with rating > 0 should never have
// review_count == 0. When the parsed Entry violates this, we emit a
// WARN-level canary so Grafana/Loki can alert on Google JSON shape changes.
func TestPlacePayloadInconsistencyCanary_FiresOnRatingWithoutCount(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-X", "user_TEST")
	pj := &PlaceJob{}
	pj.ID = "PLACE-JOB-X"
	pj.ParentID = "SEARCH-JOB-X"
	pj.URL = "https://www.google.com/maps/place/Test"
	pj.UserID = "user_TEST"
	pj.UserJobID = "USER-JOB-X"

	entry := Entry{Title: "Café Libre Berlin", ReviewRating: 4.8, ReviewCount: 0}
	checkPlacePayloadInvariants(ctx, pj, &entry)

	recs := decodeLogLines(t, buf)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r["msg"] != "place_payload_inconsistent_review_count" {
		t.Errorf("msg: got %v", r["msg"])
	}
	for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url", "place_name", "rating"} {
		if _, ok := r[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	if r["place_name"] != "Café Libre Berlin" {
		t.Errorf("place_name: got %v", r["place_name"])
	}
}

// TestPlacePayloadInconsistencyCanary_DoesNotFireOnLegitimateZeroReview
// guards against false positives on newly-listed places that legitimately
// have no reviews. rating==0 AND review_count==0 is consistent; no canary.
func TestPlacePayloadInconsistencyCanary_DoesNotFireOnLegitimateZeroReview(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-X", "user_TEST")
	pj := &PlaceJob{}
	pj.ID = "PLACE-JOB-X"
	pj.ParentID = "SEARCH-JOB-X"
	pj.URL = "https://www.google.com/maps/place/New"
	pj.UserID = "user_TEST"
	pj.UserJobID = "USER-JOB-X"

	entry := Entry{Title: "Brand new place", ReviewRating: 0, ReviewCount: 0}
	checkPlacePayloadInvariants(ctx, pj, &entry)

	recs := decodeLogLines(t, buf)
	if len(recs) != 0 {
		t.Fatalf("expected no canary on legitimate-zero-review place, got %d records: %v", len(recs), recs)
	}
}

// TestPlacePayloadInconsistencyCanary_DoesNotFireWhenBothPopulated is the
// happy-path: the entry is consistent and we stay silent.
func TestPlacePayloadInconsistencyCanary_DoesNotFireWhenBothPopulated(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-X", "user_TEST")
	pj := &PlaceJob{}
	pj.ID = "PLACE-JOB-X"
	pj.ParentID = "SEARCH-JOB-X"
	pj.URL = "https://www.google.com/maps/place/Healthy"
	pj.UserID = "user_TEST"
	pj.UserJobID = "USER-JOB-X"

	entry := Entry{Title: "Healthy", ReviewRating: 4.5, ReviewCount: 123}
	checkPlacePayloadInvariants(ctx, pj, &entry)

	recs := decodeLogLines(t, buf)
	if len(recs) != 0 {
		t.Fatalf("expected no canary on consistent entry, got %d records", len(recs))
	}
}
