package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// fakeIdempotencyRepo is an in-memory IdempotencyRepo used by these
// tests. It mirrors the postgres semantics that matter for the
// middleware:
//
//   - InsertStarted is atomic (synchronized via the mutex). Inserting
//     the same (user_id, key) twice returns ErrIdempotencyConflict.
//   - Get returns nil/nil for unknown keys.
//   - Complete only succeeds for an existing 'started' row.
//
// The fake is intentionally simple and only implements behavior the
// middleware actually exercises — exhaustive parity with the postgres
// repo would just duplicate test surface area.
type fakeIdempotencyRepo struct {
	mu   sync.Mutex
	rows map[string]*models.IdempotencyRecord // key = userID + "|" + key
}

func newFakeRepo() *fakeIdempotencyRepo {
	return &fakeIdempotencyRepo{rows: make(map[string]*models.IdempotencyRecord)}
}

func compositeKey(userID, key string) string { return userID + "|" + key }

func (f *fakeIdempotencyRepo) InsertStarted(_ context.Context, rec models.IdempotencyRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := compositeKey(rec.UserID, rec.Key)
	if _, ok := f.rows[k]; ok {
		return models.ErrIdempotencyConflict
	}
	cp := rec
	f.rows[k] = &cp
	return nil
}

func (f *fakeIdempotencyRepo) Get(_ context.Context, userID, key string) (*models.IdempotencyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.rows[compositeKey(userID, key)]
	if !ok {
		return nil, nil
	}
	cp := *rec
	return &cp, nil
}

func (f *fakeIdempotencyRepo) Complete(_ context.Context, id string, statusCode int, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.rows {
		if rec.ID == id && rec.Status == "started" {
			rec.Status = "completed"
			rec.StatusCode = statusCode
			rec.ResponseBody = append([]byte(nil), body...)
			now := time.Now().UTC()
			rec.CompletedAt = &now
			return nil
		}
	}
	return errors.New("no started row matched")
}

func (f *fakeIdempotencyRepo) CleanupExpired(_ context.Context, _ time.Duration) (int64, int64, error) {
	return 0, 0, nil
}

// withUserID returns a request with the test userID injected into the
// auth context, mirroring how the auth middleware would set it on a
// real request.
func withUserID(r *http.Request, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserIDKey, userID)
	return r.WithContext(ctx)
}

// TestIdempotency_NoKeyHeaderPassesThrough verifies the middleware is
// opt-in: requests without the Idempotency-Key header reach the inner
// handler unchanged, no row is inserted.
func TestIdempotency_NoKeyHeaderPassesThrough(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := Idempotency(repo, nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{}`))
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatal("inner handler should run when no Idempotency-Key header is set")
	}
	if len(repo.rows) != 0 {
		t.Errorf("no row should be inserted without a key header, got %d rows", len(repo.rows))
	}
}

// TestIdempotency_KeyTooLongRejected verifies oversized keys get a 400
// before any storage work happens — defense against an attacker
// flooding the table with megabyte-long key strings.
func TestIdempotency_KeyTooLongRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler must not run when key is over the length cap")
		w.WriteHeader(http.StatusOK)
	})
	mw := Idempotency(repo, nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{}`))
	req.Header.Set(IdempotencyHeader, strings.Repeat("a", maxKeyLen+1))
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized key, got %d", rr.Code)
	}
}

// TestIdempotency_ReplayReturnsCachedResponse exercises the happy
// replay path: the same (key, body) sent twice runs the handler exactly
// once and the second caller observes the cached response from the
// first call.
func TestIdempotency_ReplayReturnsCachedResponse(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	var calls int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"job-1"}`))
	})
	mw := Idempotency(repo, nil)(inner)

	do := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"name":"t"}`))
		req.Header.Set(IdempotencyHeader, "abc-123")
		req = withUserID(req, "user-1")
		mw.ServeHTTP(rr, req)
		return rr
	}

	first := do()
	second := do()

	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("expected 201/201, got %d/%d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("replay body mismatch: first=%q second=%q", first.Body.String(), second.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("inner handler must run exactly once on replay, ran %d times", got)
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay response should set Idempotent-Replayed: true header, got %q", second.Header().Get("Idempotent-Replayed"))
	}
}

