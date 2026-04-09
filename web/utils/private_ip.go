// Package utils — private/internal IP defense shared by ValidateWebhookURL
// (web/handlers/webhook_url.go) and ValidateProxyURL (web/utils/validation.go).
//
// Both call sites need the same predicate: given a host string, refuse it
// if any DNS-resolved IP is loopback, link-local, private, unspecified,
// or in a known cloud-metadata range. Keeping the predicate in ONE place
// means a future expansion of the blocklist (e.g. adding more cloud
// metadata IPs) updates both checks at once instead of drifting.
//
// Known limitation — DNS TOCTOU. This validator resolves the host at
// validation time. An attacker who controls a DNS record can return a
// public IP at validation time and 169.254.169.254 at use time, bypassing
// the check. The complete defense lives at the HTTP transport layer
// (a custom net.Dialer.Control hook that re-checks the resolved IP just
// before TCP connect). For webhook delivery this is partially mitigated
// by NewWebhookHTTPClient which pins the resolved IP at registration time.
// For proxy URLs there is no equivalent — see Task 3.5 in the audit plan
// for the upstream-issue follow-up.
package utils

import (
	"fmt"
	"net"
)

// blockedCIDRs is the closed list of metadata + carrier-grade NAT ranges
// that are not caught by the stdlib `IsPrivate`/`IsLoopback` predicates
// but should still be blocked. Add to this list when new cloud metadata
// addresses ship.
var blockedCIDRs []*net.IPNet

func init() {
	cidrs := []string{
		// AWS instance metadata endpoint (IMDS).
		"169.254.169.254/32",
		// AWS ECS task metadata endpoint.
		"169.254.170.2/32",
		// AWS IPv6 metadata endpoint.
		"fd00:ec2::254/128",
		// Carrier-grade NAT (RFC 6598). Not strictly private under
		// stdlib predicates but commonly used inside cloud VPCs and
		// should be treated as internal for SSRF purposes.
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

// CheckIPBlocklist rejects an IP if it falls in a private, loopback,
// link-local, unspecified, or cloud-metadata range. Returns nil if the
// IP is acceptable for outbound traffic (i.e., a normal public address).
//
// This is the per-IP primitive shared by AssertPublicHost (which does
// DNS resolution + per-IP iteration) and any other validator that
// already has an IP in hand.
func CheckIPBlocklist(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("nil IP")
	}
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

// AssertPublicHost resolves a hostname (or accepts a literal IP) and
// returns nil only if every resolved address passes CheckIPBlocklist.
//
// Why every address: a dual-homed host may resolve to e.g.
// [8.8.8.8, 127.0.0.1]. The HTTP client may connect to either; if any
// resolves to a blocked range, the URL must be rejected. Allowing the
// URL on the basis of the "first acceptable" IP would let an attacker
// stack one public address in front of a metadata IP and bypass the
// check on a connection-by-connection basis.
//
// Returns the first resolved net.IP on success — callers that want to
// pin the connection to a specific address (e.g., the webhook delivery
// HTTP client) can use it directly.
func AssertPublicHost(host string) (net.IP, error) {
	if host == "" {
		return nil, fmt.Errorf("host is empty")
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("DNS resolution returned no addresses for %q", host)
	}
	var firstIP net.IP
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			return nil, fmt.Errorf("could not parse resolved IP %q", addr)
		}
		if err := CheckIPBlocklist(ip); err != nil {
			return nil, err
		}
		if firstIP == nil {
			firstIP = ip
		}
	}
	return firstIP, nil
}
