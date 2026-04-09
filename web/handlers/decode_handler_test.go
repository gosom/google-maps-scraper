package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// TestScrape_RejectsUnknownFields is the integration counterpart of the
// decodeStrict unit tests in decode_test.go — it exercises the full HTTP
// path from the Scrape handler down through decodeStrict and asserts the
// 422 response. The body adds an `admin_override` field that doesn't
// exist on apiScrapeRequest; before Task 3.1 the silent-drop behavior of
// json.NewDecoder would have accepted it, masking confusion-attack
// payloads.
func TestScrape_RejectsUnknownFields(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	body := `{"name":"test","keywords":["pizza"],"lang":"en","depth":5,"max_results":10,"max_time":60,"admin_override":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.Scrape(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown field, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	// Defense in depth: the response message MUST be generic. Echoing
	// the wrapped json error would expose the user-supplied field name
	// `admin_override` and create an XSS / log-injection vector for
	// malicious field names like `<script>...</script>`.
	if strings.Contains(rr.Body.String(), "admin_override") {
		t.Errorf("response leaked attacker-controlled field name: %s", rr.Body.String())
	}
}

// TestScrape_RejectsTrailingData covers the d.More() arm of decodeStrict
// at the handler level. Two concatenated documents must be rejected with
// 422 — without this, a parser-divergence attacker can sneak the second
// document through, relying on the server reading the first one only.
func TestScrape_RejectsTrailingData(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	body := `{"name":"a","keywords":["x"],"lang":"en","depth":5,"max_results":10,"max_time":60}{"name":"b","keywords":["y"],"lang":"en","depth":5,"max_results":10,"max_time":60}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.Scrape(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for trailing data, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// ───────────────────── Task 3.2: sort allowlist + search cap ─────────────

// TestGetUserJobs_RejectsInvalidSort verifies that the GetUserJobs
// allowlist rejects unknown sort values with 400. This blocks an
// attacker from sniffing column names by passing `?sort=password` and
// observing whether the request succeeds.
func TestGetUserJobs_RejectsInvalidSort(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/user?sort=password", nil)
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetUserJobs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid sort, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	// Defense in depth: the response MUST NOT echo the attacker's
	// supplied value. Echoing `password` would leak that we considered
	// it as a possible column at all.
	if strings.Contains(rr.Body.String(), "password") {
		t.Errorf("response leaked attacker-controlled sort value: %s", rr.Body.String())
	}
}

// TestGetUserJobs_AcceptsKnownSorts walks every entry in the allowlist
// to make sure the explicit set passes validation. The mockJobService
// returns an empty page so the handler reaches the 200 OK path; what
// we're really asserting is "no 400 from the sort allowlist for any
// known column."
func TestGetUserJobs_AcceptsKnownSorts(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{App: &mockJobService{}, Auth: &auth.AuthMiddleware{}}}
	for _, sortValue := range []string{"created_at", "name", "status", "updated_at"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/user?sort="+sortValue, nil)
		req = withUserID(req, "user-1")
		rr := httptest.NewRecorder()
		h.GetUserJobs(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("sort=%q got %d, want 200: %s", sortValue, rr.Code, rr.Body.String())
		}
	}
}

// TestGetUserJobs_RejectsLongSearch locks the 200-byte search cap.
// Without this, a client can DoS the jobs list endpoint by sending a
// massive search string that forces a full table scan in postgres.
func TestGetUserJobs_RejectsLongSearch(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/user?search="+strings.Repeat("a", 300), nil)
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetUserJobs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for over-cap search, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "200") {
		t.Errorf("expected error to mention the 200-byte cap, got: %s", rr.Body.String())
	}
}

// TestGetUserJobs_AcceptsShortSearch verifies the boundary at 200 bytes
// (inclusive) — exactly 200 must pass, 201 must fail.
func TestGetUserJobs_AcceptsShortSearch(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{App: &mockJobService{}, Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/user?search="+strings.Repeat("a", 200), nil)
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()
	h.GetUserJobs(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("search=200a expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ───────────────────── Task 3.3: pagination overflow + cap ─────────────────

// TestGetJobResults_RejectsOverflowPage covers the integer-overflow
// guard in parsePagination. With page=math.MaxInt32 and limit=100, the
// product (page-1)*limit overflows int32 silently — without the guard,
// the resulting offset is a large NEGATIVE number that produces nasty
// SQL behavior (Postgres rejects negative OFFSET, but the error path
// leaks DB internals to the client). The fix is a 400 BEFORE the
// multiplication runs.
func TestGetJobResults_RejectsOverflowPage(t *testing.T) {
	t.Parallel()

	jobID := "00000000-0000-0000-0000-000000000001"
	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/results?page=2147483647&limit=100", nil)
	req = mux.SetURLVars(req, map[string]string{"id": jobID})
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetJobResults(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for overflow page, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestGetJobResults_LimitCappedAt100 locks the cap unification — the
// audit found this endpoint previously allowed limit ≤ 1000, while
// GetUserJobs allowed only ≤ 100. The 10x divergence let an attacker
// pull 10x more rows per request from results endpoints. Unified at 100.
func TestGetJobResults_LimitCappedAt100(t *testing.T) {
	t.Parallel()

	jobID := "00000000-0000-0000-0000-000000000001"
	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/results?limit=500", nil)
	req = mux.SetURLVars(req, map[string]string{"id": jobID})
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetJobResults(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit=500, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "100") {
		t.Errorf("expected error to mention the 100-row cap, got: %s", rr.Body.String())
	}
}

// TestGetUserResults_LimitCappedAt100 covers the same cap on the
// offset-based endpoint.
func TestGetUserResults_LimitCappedAt100(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/results?limit=500", nil)
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetUserResults(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit=500, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestGetUserResults_RejectsOffsetOverflow covers the int32 ceiling on
// the offset parameter. Without the guard, a client passing
// offset=9223372036854775806 silently wraps to a near-zero offset on
// 32-bit downstream consumers, or simply overflows into negative
// values inside the response builder.
func TestGetUserResults_RejectsOffsetOverflow(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/results?offset=9223372036854775806", nil)
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetUserResults(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for overflow offset, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestGetUserJobs_RejectsZeroPage covers the lower bound — page must
// be a positive integer.
func TestGetUserJobs_RejectsZeroPage(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{App: &mockJobService{}, Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/user?page=0", nil)
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetUserJobs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for page=0, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestGetUserJobs_RejectsNegativeLimit covers the negative-limit path.
func TestGetUserJobs_RejectsNegativeLimit(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{App: &mockJobService{}, Auth: &auth.AuthMiddleware{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/user?limit=-5", nil)
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()

	h.GetUserJobs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative limit, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}
