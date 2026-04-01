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
)

var blockedCIDRs []*net.IPNet

func init() {
	cidrs := []string{
		"169.254.169.254/32",
		"169.254.170.2/32",
		"fd00:ec2::254/128",
		"100.64.0.0/10",
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid CIDR in blocklist: %s", cidr))
		}
		blockedCIDRs = append(blockedCIDRs, network)
	}
}

// ValidateWebhookURL parses and validates a webhook URL for safety.
// It enforces HTTPS-only, resolves DNS, and checks ALL resolved IPs against
// a blocklist of private/loopback/link-local/metadata ranges (SSRF prevention).
// Returns the first resolved net.IP on success.
func ValidateWebhookURL(rawURL string) (net.IP, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Scheme check: HTTPS only
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("only HTTPS URLs are allowed (got %q)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL must have a hostname")
	}

	// Resolve DNS to pin the IP and check against blocklist.
	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("DNS resolution returned no addresses for %q", host)
	}

	// Check ALL resolved addresses against the blocklist.
	// A dual-homed host may return e.g. [8.8.8.8, 127.0.0.1] — if any IP is
	// blocked we must reject the URL since the HTTP client may connect to any of them.
	var firstIP net.IP
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			return nil, fmt.Errorf("could not parse resolved IP %q", addr)
		}
		if err := checkIPBlocklist(ip); err != nil {
			return nil, err
		}
		if firstIP == nil {
			firstIP = ip
		}
	}

	return firstIP, nil
}

// checkIPBlocklist rejects IPs in private, loopback, link-local, and cloud
// metadata ranges to prevent SSRF attacks.
func checkIPBlocklist(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("loopback addresses are not allowed")
	}
	if ip.IsPrivate() {
		return fmt.Errorf("private network addresses are not allowed")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("link-local addresses are not allowed")
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("unspecified addresses are not allowed")
	}

	for _, network := range blockedCIDRs {
		if network.Contains(ip) {
			return fmt.Errorf("blocked CIDR range %s: address not allowed", network.String())
		}
	}

	return nil
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