// TestIdempotency_DifferentBodySameKeyRejected verifies the
// programming-error guard: the same key with a different body returns
// 409 instead of replaying the cached response of a logically
// different request.
func TestIdempotency_DifferentBodySameKeyRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	})
	mw := Idempotency(repo, nil)(inner)

	do := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
		req.Header.Set(IdempotencyHeader, "abc-123")
		req = withUserID(req, "user-1")
		mw.ServeHTTP(rr, req)
		return rr
	}

	if first := do(`{"name":"a"}`); first.Code != http.StatusOK {
		t.Fatalf("first call should succeed, got %d", first.Code)
	}
	second := do(`{"name":"b"}`)
	if second.Code != http.StatusConflict {
		t.Errorf("same key with different body must return 409, got %d", second.Code)
	}
	if !strings.Contains(second.Body.String(), "different_body") {
		t.Errorf("expected different_body error, got %q", second.Body.String())
	}
}

// TestIdempotency_UnauthenticatedPassesThrough verifies that requests
// without an authenticated user fall through to the inner handler
// (the route's auth middleware is responsible for the 401, not us).
func TestIdempotency_UnauthenticatedPassesThrough(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := Idempotency(repo, nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{}`))
	req.Header.Set(IdempotencyHeader, "abc-123")
	// No userID in context.
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatal("unauthenticated request should pass through to the inner handler")
	}
}

// TestIdempotency_ConcurrentRequests_HandlerRunsExactlyOnce is the
// critical test that pins the Stripe two-phase design. Fires N
// goroutines at the same (user_id, key, body) and asserts:
//
//  1. The inner handler runs EXACTLY ONCE across all N goroutines.
//  2. Every caller receives either the cached 200 (first to win)
//     or a 409 in_use response (arrived while the first was still
//     in flight). No other status code is acceptable.
//
// Any implementation that doesn't reserve the key BEFORE running the
// handler will fail (1) — the test is intentionally aggressive about
// the inner-handler call count to catch the naive
// "query-then-insert" race that the two-phase pattern fixes.
func TestIdempotency_ConcurrentRequests_HandlerRunsExactlyOnce(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	var calls int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		// Sleep so the goroutines actually overlap — without this,
		// the first goroutine would complete before the second
		// even starts and there would be no real concurrency.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"job-1"}`))
	})
	mw := Idempotency(repo, nil)(inner)

	const n = 20
	var wg sync.WaitGroup
	bodies := make([]string, n)
	statuses := make([]int, n)

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"name":"t"}`))
			req.Header.Set(IdempotencyHeader, "concurrent-key")
			req = withUserID(req, "user-1")
			mw.ServeHTTP(rr, req)
			bodies[i] = rr.Body.String()
			statuses[i] = rr.Code
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("inner handler must run EXACTLY once across %d concurrent requests, ran %d times", n, got)
	}

	var okCount, conflictCount int
	for i, code := range statuses {
		switch code {
		case http.StatusOK:
			if bodies[i] != `{"id":"job-1"}` {
				t.Errorf("request %d: expected cached body, got %q", i, bodies[i])
			}
			okCount++
		case http.StatusConflict:
			if !strings.Contains(bodies[i], "in_use") {
				t.Errorf("request %d: expected in_use error, got %q", i, bodies[i])
			}
			conflictCount++
		default:
			t.Fatalf("request %d: unexpected status %d body=%q", i, code, bodies[i])
		}
	}
	if okCount < 1 {
		t.Error("at least one request must observe the OK response")
	}
	if okCount+conflictCount != n {
		t.Errorf("status totals don't match: ok=%d conflict=%d expected=%d", okCount, conflictCount, n)
	}
}

// TestIdempotency_RepoFailureFailsOpen verifies the fail-open
// posture: if the storage layer is unreachable (returns a non-conflict
// error from InsertStarted), the middleware lets the request reach
// the inner handler unidempotent rather than hard-failing legitimate
// traffic on a transient outage. Logged as a warning.
func TestIdempotency_RepoFailureFailsOpen(t *testing.T) {
	t.Parallel()
	repo := &brokenRepo{}
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := Idempotency(repo, nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{}`))
	req.Header.Set(IdempotencyHeader, "abc-123")
	req = withUserID(req, "user-1")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatal("inner handler must run when the repo fails open")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("fail-open should return the inner handler's 200, got %d", rr.Code)
	}
}

// brokenRepo always returns a non-conflict error from InsertStarted —
// simulates an unreachable database.
type brokenRepo struct{}

func (brokenRepo) InsertStarted(_ context.Context, _ models.IdempotencyRecord) error {
	return errors.New("connection refused")
}
func (brokenRepo) Get(_ context.Context, _, _ string) (*models.IdempotencyRecord, error) {
	return nil, nil
}
func (brokenRepo) Complete(_ context.Context, _ string, _ int, _ []byte) error { return nil }
func (brokenRepo) CleanupExpired(_ context.Context, _ time.Duration) (int64, int64, error) {
	return 0, 0, nil
}
