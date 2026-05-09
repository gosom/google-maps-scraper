//go:build integration

package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PROXIES env var format: comma-separated list of
//   http://USER:PASS@HOST:PORT
// e.g. for Decodo's mobile gateway with 10 sticky-session ports:
//   PROXIES="http://spf9syt8fu:PASS@gate.decodo.com:10001,http://spf9syt8fu:PASS@gate.decodo.com:10002,..."
//
// Run with:
//   go test -tags=integration -v ./proxy/ -run TestLive_

// readProxiesEnv returns the PROXIES env var split-and-trimmed, or skips
// the test when unset. Keeps creds out of the codebase.
func readProxiesEnv(t *testing.T) []string {
	t.Helper()
	raw := os.Getenv("PROXIES")
	if raw == "" {
		t.Skip("PROXIES env var not set — skipping live proxy integration test. " +
			`Run with: PROXIES="http://user:pass@host:port,..." go test -tags=integration ./proxy/`)
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	require.NotEmpty(t, out, "PROXIES set but contained no valid entries")
	return out
}

// freePort returns a TCP port that is currently free on 127.0.0.1. There
// is a small TOCTOU window between this and the next bind, but for local
// integration tests it's acceptable.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// fetchEgressIP makes an HTTPS request to api.ipify.org through the given
// HTTP proxy URL and returns the egress IP that the upstream observed.
// Failures bubble up with the proxy URL stripped so logs don't leak creds.
func fetchEgressIP(t *testing.T, localProxyURL string) string {
	t.Helper()
	parsed, err := url.Parse(localProxyURL)
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(parsed),
			ResponseHeaderTimeout: 30 * time.Second,
		},
		Timeout: 60 * time.Second,
	}

	resp, err := client.Get("https://api.ipify.org?format=json")
	require.NoError(t, err, "HTTPS request through local proxy failed")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "ipify must return 200 — non-200 means the proxy chain rejected us")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var got struct {
		IP string `json:"ip"`
	}
	require.NoError(t, json.Unmarshal(body, &got), "ipify response not valid JSON: %s", body)
	require.NotEmpty(t, got.IP, "ipify returned empty ip field")
	require.NotNil(t, net.ParseIP(got.IP), "ipify returned a string that is not a valid IP: %q", got.IP)
	return got.IP
}

// localEgressIP resolves the egress IP without using a proxy — used as a
// control to confirm the proxied request actually exits via Decodo and not
// our local network.
func localEgressIP(t *testing.T) string {
	t.Helper()
	resp, err := http.Get("https://api.ipify.org?format=json")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var got struct {
		IP string `json:"ip"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	return got.IP
}

// TestLive_SingleProxy_HTTPSRoundTrip is the core integration test:
// spin up our local forwarder pointing at the FIRST PROXIES entry, make a
// real HTTPS request through it, and confirm the egress IP is not our own
// LAN IP. This proves the whole chain works end-to-end:
//
//	client → 127.0.0.1:N (our forwarder) → gate.decodo.com:10001 → ipify
func TestLive_SingleProxy_HTTPSRoundTrip(t *testing.T) {
	proxyURLs := readProxiesEnv(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := freePort(t)
	server, err := NewServer(proxyURLs[0], port, logger)
	require.NoError(t, err)
	require.NoError(t, server.Start())
	t.Cleanup(server.Stop)

	control := localEgressIP(t)
	via := fetchEgressIP(t, server.GetLocalURL())

	t.Logf("local egress IP: %s", control)
	t.Logf("via Decodo:      %s", via)

	assert.NotEqual(t, control, via,
		"egress IP via the proxy must differ from the local IP — same IP means the request bypassed the proxy "+
			"or the upstream gate didn't accept our auth")
}

// TestLive_AllPortsAuthenticate sweeps every PROXIES entry and confirms
// each one separately authenticates and routes traffic. Catches the case
// where 9 of 10 ports work but one was misconfigured / rate-limited /
// has a stale credential — symptoms that are hard to diagnose in
// production rotation but trivial to spot one-shot in a test.
func TestLive_AllPortsAuthenticate(t *testing.T) {
	proxyURLs := readProxiesEnv(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for i, pu := range proxyURLs {
		i, pu := i, pu
		// Sanitize for subtest name — never include creds in test names
		// (they show up in -v output and CI logs).
		display := sanitizeProxyURL(pu)
		t.Run(fmt.Sprintf("port_%d_%s", i+1, display), func(t *testing.T) {
			port := freePort(t)
			server, err := NewServer(pu, port, logger)
			require.NoError(t, err, "NewServer must accept a valid PROXIES entry")
			require.NoError(t, server.Start())
			t.Cleanup(server.Stop)

			ip := fetchEgressIP(t, server.GetLocalURL())
			require.NotEmpty(t, ip)
			t.Logf("port %d egress: %s", i+1, ip)
		})
	}
}

// TestLive_StickySessionPersists makes two consecutive requests through
// the SAME local forwarder (same upstream port). Decodo's "Sticky 10min"
// session type pins the same exit IP for repeat traffic on a port.
// If we get two different IPs, the upstream forgot the session — tells
// us our forwarder is opening a fresh connection per request and losing
// the stickiness.
func TestLive_StickySessionPersists(t *testing.T) {
	proxyURLs := readProxiesEnv(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	port := freePort(t)
	server, err := NewServer(proxyURLs[0], port, logger)
	require.NoError(t, err)
	require.NoError(t, server.Start())
	t.Cleanup(server.Stop)

	first := fetchEgressIP(t, server.GetLocalURL())
	second := fetchEgressIP(t, server.GetLocalURL())

	t.Logf("first:  %s", first)
	t.Logf("second: %s", second)
	assert.Equal(t, first, second,
		"sticky-session port must keep the same exit IP across consecutive requests "+
			"(within the 10min window). Drift here means the upstream session is being lost")
}
