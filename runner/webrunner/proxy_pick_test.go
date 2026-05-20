package webrunner

import (
	"testing"
)

// TestPickProxyURL_RoundRobinAndIndexing locks in two contracts at once:
//   - URL rotates round-robin across the configured pool,
//   - Index is 1-based and PoolSize is reported on every assignment,
//
// so the `proxy_assigned` debug log can carry both. LogQL recipes in
// docs/observability/review-scraping.md grep on `index=` and `of=`; if a
// future refactor drops them again (as the first version of this fix did),
// this test fails.
func TestPickProxyURL_RoundRobinAndIndexing(t *testing.T) {
	w := &webrunner{
		proxyURLs: []string{
			"http://u:p@proxy-a.example:1",
			"http://u:p@proxy-b.example:2",
			"http://u:p@proxy-c.example:3",
		},
	}

	wantSequence := []struct {
		url   string
		idx   int
		total int
	}{
		{"http://u:p@proxy-a.example:1", 1, 3},
		{"http://u:p@proxy-b.example:2", 2, 3},
		{"http://u:p@proxy-c.example:3", 3, 3},
		{"http://u:p@proxy-a.example:1", 1, 3}, // wraps
		{"http://u:p@proxy-b.example:2", 2, 3},
	}

	for i, want := range wantSequence {
		got := w.pickProxyURL()
		if got.URL != want.url || got.Index != want.idx || got.PoolSize != want.total {
			t.Errorf("call %d: got {URL=%q Index=%d PoolSize=%d}, want {URL=%q Index=%d PoolSize=%d}",
				i+1, got.URL, got.Index, got.PoolSize, want.url, want.idx, want.total)
		}
	}
}

// TestPickProxyURL_EmptyPool returns the zero-value assignment without
// incrementing the rotation counter. The zero value (URL=="") is what
// CLI/standalone and tests-without-proxies pass through, and the cookie
// fetch path falls back to direct egress via http.DefaultTransport — see
// gmaps.newCookieFetchClient.
func TestPickProxyURL_EmptyPool(t *testing.T) {
	w := &webrunner{} // proxyURLs is nil

	got := w.pickProxyURL()
	if got.URL != "" || got.Index != 0 || got.PoolSize != 0 {
		t.Fatalf("empty pool should return zero-value proxyAssignment, got %+v", got)
	}
	// Second call: still zero, no panic on modulo-by-zero, no counter bump.
	got = w.pickProxyURL()
	if got.URL != "" || got.Index != 0 || got.PoolSize != 0 {
		t.Fatalf("empty pool second call should still return zero-value, got %+v", got)
	}
}

// (TestProxyHostForLog removed — coverage is now in proxypool.TestHostOf;
// the local proxyHostForLog helper was deleted in favor of the exported
// proxypool.HostOf.)
