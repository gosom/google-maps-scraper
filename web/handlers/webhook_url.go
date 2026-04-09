package handlers

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	webutils "github.com/gosom/google-maps-scraper/web/utils"
)

// ValidateWebhookURL parses and validates a webhook URL for safety.
// It enforces HTTPS-only and delegates the SSRF defense (DNS resolution
// + per-IP private/metadata blocklist) to webutils.AssertPublicHost so
// the same predicate is shared with ValidateProxyURL — see
// web/utils/private_ip.go for the canonical implementation.
//
// Returns the first resolved net.IP on success so the caller can pin
// the HTTP client to that exact address (NewWebhookHTTPClient does this
// to defend against DNS rebinding between validation and delivery).
func ValidateWebhookURL(rawURL string) (net.IP, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Scheme check: HTTPS only. This is webhook-specific (proxy URLs
	// use http/https/socks5) so it stays in this validator rather than
	// the shared helper.
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("only HTTPS URLs are allowed (got %q)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL must have a hostname")
	}

	return webutils.AssertPublicHost(host)
}

// NewWebhookHTTPClient returns an *http.Client that forces all connections to
// the given resolvedIP while preserving the original Host header for TLS/SNI.
// This prevents DNS rebinding attacks by ensuring the HTTP client connects only
// to the IP that was validated at registration time.
// Redirects are blocked to prevent SSRF via 3xx to internal IPs.
func NewWebhookHTTPClient(resolvedIP string, originalHost string) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				port = "443"
			}
			pinnedAddr := net.JoinHostPort(resolvedIP, port)
			return dialer.DialContext(ctx, network, pinnedAddr)
		},
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			ServerName: originalHost,
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
