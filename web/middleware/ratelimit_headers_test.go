package middleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestRateLimitHeaders_EmitsBothModernAndLegacy locks in the contract that a
// success response carries the full header set:
//   - RateLimit-Policy + RateLimit (IETF draft-10 structured fields)
//   - X-RateLimit-Limit / -Remaining / -Reset (legacy triplet)
//
// The middleware must run AFTER a limiter middleware (so the snapshot is in
// context) and BEFORE the actual handler (so Go can flush all headers
// together when the handler writes its status).
func TestRateLimitHeaders_EmitsBothModernAndLegacy(t *testing.T) {
	limiter := PerIPRateLimit(rate.Limit(5), 10)
	headers := RateLimitHeaders()
	h := limiter(headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	// Modern: RateLimit-Policy: api;q=10;w=<int>  (bare token, NOT quoted)
	gotPolicy := rec.Header().Get("RateLimit-Policy")
	if !regexp.MustCompile(`^api;q=10;w=\d+$`).MatchString(gotPolicy) {
		t.Errorf(`RateLimit-Policy: expected api;q=10;w=<int>, got %q`, gotPolicy)
	}
	// Modern: RateLimit: api;r=<int>;t=<int>
	gotRL := rec.Header().Get("RateLimit")
	rlMatch := regexp.MustCompile(`^api;r=(\d+);t=(\d+)$`).FindStringSubmatch(gotRL)
	if rlMatch == nil {
		t.Errorf(`RateLimit: expected api;r=<int>;t=<int>, got %q`, gotRL)
	}

	// Legacy triplet
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "10" {
		t.Errorf("X-RateLimit-Limit: want 10, got %q", got)
	}
	gotRemainingLegacy := rec.Header().Get("X-RateLimit-Remaining")
	if gotRemainingLegacy == "" {
		t.Error("X-RateLimit-Remaining: expected non-empty")
	}
	gotResetEpoch := rec.Header().Get("X-RateLimit-Reset")
	if gotResetEpoch == "" {
		t.Error("X-RateLimit-Reset: expected non-empty")
	}
	resetEpoch, err := strconv.ParseInt(gotResetEpoch, 10, 64)
	if err != nil {
		t.Fatalf("X-RateLimit-Reset: expected Unix epoch int, got %q (%v)", gotResetEpoch, err)
	}

	// Cross-field consistency: modern `r=` must match legacy Remaining,
	// modern `t=` must match (Reset - now). Catches a regression where the
	// two header families drift apart (e.g. one hard-coded, the other
	// derived from a different snapshot).
	if rlMatch != nil {
		if rlMatch[1] != gotRemainingLegacy {
			t.Errorf("modern RateLimit r=%s must equal X-RateLimit-Remaining %s", rlMatch[1], gotRemainingLegacy)
		}
		rT, _ := strconv.ParseInt(rlMatch[2], 10, 64)
		// Allow ±1s drift for clock movement between header writes.
		drift := resetEpoch - time.Now().Unix() - rT
		if drift < -1 || drift > 1 {
			t.Errorf("modern RateLimit t=%d should equal (X-RateLimit-Reset - now) within 1s, got drift=%d", rT, drift)
		}
	}
}

// TestRateLimitHeaders_RemainingDecrementsAcrossRequests pins the post-Allow
// snapshot contract: each successful request emits one fewer remaining
// token than the previous. Prevents a regression where the snapshot is
// taken pre-Allow (off by one) or where the bucket is double-checked from
// a clean state on every request.
func TestRateLimitHeaders_RemainingDecrementsAcrossRequests(t *testing.T) {
	limiter := PerIPRateLimit(rate.Limit(1), 5) // slow refill, small burst
	headers := RateLimitHeaders()
	h := limiter(headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	fire := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "8.8.8.8:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (Remaining trace broken)", rec.Code)
		}
		got, err := strconv.Atoi(rec.Header().Get("X-RateLimit-Remaining"))
		if err != nil {
			t.Fatalf("parse X-RateLimit-Remaining: %v", err)
		}
		return got
	}

	first := fire()
	second := fire()
	third := fire()

	if !(first > second && second > third) {
		t.Errorf("expected strictly decreasing Remaining across consecutive requests, got %d -> %d -> %d", first, second, third)
	}
}

// TestRateLimitHeaders_EmittedOn429 is the regression test for "headers
// missing on 429" — clients should know "remaining=0" without guessing.
// The dispatcher must write the same header set inline on the 429 path.
func TestRateLimitHeaders_EmittedOn429(t *testing.T) {
	limiter := PerIPRateLimit(rate.Limit(1), 1)
	h := limiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "7.7.7.7:1234"
	{
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("setup: first request must be 200, got %d", rec.Code)
		}
	}
	{
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429, got %d", rec.Code)
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != "0" {
			t.Errorf("X-RateLimit-Remaining on 429: want 0, got %q", got)
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "1" {
			t.Errorf("X-RateLimit-Limit on 429: want 1, got %q", got)
		}
		if got := rec.Header().Get("RateLimit"); !strings.HasPrefix(got, "api;r=0;t=") {
			t.Errorf("RateLimit on 429: want prefix api;r=0;t=, got %q", got)
		}
		if got := rec.Header().Get("Retry-After"); got != "1" {
			t.Errorf("Retry-After on 429: want 1, got %q", got)
		}
	}
}

// TestRateLimitHeaders_NoSnapshotSkipsEmission is the safety belt for
// public routes (no limiter in the chain): the middleware must NOT write
// nonsense headers when no snapshot is in context. Otherwise mounting it
// on the public router would broadcast a phantom "api" policy with q=0
// that misleads clients.
func TestRateLimitHeaders_NoSnapshotSkipsEmission(t *testing.T) {
	headers := RateLimitHeaders()
	h := headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("RateLimit"); got != "" {
		t.Errorf("RateLimit must be empty when no snapshot in context, got %q", got)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "" {
		t.Errorf("X-RateLimit-Limit must be empty when no snapshot in context, got %q", got)
	}
}
