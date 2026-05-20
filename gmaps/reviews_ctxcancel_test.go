// Proves Bug B: when the parent context the review fetcher receives is
// cancelled mid-request (because the ExitMonitor cancelled mateCtx after
// max_results was reached), the fetch dies. The default scrapemate stealth
// fetcher surfaces context cancellation as the literal string "timeout".
//
// The test is plumbed against an httptest.Server whose handler sleeps long
// enough for us to cancel the parent context before the response lands.
package gmaps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchWithCookies_DiesOnParentContextCancel(t *testing.T) {
	// Slow handler — long enough for us to cancel the request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	parent, cancel := context.WithCancel(context.Background())

	// Cancel quickly — simulates ExitMonitor cancelling mateCtx after another
	// worker hit max_results.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := fetchWithCookies(parent, srv.URL, "sess=abc", &http.Client{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected an error when parent context is cancelled mid-fetch, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context-cancellation error, got: %v", err)
	}
	t.Logf("baseline confirmed — fetch dies with: %v", err)
}

// TestFetchWithCookies_SurvivesParentCancel_WithDetachedContext is the fix
// hypothesis test: if the review fetcher detaches from the parent's
// cancellation signal (context.WithoutCancel) while keeping a bounded local
// deadline, it can complete the in-flight HTTP request even after
// max_results is hit.
func TestFetchWithCookies_SurvivesParentCancel_WithDetachedContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	parent, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Fix B: detach the fetch ctx from parent cancellation but cap with a
	// local deadline. Real implementation would live at the review-fetcher
	// call site in gmaps/place.go.
	fetchCtx, fetchCancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer fetchCancel()

	body, err := fetchWithCookies(fetchCtx, srv.URL, "sess=abc", &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("expected success with detached context, got error: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("got body %q, want %q", string(body), "ok")
	}
	t.Log("fix B confirmed — fetch completes after parent cancellation when ctx is detached")
}
