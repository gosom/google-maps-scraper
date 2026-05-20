package gmaps

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/scrapemate"
)

// TestExtractJSONPartialAccepted_LogContext verifies that when extractJSON
// gives up and falls back to a partial payload, the warning carries:
//   - job_id        (user-facing, from ctx .With)
//   - user_id       (from ctx .With)
//   - place_job_id  (PlaceJob's internal UUID — renamed from job_id)
//   - search_job_id (GmapJob's internal UUID — renamed from parent_job_id)
//   - place_url
//   - msg == "extract_json_partial_payload_accepted"
func TestExtractJSONPartialAccepted_LogContext(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-1", "user_TEST")

	pj := &PlaceJob{}
	pj.ID = "PLACE-JOB-1"
	pj.ParentID = "SEARCH-JOB-1"
	pj.URL = "https://www.google.com/maps/place/Test"
	pj.UserID = "user_TEST"
	pj.UserJobID = "USER-JOB-1"

	emitPartialPayloadAcceptedWarning(ctx, pj, 18867)

	recs := decodeLogLines(t, buf)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r["msg"] != "extract_json_partial_payload_accepted" {
		t.Errorf("msg: got %v", r["msg"])
	}
	for _, want := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url", "bytes"} {
		if _, ok := r[want]; !ok {
			t.Errorf("missing field %q in log record", want)
		}
	}
	if !strings.Contains(r["place_url"].(string), "/maps/place/Test") {
		t.Errorf("place_url not propagated: %v", r["place_url"])
	}
}

func TestJSONExtractionFallback_LogContext(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-Y", "user_TEST")
	pj := &PlaceJob{}
	pj.ID = "PLACE-JOB-Y"
	pj.ParentID = "SEARCH-JOB-Y"
	pj.URL = "https://www.google.com/maps/place/Broken"
	pj.UserID = "user_TEST"
	pj.UserJobID = "USER-JOB-Y"

	emitJSONExtractionFallback(ctx, pj)
	emitJSONParsingFallback(ctx, pj, errors.New("invalid json"))

	recs := decodeLogLines(t, buf)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	for i, want := range []string{"json_extraction_fallback", "json_parsing_fallback"} {
		r := recs[i]
		if r["msg"] != want {
			t.Errorf("rec %d msg: got %v want %v", i, r["msg"], want)
		}
		for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url"} {
			if _, ok := r[k]; !ok {
				t.Errorf("rec %d missing %q", i, k)
			}
		}
	}
	if recs[1]["error"] != "invalid json" {
		t.Errorf("error field missing or wrong: %v", recs[1]["error"])
	}
}

func TestReviewsGenerateURLFailed_LogContext(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-R", "user_TEST")

	// generateURL needs a mapURL that fails the placeIDRegex; an URL
	// without "!1s<id>" segment qualifies.
	params := fetchReviewsParams{
		mapURL:      "https://www.google.com/maps/place/Test/data=garbage",
		reviewCount: 100, // > 8 to trigger an attempt
		maxReviews:  50,
		langCode:    "en",
		// new fields added by this task:
		placeJobID:  "PLACE-JOB-R",
		searchJobID: "SEARCH-JOB-R",
		placeName:   "Test",
		userID:      "user_TEST",
		userJobID:   "USER-JOB-R",
	}
	// page is nil — fetch must not deref it before the URL error path
	f, ferr := newReviewFetcher(params)
	if ferr != nil {
		t.Fatalf("newReviewFetcher: %v", ferr)
	}
	_, err := f.fetch(ctx)
	if err == nil {
		t.Fatal("expected fetch to fail on unparseable URL")
	}

	// At least one log line should have appeared (reviews_generate_url_failed
	// or the initial "failed to fetch initial review page" wrapper).
	// Either way, the line must carry place context.
	recs := decodeLogLines(t, buf)
	if len(recs) == 0 {
		t.Fatal("expected at least one log record from reviews.go")
	}
	for i, r := range recs {
		for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url", "place_name"} {
			if _, ok := r[k]; !ok {
				t.Errorf("rec %d (%v): missing %q", i, r["msg"], k)
			}
		}
	}
}

