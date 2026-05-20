package proxypool

import (
	"net/url"
	"time"
)

// Stats is a read-only point-in-time snapshot of the pool. Returned by
// Pool.Stats; serialized for the /internal/proxy/stats HTTP endpoint.
type Stats struct {
	TotalProxies int          `json:"total_proxies"`
	Healthy      int          `json:"healthy"`
	Cooling      int          `json:"cooling"`
	Quarantined  int          `json:"quarantined"`
	Entries      []EntryStats `json:"entries"`
}

// EntryStats is the per-proxy view inside Stats. Host returns the host:port
// (credential-stripped via HostOf) so the snapshot is safe to log or serve
// over an internal HTTP endpoint without leaking auth.
type EntryStats struct {
	Host              string    `json:"host"`
	State             string    `json:"state"`
	NextOK            time.Time `json:"next_ok,omitzero"`
	ConsecutiveFails  int       `json:"consecutive_fails"`
	CumulativeFails   int64     `json:"cumulative_fails"`
	TotalSuccesses    int64     `json:"total_successes"`
	LastFailureReason string    `json:"last_failure_reason,omitempty"`
	LastTransitionAt  time.Time `json:"last_transition_at"`
}

// HostOf returns the host:port from a proxy URL with any userinfo
// (user:password@) stripped. Returns "" for an empty input and "invalid"
// for an unparseable URL.
//
// Exported as the single source of truth for credential-free proxy
// logging. gmaps and runner/webrunner each previously held near-identical
// copies; both will be refactored to import this — see Task 8b in the
// implementation plan.
func HostOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	pu, err := url.Parse(rawURL)
	if err != nil || pu.Host == "" {
		return "invalid"
	}
	return pu.Host
}
