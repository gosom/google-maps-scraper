package webrunner

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/proxypool"
)

// TestProxyStatsHandler_NilPoolReturns503 guards the defensive nil-pool
// branch. The webrunner only registers the handler when proxies are
// configured, but the handler must not panic if called otherwise.
func TestProxyStatsHandler_NilPoolReturns503(t *testing.T) {
	h := newProxyStatsHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", resp.StatusCode)
	}
}

// TestProxyStatsHandler_ServesJSONSnapshot is the happy-path contract:
// returns 200, application/json, and a Stats payload that includes the
// configured proxies with credential-stripped hosts.
func TestProxyStatsHandler_ServesJSONSnapshot(t *testing.T) {
	pool, err := proxypool.New([]string{
		"http://user:secret@a.example.com:1",
		"http://user:secret@b.example.com:2",
	})
	if err != nil {
		t.Fatalf("proxypool.New: %v", err)
	}

	h := newProxyStatsHandler(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json prefix", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Credential check: the raw URL "user:secret@..." must NEVER appear in
	// the response. HostOf strips userinfo; this guards against a future
	// refactor that re-introduces a raw URL field.
	if strings.Contains(string(body), "secret") {
		t.Fatalf("response body leaked credentials: %s", string(body))
	}

	// Shape check: deserialize and verify totals match what we configured.
	var stats proxypool.Stats
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("unmarshal Stats: %v\nbody: %s", err, string(body))
	}
	if stats.TotalProxies != 2 {
		t.Errorf("TotalProxies: got %d, want 2", stats.TotalProxies)
	}
	if stats.Healthy != 2 {
		t.Errorf("Healthy: got %d, want 2", stats.Healthy)
	}
	for _, e := range stats.Entries {
		if strings.Contains(e.Host, "@") {
			t.Errorf("Host leaked userinfo: %q", e.Host)
		}
	}
}