func TestReviewPageParseFailed_LogContext(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-P", "user_TEST")
	pj := &PlaceJob{}
	pj.ID = "PLACE-JOB-P"
	pj.ParentID = "SEARCH-JOB-P"
	pj.URL = "https://www.google.com/maps/place/ParseFail"
	pj.UserID = "user_TEST"
	pj.UserJobID = "USER-JOB-P"

	entry := Entry{Title: "ParseFail Place"}

	// Two garbage pages: extractReviews will fail to unmarshal both.
	pages := [][]byte{
		[]byte("not-json"),
		[]byte("also-not-json"),
	}
	entry.AddExtraReviews(ctx, pj, pages)

	recs := decodeLogLines(t, buf)
	if len(recs) != 2 {
		t.Fatalf("want 2 log records (one per failed page), got %d", len(recs))
	}
	for i, r := range recs {
		if r["msg"] != "review_page_parse_failed" {
			t.Errorf("rec %d msg: got %v", i, r["msg"])
		}
		for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url", "place_name", "page", "total_pages"} {
			if _, ok := r[k]; !ok {
				t.Errorf("rec %d missing %q", i, k)
			}
		}
		if r["place_name"] != "ParseFail Place" {
			t.Errorf("rec %d place_name: got %v", i, r["place_name"])
		}
		if r["total_pages"].(float64) != 2 {
			t.Errorf("rec %d total_pages: got %v", i, r["total_pages"])
		}
	}
}

func TestReviewExtractionLogs_AllCarryUserAndSearchContext(t *testing.T) {
	cases := []struct {
		name string
		emit func(ctx context.Context, j *PlaceJob)
		want string // expected "msg" field
	}{
		{
			name: "circuit_breaker_open",
			emit: emitReviewCircuitBreakerOpen,
			want: "review_circuit_breaker_open",
		},
		{
			name: "extraction_failed",
			emit: func(ctx context.Context, j *PlaceJob) {
				emitReviewExtractionFailed(ctx, j, errors.New("simulated failure"))
			},
			want: "review_extraction_failed",
		},
		{
			name: "api_empty_response",
			emit: func(ctx context.Context, j *PlaceJob) {
				emitReviewAPIEmptyResponse(ctx, j, 271, 33, 1, []byte(")]}'\n[null,null,null,null,null,1]"))
			},
			want: "review_api_empty_response",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, buf := newCaptureLogger(t, "USER-JOB-1", "user_TEST")
			pj := &PlaceJob{}
			pj.ID = "PLACE-JOB-1"
			pj.ParentID = "SEARCH-JOB-1"
			pj.URL = "https://www.google.com/maps/place/Test"
			pj.UserID = "user_TEST"
			pj.UserJobID = "USER-JOB-1"

			tc.emit(ctx, pj)

			recs := decodeLogLines(t, buf)
			if len(recs) != 1 {
				t.Fatalf("want 1 record, got %d", len(recs))
			}
			r := recs[0]
			if r["msg"] != tc.want {
				t.Errorf("msg: got %v want %v", r["msg"], tc.want)
			}
			for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url"} {
				if _, ok := r[k]; !ok {
					t.Errorf("missing %q in %s", k, tc.want)
				}
			}
		})
	}
}

// TestEmitHelpersOmitEmptyUserContext verifies the conditional-emit
// contract: when a PlaceJob has empty UserID and UserJobID (the CLI /
// standalone case), the emit helper does NOT contribute its own
// empty-string args. This prevents user_id="" from polluting per-user
// Grafana alert buckets when CLI traffic spikes.
//
// Uses a bare capture logger (no .With attrs) so the only path that could
// inject "user_id" / "job_id" is the helper itself.
func TestEmitHelpersOmitEmptyUserContext(t *testing.T) {
	buf := &bytes.Buffer{}
	base := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctxBare := scrapemate.ContextWithLogger(context.Background(), logger.NewSlogAdapter(base))

	// Simulate CLI run: UserID and UserJobID NOT set on PlaceJob.
	pj := &PlaceJob{}
	pj.ID = "PLACE-JOB-CLI"
	pj.ParentID = "SEARCH-JOB-CLI"
	pj.URL = "https://www.google.com/maps/place/CLI"

	emitReviewExtractionFailed(ctxBare, pj, errors.New("simulated"))

	recs := decodeLogLines(t, buf)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if _, ok := r["user_id"]; ok {
		t.Errorf("user_id should be omitted when PlaceJob.UserID is empty, got %v", r["user_id"])
	}
	if _, ok := r["job_id"]; ok {
		t.Errorf("job_id should be omitted when PlaceJob.UserJobID is empty, got %v", r["job_id"])
	}
	// Other fields should still be present.
	for _, k := range []string{"place_job_id", "search_job_id", "place_url", "error"} {
		if _, ok := r[k]; !ok {
			t.Errorf("missing required field %q in CLI-mode emit", k)
		}
	}
}
