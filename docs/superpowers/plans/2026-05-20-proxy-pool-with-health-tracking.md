# Proxy Pool with Health Tracking — Implementation Plan

> **For agentic workers:** REQUIRED: Use @superpowers:subagent-driven-development (if subagents available) or @superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking. Apply @superpowers:test-driven-development for every code change.

**Goal:** Replace the naïve round-robin proxy selection in `runner/webrunner/webrunner.go` with a health-aware `proxypool.Pool` that quarantines failing proxies, surfaces exhaustion as a first-class error, and exposes per-proxy observability — engine-agnostic so a future scrapemate replacement (e.g., the v2 C++ engine in `docs/superpowers/plans/2026-05-12-scraper-v2-architecture.md`) plugs in without touching the pool.

**Architecture:** In-memory state in a `proxypool.Pool` struct guarded by `sync.Mutex`. Per-entry state machine (`healthy → cooling → quarantined`) modeled after the existing `webhook_circuit_breaker` pattern (`postgres/webhook.go:276-336`). Callers `Acquire()` a `Lease`, use the URL, and report `Success` / `Failure(reason)` exactly once. Persistence is explicitly NOT shipped in V1 — the seam is documented in a comment so the future Postgres impl knows where it goes, but no unused-interface YAGNI is added now. See Task 9 for the rationale.

**Tech Stack:** Pure stdlib Go (`sync`, `time`, `errors`). No new dependencies. No SQL migrations.

---

## Storage Decision

Five options were evaluated. Pasted as a table for the record:

| Option | Survives Restart | Cross-Process | New Infra | V1 LoC | Verdict |
|---|---|---|---|---|---|
| **A. Pure in-memory** | ❌ | ❌ | None | ~200 | **Chosen for V1** |
| B. PostgreSQL table | ✅ | ✅ | Migration | ~400 | Premature; reserved for V2 |
| C. Hybrid (memory + async PG persist) | ✅ | △ | Migration | ~500 | Overkill for current scale |
| D. Embedded SQLite/BoltDB | ✅ | ❌ | New dep | ~350 | Out of place — Postgres already exists |
| E. In-memory + JSON dump on shutdown | △ (graceful only) | ❌ | None | ~300 | Brittle (crashes lose state) |

### Why in-memory wins for V1

1. **Scale matches.** We have 10 proxies and one webrunner process. A burnt proxy is re-discovered the next time a scrape happens to hand it out — at the cost of one scrape's worth of failed reviews (the empty-response circuit breaker trips after 3 stub responses in that scrape, which is a few minutes of real time). The amortized cost across our deploy cadence is small: a burnt proxy taints at most one scrape per restart per proxy. Annoying, not breaking.

2. **No new operational surface.** Postgres becomes a critical path for *scraping*, not just for job state. Today it's only critical for billing/auth. Adding the proxy pool there increases the blast radius of a DB outage from "can't create jobs" to "can't acquire a proxy at all."

3. **Code complexity is 2× lower.**
   - In-memory: `proxypool/pool.go` + `proxypool/state.go` + tests ≈ 200 LoC
   - PostgreSQL: same + `postgres/proxypool_repository.go` + migration + DB integration tests + ORM-y CAS handling ≈ 400 LoC
   - The extra 200 LoC buys persistence we don't measurably need at our scale.

4. **Migration path stays open without unused-interface debt.** The Pool exposes its state via `Stats()` (a value-only snapshot). When persistence becomes real, a future PR adds a `Repository` interface, calls `Load()` in `New()` and `Save()` on shutdown, and ships the Postgres implementation in the same PR. The `Pool` itself doesn't change shape; only its constructor gains an option. No speculative interface in V1.

### Trigger to revisit (write Postgres impl)

- More than one webrunner process running concurrently (cross-process consistency required)
- Operations needs historical ban events queryable via SQL
- Deploy frequency exceeds once per day (cumulative cooling re-discovery cost becomes meaningful)

---

## Engine-Agnostic Boundary

The pool's public surface is intentionally engine-free:

```go
// proxypool exposes ONLY these types/functions:
type Pool struct { /* unexported */ }
func New(urls []string, opts ...Option) *Pool

func (p *Pool) Acquire() (Lease, error)
func (p *Pool) Stats() Stats

type Lease struct { URL string /* + unexported */ }
func (l Lease) ReportSuccess()
func (l Lease) ReportFailure(reason FailureReason)

type FailureReason int
type Stats struct { /* read-only snapshot */ }

// no scrapemate import. no http.Client. no engine concept.
```

### Why this survives an engine swap

- The pool deals in `string` proxy URLs and `FailureReason` enums. Both are engine-neutral.
- The engine — scrapemate today, possibly a C++ binary tomorrow — receives a `Lease.URL` string and uses it however it wants. The Pool doesn't know or care.
- Success/failure reporting is at JOB granularity, not request granularity. The webrunner decides at job end whether to call `ReportSuccess()` or `ReportFailure(...)`. The engine just runs the job.
- The future C++ engine plugs in by replacing the `mate.Start(ctx, seeds...)` line in `runner/webrunner/webrunner.go`. The proxy plumbing — `pool.Acquire() → URL → engine config → ReportSuccess/Failure at end` — stays identical.

### Coupling check

| File modified by this plan | Imports scrapemate? | Notes |
|---|---|---|
| `proxypool/*` | ❌ NO | Pure domain. |
| `runner/webrunner/webrunner.go` | ✅ Already does | Bridge layer. |
| `gmaps/place.go` (one small exported counter) | ✅ Already does | Read-only signal export. |

---

## File Structure

### To be created

| Path | Responsibility |
|---|---|
| `proxypool/pool.go` | `Pool` struct, `New()`, `Acquire()`, `Stats()` |
| `proxypool/state.go` | `state` enum, `entry` struct, transition logic |
| `proxypool/lease.go` | `Lease` type, `ReportSuccess()`, `ReportFailure()` |
| `proxypool/options.go` | Functional options (`WithClock`, `WithThresholds`) |
| `proxypool/pool_test.go` | Pool unit tests |
| `proxypool/lease_test.go` | Lease unit tests |
| `proxypool/concurrency_test.go` | Mutex contention tests |

### To be modified

| Path | Change |
|---|---|
| `runner/webrunner/webrunner.go` | Replace `pickProxyURL` with `proxypool.Pool`; classify outcome → Lease report; handle `ErrPoolExhausted` |
| `runner/webrunner/webrunner.go` | New `/internal/proxy/stats` HTTP endpoint |
| `gmaps/place.go` | Export `ReviewEmptyCount()` so webrunner can read the circuit-breaker state at job end |

### Not touched

- `gmaps/reviews.go` — already correct after PR #82.
- `runner/jobs.go` — `SeedJobConfig.ProxyURL` stays unchanged; webrunner still threads the URL through.
- `gmaps/job.go` — `GmapJob.ProxyURL` stays unchanged.
- Any scrapemate integration code — zero changes.

---

## Chunk 1: Pool Foundation (state machine + round-robin)

> **Execution log:** Branch `feat/proxy-pool-with-health-tracking` off develop `f2a8064`. Plan committed `4f784eb`.
> - Task 1 → `61c14f5` (package skeleton: state.go, pool.go, options.go, pool_test.go)
> - Task 2 → `d02e9f7` (round-robin Acquire + Lease type)
> - Task 3 → `a571839` (skip cooling/quarantined + isUsableLocked + range-over-int)
> - Chunk 1 review fixes → `b7de0cc` (drop dead guard, white-box comment, cooling-expired test, cursor-wrap test)
> - **Chunk 1 ✅ production-ready** (Sonnet reviewer verified)

### Task 1: Package skeleton and types

**Files:**
- Create: `proxypool/state.go`
- Create: `proxypool/pool.go`
- Create: `proxypool/pool_test.go`

- [ ] **Step 1: Create `proxypool/state.go` with the state enum, FailureReason, and entry struct**

`FailureReason` lives in `state.go` (not `lease.go`) so the `entry` struct can reference it without a forward dependency on the lease file. Both are leaf types; keeping them together keeps the type graph acyclic.

```go
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
// the storage decision in docs/superpowers/plans/2026-05-20-proxy-pool-with-health-tracking.md
// for why Postgres persistence was deferred.
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
```

- [ ] **Step 2: Create `proxypool/pool.go` skeleton**

