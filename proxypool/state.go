// Package proxypool provides a health-aware HTTP proxy pool with per-proxy
// failure tracking. Entries transition through three states:
//
//	healthy → cooling: after N consecutive failures (default 3); off-rotation
//	                   until the cooling deadline elapses
//	cooling → healthy: when a subsequent acquisition succeeds (lazy promotion)
//	*       → quarantined: after a BlockedByTarget event or M cumulative failures
//	                       (default 10); off-rotation until process restart
//
// The pool is safe for concurrent use. State is held in memory only — see
// the storage decision in
// docs/superpowers/plans/2026-05-20-proxy-pool-with-health-tracking.md
// for why Postgres persistence was deferred.
//
// Persistence is intentionally out of scope for V1. Pool state lives in
// memory only and resets on process restart. When the triggers in the plan
// doc fire, persistence will land as a follow-up: a Repository interface
// added at that time, called once on New (Load) and on shutdown (Save).
// Anchoring the seam in this doc comment rather than an unused interface
// keeps the V1 surface honest.
package proxypool

import "time"

type state uint8

const (
	stateHealthy state = iota
	stateCooling
	stateQuarantined
)

func (s state) String() string {
	switch s {
	case stateHealthy:
		return "healthy"
	case stateCooling:
		return "cooling"
	case stateQuarantined:
		return "quarantined"
	default:
		return "unknown"
	}
}

// FailureReason classifies why a proxy was reported as failed. The category
// drives the state transition — most failures cool the entry, but a
// BlockedByTarget event jumps straight to quarantine (the proxy is burned).
type FailureReason int

const (
	// SoftReject is the 33-byte unauthenticated stub Google returns when it
	// recognizes the proxy IP as datacenter or otherwise untrusted. The
	// cookies are valid but the IP isn't. Cools the entry.
	SoftReject FailureReason = iota
	// NetworkErr is a connection/timeout failure reaching the proxy or
	// through it. Cools the entry.
	NetworkErr
	// ProxyErr is a 5xx from the proxy itself (gateway down, auth rejected,
	// quota exceeded). Cools the entry.
	ProxyErr
	// BlockedByTarget is an explicit "you are a bot" page or 403/429 from
	// the target. Jumps straight to quarantine — the proxy is burned for
	// the rest of the process lifetime.
	BlockedByTarget
)

func (r FailureReason) String() string {
	switch r {
	case SoftReject:
		return "soft_reject"
	case NetworkErr:
		return "network_err"
	case ProxyErr:
		return "proxy_err"
	case BlockedByTarget:
		return "blocked_by_target"
	default:
		return "unknown"
	}
}

// entry is the per-proxy mutable state. All fields are guarded by Pool.mu —
// callers must NOT read or write entry fields without holding the pool lock.
type entry struct {
	url               string
	state             state
	nextOK            time.Time // when stateCooling can re-promote to stateHealthy
	consecutiveFails  int       // resets to 0 on success; drives cooling
	cumulativeFails   int64     // never resets; drives quarantine
	totalSuccesses    int64
	lastFailureReason FailureReason
	lastTransitionAt  time.Time
}
