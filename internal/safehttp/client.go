// Package safehttp fetches public websites without allowing requests to private networks.
package safehttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	maxResponseHeaderBytes = 1 << 20
	schemeHTTP             = "http"
	schemeHTTPS            = "https"
)

// NewClient creates a client that validates and pins destination IPs on every
// request, including redirects. Only explicitly configured proxies are used.
// Proxies receive numeric destination addresses, preventing a second DNS lookup
// of the website hostname at the proxy. The proxy itself is trusted configuration.
func NewClient(proxies []string, timeout time.Duration) (*http.Client, error) {
	var proxyURL *url.URL

	if len(proxies) > 0 {
		var err error

		proxyURL, err = url.Parse(strings.TrimSpace(proxies[rand.IntN(len(proxies))])) //nolint:gosec // Proxy selection does not require cryptographic randomness.
		if err != nil || proxyURL.Hostname() == "" {
			return nil, errors.New("invalid email proxy URL")
		}

		switch proxyURL.Scheme {
		case schemeHTTP, schemeHTTPS, "socks5", "socks5h":
		default:
			return nil, errors.New("unsupported email proxy scheme")
		}
	}

	dialer := &net.Dialer{Timeout: timeout}

	return &http.Client{
		Timeout: timeout,
		Transport: &publicTransport{
			lookup:   net.DefaultResolver.LookupNetIP,
			dial:     dialer.DialContext,
			proxyURL: proxyURL,
			timeout:  timeout,
		},
	}, nil
}

type publicTransport struct {
	lookup   func(context.Context, string, string) ([]netip.Addr, error)
	dial     func(context.Context, string, string) (net.Conn, error)
	proxyURL *url.URL
	timeout  time.Duration
}

func (t *publicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.User != nil || req.URL.Opaque != "" || req.URL.Hostname() == "" ||
		(req.URL.Scheme != schemeHTTP && req.URL.Scheme != schemeHTTPS) {
		return nil, errors.New("email requests require an HTTP(S) URL without credentials")
	}

	addresses, err := t.lookup(req.Context(), "ip", req.URL.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve email website: %w", err)
	}

	if len(addresses) == 0 {
		return nil, errors.New("email website has no IP addresses")
	}

	// Reject mixed public/private answers as well. Never let the transport or
	// proxy resolve the hostname again after this check (DNS rebinding).
	for _, address := range addresses {
		if !isPublicIP(address) {
			return nil, errors.New("email website resolves to a non-public IP address")
		}
	}

	for _, address := range addresses {
		var resp *http.Response

		resp, err = t.roundTripIP(req, address.Unmap())
		if err == nil {
			return resp, nil
		}

		if req.Context().Err() != nil {
			break
		}
	}

	return nil, err
}

func (t *publicTransport) roundTripIP(req *http.Request, address netip.Addr) (*http.Response, error) {
	port := req.URL.Port()
	if port == "" {
		port = "80"
		if req.URL.Scheme == schemeHTTPS {
			port = "443"
		}
	}

	pinned := req.Clone(req.Context())
	pinned.URL.Host = net.JoinHostPort(address.String(), port)
	pinned.Host = req.URL.Host

	transport := &http.Transport{
		DialContext: t.dial,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: req.URL.Hostname(),
		},
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    t.timeout,
		ResponseHeaderTimeout:  t.timeout,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
	}
	if t.proxyURL != nil {
		switch t.proxyURL.Scheme {
		case schemeHTTP, schemeHTTPS:
			// Tunnel HTTP too: WriteProxy uses the Host header as the proxy
			// destination, which would undo IP pinning for virtual hosts.
			transport.DialContext = t.dialHTTPTunnel
		default:
			transport.Proxy = http.ProxyURL(t.proxyURL)
		}
	}

	resp, err := transport.RoundTrip(pinned)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}

	// Keep logical URLs for relative redirects and the caller's final-origin
	// check; only the network connection uses the validated numeric address.
	resp.Request = req

	return resp, nil
}

// These special-use ranges can pass IsGlobalUnicast but are not public website
// destinations. IPv6 is restricted to global allocations and excludes tunneling
// ranges that could encode private IPv4 destinations.
var excludedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("168.63.129.16/32"), // Azure platform virtual IP.
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func isPublicIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.Zone() != "" {
		return false
	}

	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}

	for _, network := range excludedNetworks {
		if network.Contains(address) {
			return false
		}
	}

	return true
}