```go
package proxypool

import (
	"errors"
	"sync"
	"time"
)

// ErrPoolExhausted is returned by Acquire when every entry in the pool is
// either cooling or quarantined. Callers should surface this as a hard
// failure for the current scrape — there is no healthy proxy to use.
var ErrPoolExhausted = errors.New("proxypool: all proxies unavailable")

// ErrEmptyPool is returned by New when constructed with zero URLs. We treat
// this as a programmer error rather than a runtime state because the
// alternative — silently succeeding then returning ErrPoolExhausted on first
// Acquire — masks misconfiguration.
var ErrEmptyPool = errors.New("proxypool: cannot construct pool with zero URLs")

// Default thresholds. Override via WithThresholds.
const (
	defaultCoolingFailThreshold    = 3
	defaultQuarantineFailThreshold = 10
	defaultBaseCoolDuration        = 30 * time.Second
	defaultMaxCoolDuration         = 30 * time.Minute
)

// Pool is a thread-safe rotating pool of proxy URLs with per-proxy health
// tracking. Construct with New. Callers Acquire a Lease, use the URL, and
// report success/failure exactly once on the Lease before discarding it.
type Pool struct {
	mu      sync.Mutex
	entries []*entry
	cursor  int // round-robin starting index for next Acquire

	// thresholds and timings (set via options)
	coolingFailThreshold    int
	quarantineFailThreshold int
	baseCoolDuration        time.Duration
	maxCoolDuration         time.Duration

	clock clock
}

// New constructs a Pool over the supplied proxy URLs. Returns ErrEmptyPool
// when urls is nil or empty.
func New(urls []string, opts ...Option) (*Pool, error) {
	if len(urls) == 0 {
		return nil, ErrEmptyPool
	}
	p := &Pool{
		entries:                 make([]*entry, 0, len(urls)),
		coolingFailThreshold:    defaultCoolingFailThreshold,
		quarantineFailThreshold: defaultQuarantineFailThreshold,
		baseCoolDuration:        defaultBaseCoolDuration,
		maxCoolDuration:         defaultMaxCoolDuration,
		clock:                   realClock{},
	}
	for _, opt := range opts {
		opt(p)
	}
	now := p.clock.Now()
	for _, u := range urls {
		p.entries = append(p.entries, &entry{
			url:              u,
			state:            stateHealthy,
			lastTransitionAt: now,
		})
	}
	return p, nil
}
```

- [ ] **Step 3: Create `proxypool/options.go` with functional options and clock injection**

```go
package proxypool

import "time"

// Option configures a Pool. See WithThresholds, WithClock.
type Option func(*Pool)

// WithThresholds overrides the failure thresholds and cooling timings. Use
// only in tests or for tuning; the defaults are appropriate for production.
//
//	coolingFails:    consecutive failures before transition to cooling
//	quarantineFails: cumulative failures before permanent quarantine
//	baseCool:        starting cool duration (doubles per consecutive cooling cycle)
//	maxCool:         upper bound on cool duration
func WithThresholds(coolingFails, quarantineFails int, baseCool, maxCool time.Duration) Option {
	return func(p *Pool) {
		p.coolingFailThreshold = coolingFails
		p.quarantineFailThreshold = quarantineFails
		p.baseCoolDuration = baseCool
		p.maxCoolDuration = maxCool
	}
}

// WithClock injects a clock for tests. The default is realClock (time.Now()).
func WithClock(c clock) Option {
	return func(p *Pool) {
		p.clock = c
	}
}

// clock abstracts time.Now() so cooling timer transitions can be tested
// without time.Sleep. Production uses realClock; tests use a fakeClock.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
```

- [ ] **Step 4: Create `proxypool/pool_test.go` with constructor tests**

```go
package proxypool

import (
	"errors"
	"testing"
	"time"
)

func TestNew_EmptyURLsReturnsErrEmptyPool(t *testing.T) {
	_, err := New(nil)
	if !errors.Is(err, ErrEmptyPool) {
		t.Fatalf("nil urls: want ErrEmptyPool, got %v", err)
	}
	_, err = New([]string{})
	if !errors.Is(err, ErrEmptyPool) {
		t.Fatalf("empty urls: want ErrEmptyPool, got %v", err)
	}
}

func TestNew_AllEntriesStartHealthy(t *testing.T) {
	urls := []string{"http://a", "http://b", "http://c"}
	p, err := New(urls)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(p.entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(p.entries))
	}
	for i, e := range p.entries {
		if e.state != stateHealthy {
			t.Errorf("entry %d (%s): state = %s, want healthy", i, e.url, e.state)
		}
		if e.consecutiveFails != 0 || e.cumulativeFails != 0 {
			t.Errorf("entry %d: counters should be zero, got cons=%d cum=%d",
				i, e.consecutiveFails, e.cumulativeFails)
		}
	}
}

// fakeClock is a manually-advanced clock for testing cooling timers.
type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time         { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
```

