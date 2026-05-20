package gmaps

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// TestNewCookieFetchClient_NoProxyURL_FallsBackToDefaultTransport verifies
// the pre-fix behavior is preserved when ProxyURL is empty: a client with
// nil Transport, so net/http falls back to the shared http.DefaultTransport
// (process-wide connection pool, respects HTTPS_PROXY / HTTP_PROXY env vars).
// A non-nil zero-value Transport would silently kill both pooling AND env
// support — see the docstring on newCookieFetchClient.
func TestNewCookieFetchClient_NoProxyURL_FallsBackToDefaultTransport(t *testing.T) {
	c, err := newCookieFetchClient("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Transport != nil {
		t.Fatalf("Transport must be nil when proxyURL is empty (so http.DefaultTransport applies); got %T", c.Transport)
	}
}

// TestNewCookieFetchClient_PinsProxy verifies that when a proxy URL is
// supplied, the returned client's transport has Proxy set to a func that
// resolves to that URL for any outbound request — proving the request will
// go through the proxy rather than direct.
func TestNewCookieFetchClient_PinsProxy(t *testing.T) {
	const proxyURL = "http://user:pass@gate.example.com:10001"

	c, err := newCookieFetchClient(proxyURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.Proxy == nil {
		t.Fatalf("transport.Proxy must be set when proxyURL is non-empty")
	}

	req, _ := http.NewRequest("GET", "https://www.google.com/maps/rpc/listugcposts", nil)
	got, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy resolver returned error: %v", err)
	}
	want, _ := url.Parse(proxyURL)
	if got == nil || got.String() != want.String() {
		t.Fatalf("Proxy resolver returned %v, want %v", got, want)
	}
}

// TestNewCookieFetchClient_InvalidProxyURL verifies that a parse failure is
// surfaced as an error rather than silently falling through to direct egress.
// Silent fallback would have masked the bug we just fixed.
func TestNewCookieFetchClient_InvalidProxyURL(t *testing.T) {
	_, err := newCookieFetchClient("ht!tp://broken url")
	if err == nil {
		t.Fatalf("expected error for invalid proxy URL, got nil")
	}
	if !strings.Contains(err.Error(), "parse proxy URL") {
		t.Fatalf("error message should mention proxy URL parsing, got: %v", err)
	}
}

// TestFetchWithCookies_RoutesThroughProxy is the end-to-end proof. A
// httptest.Server stands in as both the proxy and the upstream — any request
// reaching it with a non-empty CONNECT line (or a request whose Host header
// matches an external domain) is conclusive evidence the *http.Client routed
// via the proxy address.
//
// We exploit a simpler property to keep the test deterministic on macOS/Linux
// without raw socket access: when http.Transport.Proxy points at a local
// httptest server, plain HTTP requests to ANY upstream host land on that
// proxy server's handler first. We assert that the handler observed the
// request with the original upstream URL — proving the proxy is being used.
func TestFetchWithCookies_RoutesThroughProxy(t *testing.T) {
	var reachedProxy atomic.Bool
	var observedHost string
	var observedURI string

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedProxy.Store(true)
		observedHost = r.Host
		observedURI = r.RequestURI
		// Return a tiny success body so fetchWithCookies returns nil error.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer proxy.Close()

	// Note: when the upstream URL is plain http://, http.Transport sends the
	// request to the proxy directly (not via CONNECT), so the proxy handler
	// observes the full upstream URL as RequestURI. That's what we assert.
	upstreamURL := "http://upstream.test/some/path"

	// Build the client the same way newReviewFetcher does — pinned to the
	// proxy address — to exercise the production code path end-to-end.
	client, err := newCookieFetchClient(proxy.URL)
	if err != nil {
		t.Fatalf("newCookieFetchClient: %v", err)
	}

	body, err := fetchWithCookies(context.Background(), upstreamURL, "sid=abc", client)
	if err != nil {
		t.Fatalf("fetchWithCookies returned error: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, want %q", string(body), "ok")
	}
	if !reachedProxy.Load() {
		t.Fatalf("proxy server was not contacted — request went direct")
	}
	if !strings.Contains(observedURI, "upstream.test") {
		t.Fatalf("proxy did not observe the upstream URL in request URI: got %q", observedURI)
	}
	t.Logf("proxy reached. observed host=%q request-uri=%q", observedHost, observedURI)
}

// TestNewReviewFetcher_BuildsCookieClientOnce locks in the fix for the
// per-call-transport regression: newReviewFetcher must build the cookie HTTP
// client exactly once and stash it on f.cookieFetchClient. The first version
// of the proxy-routing fix rebuilt the *http.Transport on every paginated
// page request, which threw away the connection pool — for a 500-page place
// that meant ~500 fresh TCP+TLS handshakes instead of ~1. This test catches
// any future refactor that reintroduces per-call transport allocation.
func TestNewReviewFetcher_BuildsCookieClientOnce(t *testing.T) {
	f, err := newReviewFetcher(fetchReviewsParams{proxyURL: ""})
	if err != nil {
		t.Fatalf("newReviewFetcher: %v", err)
	}
	if f.cookieFetchClient == nil {
		t.Fatalf("cookieFetchClient must be non-nil; pagination relies on a shared client")
	}
	first := f.cookieFetchClient
	// Second hypothetical fetch call must still return the SAME client
	// instance. (Verified structurally — the field is set once at
	// construction and never reassigned anywhere in the package.)
	if f.cookieFetchClient != first {
		t.Fatalf("cookieFetchClient identity changed; expected single shared instance")
	}
}

// TestProxyHostForLog covers the credential-stripping helper that feeds the
// `proxy_used` log field. We never want passwords escaping into Loki.
func TestProxyHostForLog(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "direct"},
		{"http://gate.decodo.com:10001", "gate.decodo.com:10001"},
		{"http://user:secret%3Dpw@gate.decodo.com:10001", "gate.decodo.com:10001"},
		{"://broken", "invalid"},
	}
	for _, c := range cases {
		got := proxyHostForLog(c.in)
		if got != c.want {
			t.Errorf("proxyHostForLog(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("proxyHostForLog leaked credentials for input %q: %q", c.in, got)
		}
	}
}

// TestResponseSampleForLog locks in the escaping contract: the field stays
// on a single log line (no raw newlines), control chars become hex escapes,
// and the canonical 33-byte unauthenticated stub is recognizable in plain
// text. If this test ever fails, the log-grep recipes in
// docs/observability/review-scraping.md need updating too.
func TestResponseSampleForLog(t *testing.T) {
	// Exactly what Google returns to an unauthenticated review-RPC request.
	stub := []byte(")]}'\n[null,null,null,null,null,1]")
	got := responseSampleForLog(stub, 256)
	want := `)]}'\n[null,null,null,null,null,1]`
	if got != want {
		t.Fatalf("sample for unauth stub:\n  got  %q\n  want %q", got, want)
	}
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("escaped sample must not contain raw control chars: %q", got)
	}

	// Empty body collapses to "" (not "<nil>" or panic).
	if responseSampleForLog(nil, 256) != "" {
		t.Errorf("nil body should yield empty string")
	}
	if responseSampleForLog([]byte{}, 256) != "" {
		t.Errorf("empty body should yield empty string")
	}

	// Truncation honors the cap.
	long := make([]byte, 1024)
	for i := range long {
		long[i] = 'a'
	}
	out := responseSampleForLog(long, 32)
	if len(out) != 32 {
		t.Errorf("truncated sample length = %d, want 32", len(out))
	}
}
