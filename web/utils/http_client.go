package utils

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// NewIPPinnedClient returns an *http.Client that forces all connections to the
// given resolvedIP while preserving the original Host header for TLS/SNI.
// This prevents DNS rebinding attacks by ensuring the HTTP client connects only
// to the IP that was validated at registration time.
// Redirects are blocked to prevent SSRF via 3xx to internal IPs.
func NewIPPinnedClient(resolvedIP string, originalHost string) *http.Client {
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
