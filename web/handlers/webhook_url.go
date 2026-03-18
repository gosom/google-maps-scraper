package handlers

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateWebhookURL parses and validates a webhook URL for safety.
// It enforces HTTPS-only, resolves DNS, and checks the resolved IP against
// a blocklist of private/loopback/link-local/metadata ranges (SSRF prevention).
// Returns the resolved net.IP on success.
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

	ip := net.ParseIP(addrs[0])
	if ip == nil {
		return nil, fmt.Errorf("could not parse resolved IP %q", addrs[0])
	}

	if err := checkIPBlocklist(ip); err != nil {
		return nil, err
	}

	return ip, nil
}

// portFromURL returns the port from a URL, defaulting to 443 for https.
func portFromURL(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	return "443"
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

	// Block AWS/GCP/Azure metadata endpoints
	metadataRanges := []string{
		"169.254.169.254/32", // AWS, GCP, Azure metadata
		"169.254.170.2/32",   // AWS ECS metadata
		"fd00:ec2::254/128",  // AWS IMDSv2 IPv6
	}
	for _, cidr := range metadataRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil && network.Contains(ip) {
			return fmt.Errorf("cloud metadata addresses are not allowed")
		}
	}

	return nil
}