- [ ] **Step 5: Run tests, verify they fail (Pool doesn't compile yet because no Acquire/Lease)**

Run: `go test ./proxypool/ -v`
Expected: BUILD or PASS — the constructor tests should pass because we have all the code. The Acquire tests we haven't written yet.

- [ ] **Step 6: Commit**

```bash
git add proxypool/state.go proxypool/pool.go proxypool/options.go proxypool/pool_test.go
git commit -m "feat(proxypool): package skeleton — Pool, entry, options, clock injection

Foundation for the health-aware proxy pool. State machine, entry struct,
functional options (WithThresholds, WithClock), and constructor tests.

No Acquire / Lease / state transitions yet — those land in subsequent commits.

Refs: docs/superpowers/plans/2026-05-20-proxy-pool-with-health-tracking.md"
```

---

### Task 2: Round-robin Acquire (no health awareness yet)

**Files:**
- Modify: `proxypool/pool.go`
- Create: `proxypool/lease.go`
- Modify: `proxypool/pool_test.go`

- [ ] **Step 1: Write the failing test for round-robin Acquire**

Append to `proxypool/pool_test.go`:

```go
func TestAcquire_RoundRobinAcrossHealthyEntries(t *testing.T) {
	urls := []string{"http://a", "http://b", "http://c"}
	p, err := New(urls)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := []string{"http://a", "http://b", "http://c", "http://a", "http://b"}
	for i, expected := range want {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("call %d: Acquire returned error: %v", i+1, err)
		}
		if lease.URL != expected {
			t.Errorf("call %d: got %q, want %q", i+1, lease.URL, expected)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./proxypool/ -run TestAcquire_RoundRobinAcrossHealthyEntries -v`
Expected: FAIL with "undefined: Acquire" or similar.

- [ ] **Step 3: Create `proxypool/lease.go` with the Lease type**

```go
package proxypool

// Lease holds a single proxy URL acquired from the Pool. The caller MUST
// call exactly one of ReportSuccess or ReportFailure on a Lease before
// discarding it — without that signal the pool cannot track health.
//
// Implementations of ReportSuccess and ReportFailure land in subsequent
// commits; the Lease type is introduced here so Acquire has a return shape.
type Lease struct {
	URL string

	pool *Pool
	e    *entry
}
```

- [ ] **Step 4: Append `Acquire()` to `proxypool/pool.go`**

```go
// Acquire returns a Lease for the next available proxy in round-robin order.
// At V1 it does NOT skip cooling/quarantined entries — that lands in Task 4.
//
// Callers MUST call exactly one of Lease.ReportSuccess or Lease.ReportFailure
// before discarding the returned Lease.
func (p *Pool) Acquire() (Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.entries)
	if n == 0 {
		return Lease{}, ErrPoolExhausted
	}

	idx := p.cursor % n
	p.cursor = (p.cursor + 1) % n
	e := p.entries[idx]
	return Lease{URL: e.url, pool: p, e: e}, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./proxypool/ -run TestAcquire_RoundRobinAcrossHealthyEntries -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add proxypool/lease.go proxypool/pool.go proxypool/pool_test.go
git commit -m "feat(proxypool): round-robin Acquire returning a Lease

Acquire walks the entries slice cyclically and returns a Lease holding the
chosen URL. Health-aware skipping (cooling/quarantined) lands in a
subsequent commit so we can build the state machine first."
```

---

### Task 3: Empty pool returns ErrPoolExhausted

This is already covered by the `n == 0` check in `Acquire`, but the contract should be locked in by a test. Edge case worth catching: a pool constructed with one URL that's later quarantined.

**Files:**
- Modify: `proxypool/pool_test.go`

- [ ] **Step 1: Write a regression test for the zero-entries-after-quarantine case**

Append to `proxypool/pool_test.go`:

```go
// TestAcquire_ErrPoolExhaustedAfterAllRemoved is a forward-compatibility
// guard: once Acquire learns to skip non-healthy entries (Task 5), the
// "all quarantined" path must return ErrPoolExhausted, not a stale URL.
// We simulate by setting state directly to bypass the state machine logic
// (covered in Task 5).
func TestAcquire_ErrPoolExhaustedAfterAllRemoved(t *testing.T) {
	p, err := New([]string{"http://a"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Manually quarantine the only entry. Lock to mirror real mutation paths.
	p.mu.Lock()
	p.entries[0].state = stateQuarantined
	p.mu.Unlock()

	_, err = p.Acquire()
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
}
```

- [ ] **Step 2: Run — expect FAIL (Acquire doesn't yet skip quarantined)**

Run: `go test ./proxypool/ -run TestAcquire_ErrPoolExhaustedAfterAllRemoved -v`
Expected: FAIL — Acquire happily returns the quarantined URL.

- [ ] **Step 3: Make Acquire skip non-healthy entries**

Replace the `Acquire` method in `proxypool/pool.go`:

```go
// Acquire returns a Lease for the next healthy proxy in round-robin order,
// skipping cooling and quarantined entries. A cooling entry whose nextOK
// has passed is treated as healthy for selection purposes (lazy promotion
// happens when the lease is reported successful — see Lease.ReportSuccess).
//
// Returns ErrPoolExhausted when no entry is available.
//
// Callers MUST call exactly one of Lease.ReportSuccess or Lease.ReportFailure
// before discarding the returned Lease.
func (p *Pool) Acquire() (Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.entries)
	if n == 0 {
		return Lease{}, ErrPoolExhausted
	}

	now := p.clock.Now()

	// Walk the ring starting at cursor; return the first usable entry.
	for i := 0; i < n; i++ {
		idx := (p.cursor + i) % n
		e := p.entries[idx]
		if p.isUsableLocked(e, now) {
			p.cursor = (idx + 1) % n
			return Lease{URL: e.url, pool: p, e: e}, nil
		}
	}
	return Lease{}, ErrPoolExhausted
}

// isUsableLocked reports whether e can be handed out for an Acquire call.
// Must be called with p.mu held.
func (p *Pool) isUsableLocked(e *entry, now time.Time) bool {
	switch e.state {
	case stateHealthy:
		return true
	case stateCooling:
		return !now.Before(e.nextOK)
	case stateQuarantined:
		return false
	default:
		return false
	}
}
```

- [ ] **Step 4: Re-run both tests**

Run: `go test ./proxypool/ -v`
Expected: PASS — round-robin still works, quarantined entry now skipped.

- [ ] **Step 5: Commit**

```bash
git add proxypool/pool.go proxypool/pool_test.go
git commit -m "feat(proxypool): Acquire skips cooling/quarantined; returns ErrPoolExhausted

Adds isUsableLocked helper that respects state and clock-based cooling
expiry. Acquire now walks the ring to find a usable entry instead of
returning whatever sits at the cursor."
```

---

## Chunk 2: Lease + Failure Reporting

> **Execution log:**
> - Task 4 → `964a8d2` (Lease.ReportSuccess + Acquire returns *Lease)
> - Tasks 5/6/7 → `becf1fe` (ReportFailure, exp-backoff coolDuration, transitionLocked, cooling-deadline reacquire, idempotency)
> - Chunk 2 review fixes → `d9ca8b1` (quarantine-guards, ordering-invariant comments, wrap-boundary tests, BlockedByTarget counter assertions)
> - **Chunk 2 ✅ production-ready** (Sonnet reviewer verified)

### Task 4: Lease.ReportSuccess clears counters

**Files:**
- Modify: `proxypool/lease.go`
- Create: `proxypool/lease_test.go`

- [ ] **Step 1: Write the failing test**

```go
package proxypool

import (
	"testing"
	"time"
)

func TestReportSuccess_ResetsConsecutiveFails(t *testing.T) {
	p, err := New([]string{"http://a"}, WithClock(newFakeClock()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Seed a failure count directly.
	p.mu.Lock()
	p.entries[0].consecutiveFails = 2
	p.mu.Unlock()

	lease, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lease.ReportSuccess()

	p.mu.Lock()
	got := p.entries[0].consecutiveFails
	successes := p.entries[0].totalSuccesses
	p.mu.Unlock()

	if got != 0 {
		t.Errorf("consecutiveFails: got %d, want 0", got)
	}
	if successes != 1 {
		t.Errorf("totalSuccesses: got %d, want 1", successes)
	}
}

func TestReportSuccess_PromotesCoolingToHealthy(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Manually put the entry into cooling with an already-expired deadline.
	p.mu.Lock()
	p.entries[0].state = stateCooling
	p.entries[0].nextOK = clk.Now().Add(-time.Second)
	p.mu.Unlock()

	lease, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lease.ReportSuccess()

	p.mu.Lock()
	got := p.entries[0].state
	p.mu.Unlock()
	if got != stateHealthy {
		t.Errorf("state after success: got %s, want healthy", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL (ReportSuccess not implemented)**

Run: `go test ./proxypool/ -run TestReportSuccess -v`
Expected: FAIL — ReportSuccess is undefined or no-op.

- [ ] **Step 3: Implement `ReportSuccess` in `proxypool/lease.go`**

```go
package proxypool

import "sync/atomic"

// Lease holds a single proxy URL acquired from the Pool. The caller MUST
// call exactly one of ReportSuccess or ReportFailure on a Lease before
// discarding it — without that signal the pool cannot track health.
//
// Calling either method more than once is a no-op (idempotent guard via
// the reported flag).
type Lease struct {
	URL string

	pool     *Pool
	e        *entry
	reported atomic.Bool
}

// ReportSuccess marks the proxy as having served a successful request. It
// resets the consecutive-failure counter and, if the entry was cooling with
// an expired deadline, promotes it back to healthy. Quarantined entries
// stay quarantined — a manual operator action (process restart) is required
// to recover them.
//
// Safe to call multiple times; subsequent calls are no-ops.
func (l *Lease) ReportSuccess() {
	if !l.reported.CompareAndSwap(false, true) {
		return
	}
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	now := l.pool.clock.Now()

	l.e.totalSuccesses++
	l.e.consecutiveFails = 0

	if l.e.state == stateCooling && !now.Before(l.e.nextOK) {
		l.e.state = stateHealthy
		l.e.lastTransitionAt = now
	}
}
```

Note: the `Lease` struct now contains `reported atomic.Bool` — Acquire returns a `Lease` (struct, not pointer). To keep `Lease.URL` ergonomic for callers (no `&` required) while preserving idempotency, we change `Lease` to be returned BY POINTER. Update `Acquire`:

- [ ] **Step 4: Change `Acquire` to return `*Lease`**

In `proxypool/pool.go`, modify the signature:

```go
func (p *Pool) Acquire() (*Lease, error) {
	// ... existing body, but return &Lease{URL: e.url, pool: p, e: e}, nil ...
}
```

And the `Lease{}` returned in the error paths becomes `nil`.

Also update existing tests in `pool_test.go` that call `lease.URL` — those still work because pointer dereference is automatic.

- [ ] **Step 5: Run all tests; verify pass**

Run: `go test ./proxypool/ -v`
Expected: PASS (all of Chunk 1 + the two new ReportSuccess tests).

- [ ] **Step 6: Commit**

```bash
git add proxypool/lease.go proxypool/pool.go proxypool/lease_test.go proxypool/pool_test.go
git commit -m "feat(proxypool): Lease.ReportSuccess promotes cooling→healthy

ReportSuccess is idempotent (atomic.Bool guard) and:
  - resets the consecutive-failure counter
  - increments totalSuccesses
  - promotes cooling entries past their nextOK deadline back to healthy
  - leaves quarantined entries quarantined (operator-only recovery)

Lease is now returned by pointer so the atomic guard field works
correctly across multiple ReportSuccess/ReportFailure calls."
```

---

### Task 5: Lease.ReportFailure increments and transitions

**Files:**
- Modify: `proxypool/lease.go`
- Modify: `proxypool/lease_test.go`

`FailureReason` and its constants were already defined in `state.go` in Task 1, so this task only adds `ReportFailure` to the Lease and the cooling/quarantine state-machine wiring.

- [ ] **Step 1: Write the failing tests**

Append to `proxypool/lease_test.go`:

```go
func TestReportFailure_BelowCoolingThresholdStaysHealthy(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 2; i++ {
		lease, _ := p.Acquire()
		lease.ReportFailure(SoftReject)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[0].state != stateHealthy {
		t.Errorf("after 2 failures (threshold=3): state = %s, want healthy", p.entries[0].state)
	}
	if p.entries[0].consecutiveFails != 2 {
		t.Errorf("consecutiveFails: got %d, want 2", p.entries[0].consecutiveFails)
	}
}

func TestReportFailure_AtThresholdCools(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 3; i++ {
		lease, _ := p.Acquire()
		lease.ReportFailure(SoftReject)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[0].state != stateCooling {
		t.Errorf("at threshold: state = %s, want cooling", p.entries[0].state)
	}
	wantNextOK := clk.Now().Add(time.Minute) // baseCool = 1 min
	if !p.entries[0].nextOK.Equal(wantNextOK) {
		t.Errorf("nextOK: got %v, want %v", p.entries[0].nextOK, wantNextOK)
	}
}

func TestReportFailure_BlockedByTargetJumpsToQuarantine(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lease, _ := p.Acquire()
	lease.ReportFailure(BlockedByTarget)

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[0].state != stateQuarantined {
		t.Errorf("BlockedByTarget: state = %s, want quarantined", p.entries[0].state)
	}
}

func TestReportFailure_CumulativeQuarantine(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Alternate failure + clock-advance + success cycles until we accumulate
	// 10 cumulative failures.
	for cycle := 0; cycle < 4; cycle++ {
		// 3 failures → cooling
		for i := 0; i < 3; i++ {
			lease, _ := p.Acquire()
			lease.ReportFailure(SoftReject)
		}
		// advance past cool deadline
		clk.Advance(time.Hour)
		// success resets consecutive but cumulative stays
		lease, err := p.Acquire()
		if err != nil {
			break // exhausted
		}
		lease.ReportSuccess()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[0].cumulativeFails < 10 {
		t.Fatalf("cumulativeFails: got %d, want ≥10", p.entries[0].cumulativeFails)
	}
	if p.entries[0].state != stateQuarantined {
		t.Errorf("after %d cumulative fails: state = %s, want quarantined",
			p.entries[0].cumulativeFails, p.entries[0].state)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

Run: `go test ./proxypool/ -run TestReportFailure -v`
Expected: FAIL — ReportFailure not implemented.

- [ ] **Step 3: Implement `ReportFailure`**

Append to `proxypool/lease.go`:

```go
// ReportFailure marks the proxy as having failed a request. The reason
// classifies the failure category and drives the state transition:
//
//	BlockedByTarget → immediate quarantine (proxy IP is burned)
//	other reasons   → increment counters; if consecutive ≥ coolingFailThreshold,
//	                  transition to cooling with exponential backoff;
//	                  if cumulative ≥ quarantineFailThreshold, quarantine
//
// Safe to call multiple times; subsequent calls are no-ops.
func (l *Lease) ReportFailure(reason FailureReason) {
	if !l.reported.CompareAndSwap(false, true) {
		return
	}
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	now := l.pool.clock.Now()

	l.e.consecutiveFails++
	l.e.cumulativeFails++
	l.e.lastFailureReason = reason

	if reason == BlockedByTarget {
		l.e.state = stateQuarantined
		l.e.lastTransitionAt = now
		return
	}

	if l.e.cumulativeFails >= int64(l.pool.quarantineFailThreshold) {
		l.e.state = stateQuarantined
		l.e.lastTransitionAt = now
		return
	}

	if l.e.consecutiveFails >= l.pool.coolingFailThreshold {
		l.e.state = stateCooling
		l.e.nextOK = now.Add(l.pool.coolDuration(l.e.consecutiveFails))
		l.e.lastTransitionAt = now
	}
}
```

- [ ] **Step 4: Implement `coolDuration` helper in `proxypool/pool.go`**

```go
// coolDuration returns the cooling-deadline offset for an entry whose
// consecutive failure count just reached the supplied value. The base
// duration doubles per cooling cycle (consecutiveFails - threshold + 1) up
// to maxCoolDuration:
//
//	threshold-th failure:     base
//	(threshold+1)-th failure: base*2
//	(threshold+2)-th failure: base*4
//	... capped at maxCool
//
// Note: consecutiveFails resets on a successful Acquire, so the doubling
// only applies within a single bad-streak.
func (p *Pool) coolDuration(consecutiveFails int) time.Duration {
	overshoot := consecutiveFails - p.coolingFailThreshold
	if overshoot < 0 {
		overshoot = 0
	}
	d := p.baseCoolDuration << overshoot
	if d <= 0 || d > p.maxCoolDuration { // shift overflow → cap
		return p.maxCoolDuration
	}
	return d
}
```

- [ ] **Step 5: Run all tests, expect PASS**

Run: `go test ./proxypool/ -v`
Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add proxypool/lease.go proxypool/pool.go proxypool/lease_test.go
git commit -m "feat(proxypool): Lease.ReportFailure with state transitions

Failures increment per-entry counters and drive transitions per the
state machine documented in state.go:
  - BlockedByTarget → immediate quarantine
  - cumulative ≥ quarantineFailThreshold → quarantine
  - consecutive ≥ coolingFailThreshold → cooling with exponential backoff

coolDuration applies bit-shift doubling capped at maxCoolDuration."
```

---

### Task 6: Cooling timer expiry → re-acquirable

**Files:**
- Modify: `proxypool/lease_test.go`

- [ ] **Step 1: Write the test**

Append:

```go
func TestAcquire_CoolingEntryReacquirableAfterDeadline(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a", "http://b"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Cool entry "a" — keep acquiring until we've reported failure on it
	// `coolingFailThreshold` times. With round-robin over 2 entries, the
	// "a" lease lands every other call, so 6 iterations gets us 3 failures.
	// We loop on a counter rather than a fixed iteration count so the test
	// is robust if the rotation order changes (e.g., cursor starts at 1).
	failuresOnA := 0
	for failuresOnA < 3 {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("unexpected Acquire error while cooling 'a': %v", err)
		}
		if lease.URL == "http://a" {
			lease.ReportFailure(SoftReject)
			failuresOnA++
		} else {
			lease.ReportSuccess()
		}
	}

	// Right after: only "b" should be acquirable.
	for i := 0; i < 5; i++ {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if lease.URL == "http://a" {
			t.Fatalf("call %d: cooling entry handed out (URL=%s)", i+1, lease.URL)
		}
		lease.ReportSuccess()
	}

	// Advance past the cooling deadline.
	clk.Advance(2 * time.Minute)

	// Now "a" should come back into rotation. Try up to 10 acquires —
	// must see "a" at least once.
	saw := false
	for i := 0; i < 10; i++ {
		lease, _ := p.Acquire()
		if lease.URL == "http://a" {
			saw = true
		}
		lease.ReportSuccess()
	}
	if !saw {
		t.Fatal("cooling entry never reacquired after clock advance past deadline")
	}
}
```

- [ ] **Step 2: Run — should PASS already (Acquire skips via isUsableLocked which respects nextOK)**

Run: `go test ./proxypool/ -run TestAcquire_CoolingEntryReacquirable -v`
Expected: PASS.

- [ ] **Step 3: Commit (test-only commit; locks in regression coverage)**

```bash
git add proxypool/lease_test.go
git commit -m "test(proxypool): lock in cooling→healthy reacquire-after-deadline"
```

---

### Task 7: Lease idempotency

**Files:**
- Modify: `proxypool/lease_test.go`

- [ ] **Step 1: Write tests**

```go
func TestLease_ReportSuccessTwiceIsNoOp(t *testing.T) {
	p, _ := New([]string{"http://a"}, WithClock(newFakeClock()))
	lease, _ := p.Acquire()
	lease.ReportSuccess()
	lease.ReportSuccess()

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[0].totalSuccesses != 1 {
		t.Errorf("double-report counted: totalSuccesses = %d, want 1",
			p.entries[0].totalSuccesses)
	}
}

func TestLease_ReportFailureThenSuccessIsNoOp(t *testing.T) {
	p, _ := New([]string{"http://a"}, WithClock(newFakeClock()))
	lease, _ := p.Acquire()
	lease.ReportFailure(SoftReject)
	lease.ReportSuccess() // must be ignored

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[0].consecutiveFails != 1 {
		t.Errorf("ReportSuccess after Failure mutated counters: consecutiveFails = %d, want 1",
			p.entries[0].consecutiveFails)
	}
	if p.entries[0].totalSuccesses != 0 {
		t.Errorf("ReportSuccess after Failure was not ignored: totalSuccesses = %d, want 0",
			p.entries[0].totalSuccesses)
	}
}
```

- [ ] **Step 2: Run — expect PASS (idempotency already implemented via atomic.Bool)**

Run: `go test ./proxypool/ -run TestLease_ -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add proxypool/lease_test.go
git commit -m "test(proxypool): lock in Lease report idempotency"
```

---

## Chunk 3: Stats, hostOf consolidation, Concurrency

> **Execution log:**
> - Task 8 → `e49ffc7` (Stats() + exported HostOf + json omitzero on time.Time)
> - Task 8b → `6f43b56` (deleted gmaps + webrunner proxyHostForLog dupes; callers use cmp.Or(proxypool.HostOf, "direct"); fixed pre-existing webrunner doc-comment-orphan)
> - Task 9 → no-op (persistence seam was already documented in Task 1)
> - Task 10 → `425bf39` (concurrent + burnout + full-exhaustion tests; 22 tests total under -race)
> - Chunk 3 review polish → `840841f` (clarifying comments + TestStats_ConcurrentWithReports — 23 tests pass under -race)
> - **Chunk 3 ✅ production-ready** (Sonnet reviewer verified)

### Task 8: Stats() snapshot

**Files:**
- Create: `proxypool/stats.go`
- Modify: `proxypool/pool.go`
- Create: `proxypool/stats_test.go`

- [ ] **Step 1: Define `Stats` type in `proxypool/stats.go`**

```go
package proxypool

import "time"

// Stats is a read-only point-in-time snapshot of the pool. Returned by
// Pool.Stats; serialized for the /internal/proxy/stats HTTP endpoint.
type Stats struct {
	TotalProxies int           `json:"total_proxies"`
	Healthy      int           `json:"healthy"`
	Cooling      int           `json:"cooling"`
	Quarantined  int           `json:"quarantined"`
	Entries      []EntryStats  `json:"entries"`
}

// EntryStats is the per-proxy view inside Stats. Host returns the host:port
// (credential-stripped) so the snapshot is safe to log or serve over an
// internal HTTP endpoint without leaking auth.
type EntryStats struct {
	Host              string    `json:"host"`
	State             string    `json:"state"`
	NextOK            time.Time `json:"next_ok,omitempty"`
	ConsecutiveFails  int       `json:"consecutive_fails"`
	CumulativeFails   int64     `json:"cumulative_fails"`
	TotalSuccesses    int64     `json:"total_successes"`
	LastFailureReason string    `json:"last_failure_reason,omitempty"`
	LastTransitionAt  time.Time `json:"last_transition_at"`
}
```

- [ ] **Step 2: Add `Stats()` method to `proxypool/pool.go`**

```go
// Stats returns a snapshot of the current pool state. Safe to call
// concurrently with Acquire/Report — the snapshot is copy-on-read.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()

	s := Stats{
		TotalProxies: len(p.entries),
		Entries:      make([]EntryStats, 0, len(p.entries)),
	}
	for _, e := range p.entries {
		switch e.state {
		case stateHealthy:
			s.Healthy++
		case stateCooling:
			s.Cooling++
		case stateQuarantined:
			s.Quarantined++
		}
		es := EntryStats{
			Host:             HostOf(e.url),
			State:            e.state.String(),
			ConsecutiveFails: e.consecutiveFails,
			CumulativeFails:  e.cumulativeFails,
			TotalSuccesses:   e.totalSuccesses,
			LastTransitionAt: e.lastTransitionAt,
		}
		if e.state == stateCooling {
			es.NextOK = e.nextOK
		}
		if e.cumulativeFails > 0 {
			es.LastFailureReason = e.lastFailureReason.String()
		}
		s.Entries = append(s.Entries, es)
	}
	return s
}

// HostOf returns the host:port from a proxy URL with any userinfo
// (user:password@) stripped. Returns "" for an empty input and "invalid"
// for an unparseable URL.
//
// Exported as the single source of truth for credential-free proxy logging.
// gmaps/reviews.go and runner/webrunner/webrunner.go each previously held
// near-identical copies; both now import this — see Task 8b.
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
```

Add `"net/url"` to the import block in `pool.go`.

Update the `EntryStats.Host` population in `Stats()` to call `HostOf(e.url)` instead of `hostOf(e.url)` (capitalized — it's now exported).

- [ ] **Step 3: Write the test**

Create `proxypool/stats_test.go`:

```go
package proxypool

import (
	"strings"
	"testing"
	"time"
)

func TestStats_ReflectsLiveState(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://user:pw@a:1", "http://user:pw@b:2", "http://user:pw@c:3"},
		WithClock(clk),
		WithThresholds(2, 5, time.Minute, 30*time.Minute),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Cool "a" via 2 failures.
	for i := 0; i < 2; i++ {
		l, _ := p.Acquire()
		if l.URL == "http://user:pw@a:1" {
			l.ReportFailure(SoftReject)
		} else {
			l.ReportSuccess()
		}
	}

	// Quarantine "b" via BlockedByTarget.
	for {
		l, _ := p.Acquire()
		if l.URL == "http://user:pw@b:2" {
			l.ReportFailure(BlockedByTarget)
			break
		}
		l.ReportSuccess()
	}

	s := p.Stats()
	if s.TotalProxies != 3 {
		t.Errorf("TotalProxies: got %d, want 3", s.TotalProxies)
	}
	if s.Cooling != 1 || s.Quarantined != 1 || s.Healthy != 1 {
		t.Errorf("state counts: healthy=%d cooling=%d quarantined=%d; want 1/1/1",
			s.Healthy, s.Cooling, s.Quarantined)
	}

	// Credential stripping in Host: the URL form "user:pass@host:port"
	// becomes "host:port" — the '@' separator is the unambiguous signal
	// that userinfo survived. ':' alone is fine (it's the port separator).
	for _, e := range s.Entries {
		if e.Host == "" {
			t.Errorf("Host empty for entry: %+v", e)
		}
		if strings.Contains(e.Host, "@") {
			t.Errorf("Host %q leaked credentials (contains @)", e.Host)
		}
	}
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./proxypool/ -v`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add proxypool/stats.go proxypool/stats_test.go proxypool/pool.go
git commit -m "feat(proxypool): Stats snapshot for observability

Read-only point-in-time view of the pool. Host strings are
credential-stripped via the exported HostOf helper, safe to serve from
the /internal/proxy/stats endpoint without leaking proxy passwords."
```

---

### Task 8b: Consolidate hostOf duplicates onto proxypool.HostOf

Two near-identical copies exist in `gmaps/reviews.go` (`proxyHostForLog`) and `runner/webrunner/webrunner.go` (`proxyHostForLog`). Both predate this package. Delete both, route all callers through `proxypool.HostOf`. Net: ~20 lines removed across the repo.

**Files:**
- Modify: `gmaps/reviews.go`
- Modify: `runner/webrunner/webrunner.go`

- [ ] **Step 1: Delete `proxyHostForLog` from `gmaps/reviews.go`**

Remove the function definition. Update every call site (`proxyHostForLog(f.params.proxyURL)`) to `proxypool.HostOf(f.params.proxyURL)`. Add the import `"github.com/gosom/google-maps-scraper/proxypool"` to `gmaps/reviews.go`.

Quick sanity: `gmaps` already depends on nothing from `proxypool` in production code, so the import is acyclic. `proxypool` has zero gmaps imports. Verified one-way dependency.

- [ ] **Step 2: Delete `proxyHostForLog` from `runner/webrunner/webrunner.go`**

Same shape. Replace call sites with `proxypool.HostOf(...)`. The `proxypool` import is already added in Task 12.

- [ ] **Step 3: Verify nothing in `gmaps/reviews_proxy_test.go` references the old name**

The test file had a `TestProxyHostForLog` calling the package-private `proxyHostForLog`. Rename to test the new exported function — but since `HostOf` is in a different package now, the test should move too OR be deleted (its replacement is `TestHostOf` in `proxypool/stats_test.go` from Task 8). Delete the gmaps copy; keep the proxypool copy as the canonical test.

- [ ] **Step 4: Build + run all tests**

Run: `go build ./... && go test ./gmaps/ ./runner/... ./proxypool/ -count=1`
Expected: all green. Tests that referenced the deleted name will surface if missed.

- [ ] **Step 5: Commit**

```bash
git add gmaps/reviews.go runner/webrunner/webrunner.go gmaps/reviews_proxy_test.go
git commit -m "refactor: route proxy-host logging through proxypool.HostOf

Deletes two near-identical proxyHostForLog copies (gmaps + webrunner)
in favor of the single exported proxypool.HostOf. ~20 lines removed."
```

---

### Task 9: Persistence — explicitly NOT in V1

The first draft of this plan defined a `Repository` interface + a `MemoryRepository` no-op so a future Postgres implementation could slot in. On review, that was YAGNI: ~50 lines of code that nothing actually calls in V1. An unused interface is worse than no interface — it lies about the API surface and adds maintenance overhead with no live behavior to verify.

**Decision:** ship V1 with zero persistence code. When the trigger conditions fire (multi-process webrunner, historical-event queries needed, etc.) write the persistence layer as a focused follow-up PR — at that point we'll know exactly what shape it needs, instead of guessing now.

**What to do instead in this task:**

- [ ] **Step 1: Add a single docstring to `proxypool/pool.go` documenting the future seam**

Append to the package-level doc comment in `pool.go` (or to the `Pool` type doc):

```go
// Persistence is intentionally out of scope for V1. Pool state lives in
// memory only and resets on process restart. When the triggers in
// docs/superpowers/plans/2026-05-20-proxy-pool-with-health-tracking.md
// fire, persistence will land as a follow-up: a `Repository` interface
// added at that time, called once on New() (Load) and on shutdown (Save).
// Anchoring the seam in a docstring rather than an unused interface
// keeps the V1 surface honest.
```

- [ ] **Step 2: Commit (docs-only, no code change)**

```bash
git add proxypool/pool.go
git commit -m "docs(proxypool): document persistence seam without shipping it

YAGNI on a Repository interface. Future Postgres persistence is anchored
in a comment now; the interface lands when we actually have a second
implementation to validate the contract against."
```

---

### Task 10: Concurrent Acquire safety

**Files:**
- Create: `proxypool/concurrency_test.go`

- [ ] **Step 1: Write the contention test**

```go
package proxypool

import (
	"sync"
	"testing"
	"time"
)

// TestAcquire_ConcurrentCallersDoNotPanicOrDeadlock fires N goroutines that
// each Acquire+ReportSuccess in a tight loop. With go test -race, mutex
// violations or unsafe entry mutations would surface here.
func TestAcquire_ConcurrentCallersDoNotPanicOrDeadlock(t *testing.T) {
	const (
		nGoroutines = 32
		nIterations = 200
	)
	p, err := New([]string{"http://a", "http://b", "http://c"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(nGoroutines)
	for g := 0; g < nGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < nIterations; i++ {
				l, err := p.Acquire()
				if err != nil {
					// Acceptable if every entry happens to be cooling at the
					// same instant; very unlikely with healthy entries.
					continue
				}
				if i%5 == 0 {
					l.ReportFailure(SoftReject)
				} else {
					l.ReportSuccess()
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: goroutines did not finish within 10s")
	}

	// Sanity: cumulative bookkeeping must add up.
	s := p.Stats()
	var totalOps int64
	for _, e := range s.Entries {
		totalOps += e.TotalSuccesses + e.CumulativeFails
	}
	if totalOps == 0 {
		t.Fatal("no operations recorded despite 32×200 iterations")
	}
}
```

- [ ] **Step 2: Run with race detector**

Run: `go test ./proxypool/ -race -run TestAcquire_Concurrent -v`
Expected: PASS, no race reports.

- [ ] **Step 3: Commit**

```bash
git add proxypool/concurrency_test.go
git commit -m "test(proxypool): concurrent Acquire + Report under -race

Locks in the mutex contract — 32 goroutines × 200 iterations must not
deadlock or race. Catches future refactors that drop the lock or share
entry mutations across the lock boundary."
```

---

## Chunk 4: Webrunner Integration

> **Execution log:**
> - Task 11 → `4ebcd12` (export ReviewEmptyCount + ReviewCircuitBreakerThreshold from gmaps)
> - Tasks 12/13 → `58fe8ac` (proxypool.Pool wired into scrapeJob; panic-safe lease reporting via defer; classifyProxyOutcome helper; CauseProxyPoolExhausted)
> - Task 14 → `e2b1daf` (/internal/proxy/stats HTTP handler via web.ServerConfig.InternalHandlers extension point; 503 on nil pool; credential-stripped JSON)
> - Task 15 → `a3dd2b4` (two end-to-end tests: 50-scrape mixed-outcome simulation + cooled-pool recovery)
> - Chunk 4 review fixes → `ce90c62` (mateErr shadowing on forcedCompletionCh path FIXED — was a real classification bug; proxyAttempted gate; double-reset cleanup; classifyProxyOutcome default → NetworkErr; InternalHandlers uniqueness comment; cross-job reviewEmptyCount race documented as known limitation, follow-up tracked)
> - **Chunk 4 ✅ production-ready** (Sonnet reviewer verified)
>
> **Master code review** (PR #83, 5 parallel Sonnet reviewers via superpowers:code-review skill):
> - Master review fixes → `37ef9ac` — TWO critical bugs caught and fixed:
>   - `classifyProxyOutcome` was treating `context.Canceled` from `mate.Start`'s natural success-path termination as a job error, which would have cooled every healthy proxy after 3 successful scrapes. Mirrors the same disambiguation `classifyOutcome` already does via `naturalCompletion`.
>   - `newCookieFetchClient` wrapped `*url.Error` which embeds the full proxy URL including userinfo — credential leak into Loki on any malformed URL.
> - **Master review ✅ production-ready** (only 2 of 14 findings met 80+ confidence; both fixed)

### Task 11: Expose ReviewEmptyCount from gmaps

The webrunner needs a way to ask, at job end, "did the review circuit breaker trip during this job?" — that's the signal for classifying the proxy outcome as `SoftReject`. The package-level `reviewEmptyCount` already exists; we just need an exported reader.

**Files:**
- Modify: `gmaps/place.go`
- Create: `gmaps/review_circuit_state_test.go`

- [ ] **Step 1: Write the test**

```go
package gmaps

import "testing"

func TestReviewEmptyCount_ReflectsAtomic(t *testing.T) {
	ResetReviewCircuitBreaker()
	if got := ReviewEmptyCount(); got != 0 {
		t.Fatalf("after reset: got %d, want 0", got)
	}
	reviewEmptyCount.Add(2)
	if got := ReviewEmptyCount(); got != 2 {
		t.Errorf("after Add(2): got %d, want 2", got)
	}
	ResetReviewCircuitBreaker()
	if got := ReviewEmptyCount(); got != 0 {
		t.Errorf("after second reset: got %d, want 0", got)
	}
}
```

- [ ] **Step 2: Add the exported reader to `gmaps/place.go`**

After the existing `ResetReviewCircuitBreaker` function (around line 48):

```go
// ReviewEmptyCount returns the current value of the per-job review
// empty-response counter. Used by the webrunner at job end to decide
// whether to classify the assigned proxy as SoftReject — see
// docs/superpowers/plans/2026-05-20-proxy-pool-with-health-tracking.md.
//
// Callers should read this only AFTER mate.Start has returned; reading
// during a scrape gives a racy mid-scrape view.
func ReviewEmptyCount() int32 {
	return reviewEmptyCount.Load()
}
```

- [ ] **Step 3: Run, commit**

Run: `go test ./gmaps/ -run TestReviewEmptyCount -v`
Expected: PASS.

```bash
git add gmaps/place.go gmaps/review_circuit_state_test.go
git commit -m "feat(gmaps): export ReviewEmptyCount for proxy-pool outcome classification

The webrunner reads this at job end to decide whether to report the
assigned proxy as SoftReject (≥ threshold empties = Google rejecting
the proxy IP) or NetworkErr / Success."
```

---

### Task 12: Replace pickProxyURL with proxypool.Pool

**Files:**
- Modify: `runner/webrunner/failure_reason.go` (first — defines the constant the next steps reference)
- Modify: `runner/webrunner/webrunner.go`

- [ ] **Step 1: Add `CauseProxyPoolExhausted` failure reason**

Define the constant BEFORE any code references it, so the package compiles after each step.

In `runner/webrunner/failure_reason.go`, add a new cause to the existing block:

```go
// CauseProxyPoolExhausted is set when proxypool.Pool.Acquire returns
// ErrPoolExhausted — every proxy is cooling or quarantined and the
// scrape cannot proceed. Operator-visible: indicates the entire pool
// has been burned by the target (typically Google detecting datacenter
// IPs across the whole Decodo allocation).
CauseProxyPoolExhausted Cause = "proxy_pool_exhausted"
```

Add the user-facing message in the same file's `UserMessage` (or equivalent) switch:

```go
case CauseProxyPoolExhausted:
	return "Scraping aborted: every configured proxy was rejected by Google. Pool needs new IPs."
```

Verify the package still builds before continuing: `go build ./runner/webrunner/...`

- [ ] **Step 2: Add the import and the pool field on `webrunner`**

In `runner/webrunner/webrunner.go`, add to the imports:

```go
"github.com/gosom/google-maps-scraper/proxypool"
```

Find the `webrunner` struct (around line 100) and add a field:

```go
type webrunner struct {
	// ... existing fields ...
	proxyPool *proxypool.Pool
}
```

- [ ] **Step 3: Initialize `proxyPool` in `New()`**

Find the `New` function (line 203). After the `proxyURLs` field is populated (around line 422):

```go
if len(cfg.Proxy.Proxies) > 0 {
	pool, err := proxypool.New(cfg.Proxy.Proxies)
	if err != nil {
		return nil, fmt.Errorf("proxypool.New: %w", err)
	}
	w.proxyPool = pool
}
```

- [ ] **Step 4: Replace the `pickProxyURL` call in `scrapeJob`**

Find the `jobProxy := w.pickProxyURL()` line. Replace the immediate surrounding block (the comment + the call + the pass to setupMate / SeedJobConfig):

```go
// Acquire a Lease from the proxy pool. The same URL feeds both
// scrapemate (setupMate → WithProxies) and the seed jobs
// (SeedJobConfig.ProxyURL → fetchWithCookies), so the entire scrape
// shares one upstream identity.
//
// At job end we report Success / Failure(...) on the Lease — see the
// classification block after mate.Start returns.
var (
	proxyLease *proxypool.Lease
	jobProxy   proxyAssignment
)
if w.proxyPool != nil {
	lease, lerr := w.proxyPool.Acquire()
	if lerr != nil {
		outcome = OutcomeFailed(CauseProxyPoolExhausted, "all proxies quarantined", lerr)
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		w.logger.Error("proxy_pool_exhausted",
			slog.String("job_id", job.ID),
			slog.String("user_id", job.UserID),
		)
		return outcome
	}
	proxyLease = lease
	jobProxy = proxyAssignment{URL: lease.URL}
}
```

Note on `proxyAssignment.Index` / `PoolSize`: the pool skips cooling/quarantined entries, so positional index is no longer meaningful — those fields will be deprecated. For now, leave them zero — Step 5 drops them from the `proxy_assigned` log so they don't surface.

- [ ] **Step 5: Adapt `proxyAssignment` log fields**

Adjust `setupMate`'s `proxy_assigned` log to drop `index` / `of` (the pool's round-robin cursor is internal) and switch to fields that ARE meaningful:

```go
if proxy.URL != "" {
	opts = append(opts, scrapemateapp.WithProxies([]string{proxy.URL}))
	w.logger.Debug("proxy_assigned",
		slog.String("job_id", job.ID),
		slog.String("proxy_host", proxypool.HostOf(proxy.URL)),
	)
}
```

Note: this DOES drop the `index` / `of` fields restored in PR #82. Document in the commit message that the pool's selection is round-robin over healthy entries (not deterministic positions), so the index field is no longer meaningful — replaced by `proxy_host` as the queryable identifier.

- [ ] **Step 6: Build, run unit tests**

Run: `go build ./... && go test ./runner/... ./gmaps/ ./proxypool/ -count=1`
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add runner/webrunner/webrunner.go runner/webrunner/failure_reason.go
git commit -m "feat(webrunner): integrate proxypool.Pool — replaces pickProxyURL

Pool.Acquire returns a Lease; the lease URL feeds both scrapemate and the
SeedJobConfig (same path as before). ErrPoolExhausted maps to a new
failure cause (proxy_pool_exhausted) with an operator-visible message.

Note: drops the index/of fields from the proxy_assigned debug log — the
pool's selection skips cooling/quarantined entries, so a positional index
is no longer meaningful. proxy_host remains as the queryable identifier."
```

---

### Task 13: Classify outcome → Lease report

**Files:**
- Modify: `runner/webrunner/webrunner.go`

- [ ] **Step 1: Add classification helper**

After `setupMate`, add a small classification helper:

```go
// classifyProxyOutcome maps a job's terminal state into a proxypool
// FailureReason, or returns ok=false if the job should be reported as a
// success on its proxy lease.
//
// Today's classification is intentionally coarse — we infer the reason
// from job-level signals because scrapemate does not surface per-request
// proxy attribution. Refinements should add more signals here rather
// than push complexity into the pool.
func classifyProxyOutcome(jobSuccess bool, jobErr error, reviewCircuitTripped bool) (reason proxypool.FailureReason, report bool) {
	switch {
	case !jobSuccess || jobErr != nil:
		return proxypool.NetworkErr, true
	case reviewCircuitTripped:
		// Cookies + proxy combination got the 33-byte stub repeatedly
		// → Google rejected this proxy IP for the cookie-authenticated
		// review-RPC endpoint. Classify as SoftReject so the pool cools
		// rather than quarantines (the IP may recover).
		return proxypool.SoftReject, true
	default:
		return 0, false
	}
}
```

- [ ] **Step 2: Install panic-safe lease reporting via `defer` at the top of `scrapeJob`**

The lease MUST be reported on every exit path — including panics and early returns — or the proxy entry stays at its current state and a bad proxy keeps getting reused. Place this defer block immediately after the `proxyLease` is acquired in Task 12 (before any work that could panic):

```go
// Ensure the lease is reported on every exit path. The atomic-Bool guard
// inside the Lease makes double-reporting safe, so it's fine to also call
// ReportSuccess/Failure explicitly below — only the FIRST call wins.
//
// On panic: classify as NetworkErr (most conservative) and re-raise so
// the surrounding recover/observability still runs.
var (
	jobSuccess           bool
	mateErr              error
	reviewCircuitTripped bool
)
defer func() {
	if proxyLease == nil {
		return
	}
	if r := recover(); r != nil {
		proxyLease.ReportFailure(proxypool.NetworkErr)
		w.logger.Error("proxy_lease_reported_on_panic",
			slog.String("job_id", job.ID),
			slog.String("proxy_host", proxypool.HostOf(proxyLease.URL)),
			slog.Any("panic", r),
		)
		panic(r) // re-raise so existing recover wrappers handle it
	}
	reason, fail := classifyProxyOutcome(jobSuccess, mateErr, reviewCircuitTripped)
	if fail {
		proxyLease.ReportFailure(reason)
		w.logger.Info("proxy_outcome_reported",
			slog.String("job_id", job.ID),
			slog.String("proxy_host", proxypool.HostOf(proxyLease.URL)),
			slog.String("outcome", "failure"),
			slog.String("reason", reason.String()),
		)
	} else {
		proxyLease.ReportSuccess()
		w.logger.Info("proxy_outcome_reported",
			slog.String("job_id", job.ID),
			slog.String("proxy_host", proxypool.HostOf(proxyLease.URL)),
			slog.String("outcome", "success"),
		)
	}
}()
```

The closure captures `jobSuccess`, `mateErr`, `reviewCircuitTripped` by reference — populate them inline as the function progresses. Set `reviewCircuitTripped = gmaps.ReviewEmptyCount() >= gmaps.ReviewCircuitBreakerThreshold()` right after `mate.Start` returns (it's read AFTER scrapemate is done; reading mid-scrape is racy).

This replaces the inline reporting block that was originally placed after `mate.Start` returned — the defer is the only place that knows about every exit path.

- [ ] **Step 3: Export the threshold from gmaps**

In `gmaps/place.go`:

```go
// ReviewCircuitBreakerThreshold returns the empty-response count that
// trips the review circuit breaker. Stable across the process lifetime.
func ReviewCircuitBreakerThreshold() int32 {
	return reviewCircuitBreakerThreshold
}
```

In the webrunner's `scrapeJob`, immediately after `mate.Start` returns (and before any return statement so the defer captures the right value):

```go
reviewCircuitTripped = gmaps.ReviewEmptyCount() >= gmaps.ReviewCircuitBreakerThreshold()
```

This populates the closure variable read by the defer block in Step 2.

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./gmaps/ ./runner/... ./proxypool/ -count=1`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add gmaps/place.go runner/webrunner/webrunner.go
git commit -m "feat(webrunner): classify job outcome → proxypool Lease report

After mate.Start returns, the webrunner reports the proxy lease as
success or failure based on job error + review circuit breaker state.
SoftReject (cookies + proxy combination triggered the 33-byte stub
threshold) cools the proxy; NetworkErr (job error / panic) cools more
aggressively via the consecutive-failure counter."
```

---

### Task 14: /internal/proxy/stats HTTP endpoint

**Files:**
- Modify: `runner/webrunner/webrunner.go` (or wherever the internal server is wired)

- [ ] **Step 1: Locate the internal server mux**

Run: `grep -n "internal_server_started\|internal.*Mux\|9090" runner/webrunner/*.go`

You should see the internal listener registered (probably around `internal_server_started` log line, port 9090). Add a handler there.

- [ ] **Step 2: Add the handler as a method on `*webrunner`**

Extracting to a method avoids the `w_`/`w` shadowing footgun and makes the handler unit-testable in isolation.

In `runner/webrunner/webrunner.go`:

```go
// handleProxyStats serves a JSON snapshot of proxypool.Stats. Bound to
// the internal listener (127.0.0.1:9090). Returns 503 when no proxy pool
// is configured (e.g., CLI mode with no PROXIES env). Never includes
// credentials in the response — HostOf strips userinfo.
func (w *webrunner) handleProxyStats(rw http.ResponseWriter, _ *http.Request) {
	if w.proxyPool == nil {
		http.Error(rw, "proxy pool not configured", http.StatusServiceUnavailable)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(rw).Encode(w.proxyPool.Stats()); err != nil {
		w.logger.Error("proxy_stats_encode_failed", slog.Any("error", err))
	}
}
```

Register it on the internal mux:

```go
internalMux.HandleFunc("/internal/proxy/stats", w.handleProxyStats)
```

Add `"encoding/json"` and `"net/http"` to imports if not already there.

- [ ] **Step 3: Smoke test by hand (optional)**

Locally, with the backend running:
```
curl -s http://localhost:9090/internal/proxy/stats | jq
```

Expected: JSON with `total_proxies`, `healthy`, `cooling`, `quarantined`, and a per-entry array.

- [ ] **Step 4: Commit**

```bash
git add runner/webrunner/webrunner.go
git commit -m "feat(webrunner): /internal/proxy/stats endpoint exposing Pool snapshot

Returns the proxypool.Stats JSON serialization. Credential-stripped via
proxypool.HostOf; safe to call from internal ops tooling.

Bound to the internal listener (port 9090) — not exposed publicly."
```

---

### Task 15: End-to-end integration test

**Files:**
- Create: `proxypool/integration_test.go`

The pool unit tests cover the state machine; this test simulates a realistic burnout scenario (one bad proxy, two good, pool degrades gracefully).

- [ ] **Step 1: Write the integration test**

```go
package proxypool

import (
	"testing"
	"time"
)

// TestPool_BurnoutScenario simulates the failure mode we shipped this
// system to handle: one of three proxies returns the 33-byte stub
// (SoftReject) repeatedly. The pool should cool it after 3 strikes, leave
// it cool for the backoff window, and never permanently quarantine it
// until cumulative threshold is hit. Meanwhile, the two healthy proxies
// keep serving traffic.
func TestPool_BurnoutScenario(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://good-1", "http://bad", "http://good-2"},
		WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Simulate 30 jobs. The "bad" proxy fails with SoftReject;
	// "good-1" and "good-2" succeed.
	for i := 0; i < 30; i++ {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("iter %d: Acquire: %v", i, err)
		}
		if lease.URL == "http://bad" {
			lease.ReportFailure(SoftReject)
		} else {
			lease.ReportSuccess()
		}
		// Advance time slightly so cooling windows can expire.
		clk.Advance(5 * time.Second)
	}

	s := p.Stats()
	t.Logf("final stats: healthy=%d cooling=%d quarantined=%d",
		s.Healthy, s.Cooling, s.Quarantined)

	// "bad" entry must NOT be healthy.
	for _, e := range s.Entries {
		if e.Host == "bad" && e.State == "healthy" {
			t.Errorf("bad proxy still healthy after %d SoftReject reports", e.ConsecutiveFails)
		}
	}

	// The two good proxies must both be healthy and have served traffic.
	healthyGoods := 0
	for _, e := range s.Entries {
		if (e.Host == "good-1" || e.Host == "good-2") && e.State == "healthy" && e.TotalSuccesses > 0 {
			healthyGoods++
		}
	}
	if healthyGoods != 2 {
		t.Errorf("good proxies serving: got %d, want 2", healthyGoods)
	}
}

// TestPool_FullBurnoutReturnsErrPoolExhausted simulates the worst case:
// every proxy is bad. The pool should eventually return ErrPoolExhausted
// rather than spinning forever or returning a quarantined URL.
func TestPool_FullBurnoutReturnsErrPoolExhausted(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://a", "http://b"},
		WithClock(clk),
		WithThresholds(2, 4, time.Hour, time.Hour),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Burn both proxies past their cumulative-quarantine threshold.
	// 2 entries × 4 failures each = 8 attempts to fail.
	for i := 0; i < 8; i++ {
		lease, err := p.Acquire()
		if err != nil {
			break
		}
		lease.ReportFailure(SoftReject)
	}

	_, err = p.Acquire()
	if err == nil {
		t.Fatal("expected ErrPoolExhausted after every entry quarantined")
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./proxypool/ -run TestPool_ -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add proxypool/integration_test.go
git commit -m "test(proxypool): burnout + full-pool-exhaustion integration tests

Two realistic scenarios:
  - One bad proxy among healthy ones: cools out, others keep serving
  - All proxies bad: pool exhausts and returns ErrPoolExhausted"
```

---

## Chunk 5: Final Wiring, Verification, PR

> **Execution log:**
> - PR opened: https://github.com/brezel-ai/brezelscraper-backend/pull/83 (`feat/proxy-pool-with-health-tracking` → `develop`)
> - Smoke-test runbook → `docs/observability/proxy-pool-smoke-test.md`
> - **Chunk 5 ✅ ready** — PR open, master review passed, smoke runbook documented; awaiting operator-run smoke test before merge

### Task 16: End-to-end smoke test (manual)

**Pre-merge verification (manual, not a unit test):**

- [ ] **Step 1: Run a small scrape locally with two configured proxies (one valid, one bogus)**

In `.env` or runtime:
```
PROXIES=http://VALID:PASS@gate.decodo.com:10001,http://invalid:invalid@1.2.3.4:80
```

Start the backend:
```
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" ./tmp/server -web
```

Submit a small job from the frontend. Watch logs for:
- `proxy_outcome_reported` (success on the valid proxy, NetworkErr on the bogus one)
- `proxy_cooling` / `proxy_quarantined` if you crank up the iterations

Hit `curl localhost:9090/internal/proxy/stats | jq` and confirm:
- `total_proxies: 2`
- `healthy: 1`, `cooling` or `quarantined: 1` (depending on how many times the bad one was tried)
- Per-entry `Host` is the bare `host:port` (no creds)

- [ ] **Step 2: Run the race-detector on the full proxypool suite**

```
go test ./proxypool/ -race -count=1 -v
```

Expected: all green.

### Task 17: PR

- [ ] **Step 1: Push the branch**

```bash
git push -u origin feat/proxy-pool-with-health-tracking
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --repo brezel-ai/brezelscraper-backend --base develop \
  --title "feat(proxypool): health-aware proxy pool with quarantine + observability" \
  --body "$(cat <<'EOF'
## Summary

Introduces \`proxypool.Pool\` — a health-aware HTTP proxy pool that:

- Quarantines proxies after repeated failures (\`SoftReject\` / \`NetworkErr\` / \`ProxyErr\` cool, \`BlockedByTarget\` quarantines immediately)
- Returns \`ErrPoolExhausted\` when every proxy is unavailable — surfaced as a first-class \`CauseProxyPoolExhausted\` job outcome
- Exposes per-proxy state via \`/internal/proxy/stats\` (credential-stripped)
- Is engine-agnostic (no scrapemate import) — survives a future scrapemate→C++ engine swap

Storage decision: **in-memory for V1**. Persistence (Postgres-backed Repository) is deferred until the trigger conditions in the plan doc fire; the seam is documented but the interface is not pre-defined.

Plan: \`docs/superpowers/plans/2026-05-20-proxy-pool-with-health-tracking.md\`

## Test plan

- [ ] proxypool unit tests pass (\`go test ./proxypool/ -race\`)
- [ ] webrunner tests pass (\`go test ./runner/...\`)
- [ ] Manual smoke: one valid + one bogus proxy → bogus quarantines, valid keeps serving
- [ ] \`/internal/proxy/stats\` returns sane JSON with no credentials in \`host\` fields
- [ ] Log events appear: \`proxy_outcome_reported\`, \`proxy_pool_exhausted\` (when applicable)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Verify PR mergeable**

```
gh pr view <number> --repo brezel-ai/brezelscraper-backend --json mergeable,mergeStateStatus
```

Expected: `MERGEABLE`.

---

## Open Follow-ups (NOT in this PR)

1. **Postgres-backed persistence**: At the point a real persistent implementation is needed, introduce the `Repository` interface and the Postgres impl in the SAME follow-up PR — that way the interface is validated against a real second implementation from the start instead of shipping speculatively. Trigger: more than one webrunner process running concurrently, OR ops needs historical proxy ban events queryable via SQL.
2. **Per-request proxy attribution**: Today's classification is job-level. Per-request needs forking scrapemate or a custom HTTP middleware between scrapemate and the proxy. Defer until the v2 engine work.
3. **Adaptive backoff tuning**: The current `2^n` exponential is hardcoded. Make it tunable via env / config once we have ops data on what cool durations actually recover.
4. **Engine interface (`scrapeengine.Engine`)**: A separate ~150 LoC piece of work. The Pool stays unchanged when this lands. See the "Phase 2" section of the architectural read.
5. **Webhook circuit breaker pattern lift**: `postgres/webhook.go:276-336` has a robust idempotent state machine that the V2 Postgres-backed pool should mirror. Reuse, don't reinvent.

6. **Per-job review-empty counter (replaces process-global `gmaps.reviewEmptyCount`)**: Under `max_concurrent_jobs > 1`, the global counter is shared across simultaneous scrapes. A concurrent scrape that trips its own breaker drives THIS scrape's `ReviewEmptyCount()` read past threshold and the proxy assigned here gets a spurious SoftReject. Pre-this-PR the same race caused spurious mid-scrape review-skip; this PR propagates the consequence to proxy health, which is a regression in fidelity even though the proxy pool's cooling recovers. The right fix is per-job state (via context value or a per-PlaceJob counter that aggregates to the scrape), not a snapshot trick. Tracked as a follow-up because it touches gmaps's core review pipeline.

---

## Done When

- All proxypool unit tests pass under `-race`
- `/internal/proxy/stats` serves a credential-stripped JSON snapshot
- A pool of one valid + one bogus proxy gracefully quarantines the bogus one and keeps scraping
- `ErrPoolExhausted` surfaces as `CauseProxyPoolExhausted` job outcome with operator-visible message
- PR opened against `develop`
