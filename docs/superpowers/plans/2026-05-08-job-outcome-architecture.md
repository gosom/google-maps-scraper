# Job Outcome Architecture Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the two architectural bugs surfaced in the May 2026 Loki audit — `job_scrape_succeeded` lying after a failed run, and the 30s grace period wasted on terminally-failed seeds — by introducing `JobOutcome` as a first-class return value and `SeedOutcome` as a typed exit-monitor signal. After this plan, **the function return value IS the outcome**; status-mutation-as-side-effect is gone.

**Architecture:** Two surgical changes. (1) `webrunner.scrapeJob` returns a `JobOutcome` struct (not bare `error`) constructed by a pure `classifyOutcome(...)` function. The outer worker switches on `outcome.Status` to log the correct event — making `succeeded` and `failed` impossible to mix up by construction. (2) `exiter.RecordSeedOutcome(SeedOutcome{...})` replaces the split `IncrSeedCompleted + RecordSeedError` calls; `isDone()` fail-fasts when all seeds terminally failed, eliminating the 30s magic wait.

**Tech Stack:** Go 1.25, existing `database/sql` + `pgx`, `slog`, `scrapemate`, no new dependencies. `testify/require` (already transitively present) standardized for fatal asserts; `go.uber.org/goleak` for goroutine-leak detection in test packages that touch the runner.

---

## Decisions (and YAGNI cuts) — synthesized from 5 Go-skill brainstorms

| # | Decision | Rationale |
|---|---|---|
| **D1** | `JobOutcome` is a **struct returned by value**, not state-mutated on `*web.Job`. | Single source of truth. Function return reflects outcome. The `golang-error-handling` skill: "return error or return outcome value, never both — and never mutate state under the guise of returning nil". |
| **D2** | `models.Status` stays a **named string type** (not `iota` int). | Persisted to Postgres + serialized to JSON clients. Switching to int breaks the DB and every consumer. The `golang-structs-interfaces` skill prevailed over the design-patterns proposal here — wire-compat trumps purity. |
| **D3** | `TerminalCause` is a **named string type**, internal to `runner/webrunner`. | It's logged to slog and embedded in error attrs; readable values (`"seed_exhausted"`, `"user_cancel"`) beat opaque ints (`2`) in Loki streams. Internal-only — never surfaced to UI. |
| **D4** | `JobOutcome` uses **constructor-based invariants**, not a validate-at-construction switch. | `OutcomeSuccess(n)` / `OutcomeFailed(cause, reason, raw)` / `OutcomeUserCancelled()` / `OutcomePartial(n)` — illegal states unrepresentable. The `Status`/`Cause`/`FailureReason` correlation is enforced by the constructor; no validate(). |
| **D5** | `classifyOutcome` is a **pure function** — `(mateErr, userInitiatedCancel bool, resultCount, seedErr) → JobOutcome`. | The 80-line `if err != nil { if errors.Is(err, ctx.Canceled) {...} else if ... }` tree at `webrunner.go:1082-1207` is currently untestable because it reads from DB, exit monitor, and job in-place. Extracting the decision logic to a pure function makes every branch a row in a table-driven test. |
| **D6** | `SeedOutcome` is a **value type with `Err`, `RetriesLeft`, `PlacesFound`**, recorded via a single `RecordSeedOutcome` call (not the current split `IncrSeedCompleted + RecordSeedError`). | Counter carries semantics. `isDone()` can ask "did every seed terminally fail?" instead of guessing via "all seeds counted AND no places found AND 30s elapsed". |
| **D7** | **`JobOutcome` lives in `runner/webrunner/`**, not `web/` or `models/`. | It's an internal coordination type for the webrunner. No HTTP handler needs it; no model layer needs it. Putting it in `models/` reverses dependency direction (models would import webrunner concepts). |
| **D8** | Sanitizer (`runner/webrunner/failure_reason.go` from PR #55) **stays unchanged**. Its call site moves into `OutcomeFailed`'s constructor — pure transform, no logging side-effect at call site. | The sanitizer is already a pure function; the bug was that its caller logged at the same site. Moving the call into the constructor enforces that pattern. |
| **D9** | **NO `samber/oops`**, NO new dependencies. Continue `fmt.Errorf("...%w", err)` for wrapping. | Codebase doesn't use oops. Introducing it mid-refactor creates two incompatible error styles. `slog` with structured attrs at the single log site achieves the same observability. |
| **D10** | **NO `errgroup` migration of the 4-goroutine select**. The concurrency brief identified a real watcher-goroutine leak (unconditional `time.Sleep(30s)` after parent returns) but it's orthogonal to the two bugs in scope. Tracked as a follow-up. | "Do not over-engineer". The two bugs both fix at the seam between scrapeJob and its outcome — not in the goroutine-coordination model. Follow-up PR will replace channel-soup with `errgroup`. |
| **D11** | **NO Exiter interface split** (currently 13 methods → could become 3 narrow interfaces). | "Wait for 2+ implementations". The current Exiter has one consumer pattern (workers write, runner reads). Phase 2 adds 1 method, doesn't justify the cleanup. Follow-up PR. |
| **D12** | **NO Phase 3 coordinator state machine / typed-event channel.** | Brainstorm convergence: Phase 1+2 buy 80% of the value. The state-machine refactor (consume typed events from a single channel) is the long-term clean architecture but doesn't fix the two bugs. Defer until a third bug in this surface justifies it. |

**Out-of-scope follow-ups (deliberate YAGNI):**
- ❌ `errgroup`-based goroutine lifecycle for `scrapeJob` (fixes a real leak; separate PR)
- ❌ `Exiter` interface split into `SeedProgressWriter` / `JobProgressReader` (hygiene; separate PR)
- ❌ `Scraper` interface for injecting fake scrapemate in tests (Phase 3)
- ❌ Pure `coordinate()` state machine consuming typed events (Phase 3)
- ❌ Place-job failure tracking (different bug class; not the seed-completion gate)

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `runner/webrunner/outcome.go` | **Create** | `JobOutcome` struct, `TerminalCause` enum, `Outcome*` constructors. Pure types — no I/O, no logging, no goroutines. |
| `runner/webrunner/outcome_test.go` | **Create** | Table-driven assertions on each constructor's invariants (correct Status / Cause / FailureReason mapping). |
| `runner/webrunner/classify.go` | **Create** | `classifyOutcome(mateErr, userInitiatedCancel, resultCount, seedErr) JobOutcome` — pure function. The decision tree currently inlined at `webrunner.go:1082-1207`, extracted. |
| `runner/webrunner/classify_test.go` | **Create** | Table-driven tests covering all 6 terminal scenarios (happy / partial / cancelled-by-user / cancelled-empty / timeout-with-results / timeout-empty / seed-failure). |
| `runner/webrunner/webrunner.go` | Modify (~80 LOC removed, ~25 added net) | `scrapeJob` signature changes to `(JobOutcome, error)`. The `if errors.Is(err, ...) { ... } else if ... { ... }` tree at lines 1082-1207 collapses to one `classifyOutcome(...)` call. The outer worker (lines 633-666) switches on `outcome.Status` and logs `job_scrape_succeeded` only when it actually succeeded. |
| `exiter/seed_outcome.go` | **Create** | `SeedOutcome` struct (`Err`, `RetriesLeft`, `PlacesFound`). Lives separately so importers don't pull the rest of `exiter` if they only need the type. |
| `exiter/exiter.go` | Modify (~30 LOC) | Add `RecordSeedOutcome(SeedOutcome)` method on the `Exiter` interface. Update `isDone()` to fail-fast when `allSeedsTerminallyFailed()`. Keep `IncrSeedCompleted` + `RecordSeedError` for backwards-compat (deprecated; new callers use `RecordSeedOutcome`). |
| `exiter/exiter_test.go` | **Create** | Tests for `RecordSeedOutcome` accumulation + `isDone()` short-circuit on terminal-seeds-only path. |
| `gmaps/job.go` | Modify (~5 LOC) | The fetch-error branch in `Process()` (added in PR #54) calls `RecordSeedOutcome(SeedOutcome{Err: resp.Error, ...})` once instead of `IncrSeedCompleted + RecordSeedError` twice. |
| `gmaps/job_test.go` | Modify (~10 LOC) | Update `fakeExiter` to implement `RecordSeedOutcome`; existing assertions unchanged in spirit (verify the seed was recorded as failed). |
| `runner/webrunner/webrunner_test.go` | Modify or create | `TestMain` adds `goleak.VerifyTestMain(m)` so any leaked goroutine in the new tests fails the suite immediately. |
| `go.mod` / `go.sum` | Modify | Add `go.uber.org/goleak` (likely already transitive — make direct). |

**No new packages.** Both new types and the classifier live in `runner/webrunner/` (the consumer). The exiter changes stay in `exiter/`.

---

## Task 1 — `JobOutcome` types and constructors (pure data, no behavior)

**Files:**
- Create: `runner/webrunner/outcome.go`
- Create: `runner/webrunner/outcome_test.go`

This is a no-behavior task: introduce types + constructors before anything calls them. Compiles cleanly on its own.

- [ ] **Step 1: Write the failing tests** in `runner/webrunner/outcome_test.go`

```go
package webrunner

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/models"
)

// Each constructor enforces an invariant about the (Status, Cause, FailureReason,
// rawErr) tuple so callers cannot accidentally produce illegal combinations like
// "Status=Completed but Cause=RuntimeError". Tests pin these invariants.

func TestOutcomeSuccess(t *testing.T) {
	t.Parallel()
	o := OutcomeSuccess(42)
	assert.Equal(t, models.StatusCompleted, o.Status)
	assert.Equal(t, CauseSuccess, o.Cause)
	assert.Equal(t, 42, o.ResultCount)
	assert.Empty(t, o.FailureReason, "successful outcome must have no failure reason")
	assert.NoError(t, o.Err(), "successful outcome must not carry a raw error")
}

func TestOutcomePartial_TimeoutWithResults(t *testing.T) {
	t.Parallel()
	// Timeout WITH results is treated as success per existing webrunner.go:1136-1139.
	// OutcomePartial captures this case explicitly so the outer worker can log
	// "job_scrape_partial" if it ever wants to distinguish — today it logs
	// "job_scrape_succeeded" for any Status==Completed.
	o := OutcomePartial(7)
	assert.Equal(t, models.StatusCompleted, o.Status, "partial-with-results is still Completed")
	assert.Equal(t, CausePartial, o.Cause)
	assert.Equal(t, 7, o.ResultCount)
}

func TestOutcomeUserCancelled(t *testing.T) {
	t.Parallel()
	o := OutcomeUserCancelled()
	assert.Equal(t, models.StatusCancelled, o.Status)
	assert.Equal(t, CauseUserCancel, o.Cause)
	assert.Empty(t, o.FailureReason, "user cancellation is not a failure")
}

func TestOutcomeFailed_CarriesReasonAndRawErr(t *testing.T) {
	t.Parallel()
	raw := errors.New("playwright: net::ERR_PROXY_CONNECTION_FAILED at https://x")
	o := OutcomeFailed(CauseSeedExhausted, "Proxy connection failed", raw)
	assert.Equal(t, models.StatusFailed, o.Status)
	assert.Equal(t, CauseSeedExhausted, o.Cause)
	assert.Equal(t, "Proxy connection failed", o.FailureReason)
	require.ErrorIs(t, o.Err(), raw, "raw error must be retrievable for support logging")
}

func TestJobOutcome_RawErrIsUnexported(t *testing.T) {
	t.Parallel()
	// Documents the design: rawErr is unexported. Callers MUST use Err() —
	// they should not be able to grep job.RawErr and string-match on it.
	o := OutcomeFailed(CauseRuntimeError, "X", errors.New("internal detail"))
	// If you can read the unexported field by name, this whole test file
	// failed to compile (which would itself signal a regression). The
	// runtime assertion is just that Err() returns the same value.
	assert.NotNil(t, o.Err())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./runner/webrunner/ -run "TestOutcome|TestJobOutcome" -count=1 -v
```
Expected: compile errors (`undefined: OutcomeSuccess`, `undefined: JobOutcome`, etc.).

- [ ] **Step 3: Implement `runner/webrunner/outcome.go`**

```go
// Package webrunner — outcome.go defines the value-typed result of running
// a single scrape job. The function webrunner.scrapeJob returns a JobOutcome
// rather than mutating *web.Job in place, so the outer worker can log the
// correct event by switching on outcome.Status (the prior code logged
// "job_scrape_succeeded" whenever scrapeJob returned nil error, even if
// internal state had set jobSuccess=false — the bug this type prevents
// by construction).
package webrunner

import "github.com/gosom/google-maps-scraper/models"

// TerminalCause is an internal-only label describing WHY a job ended.
// Logged to slog as a structured attribute and used in the outer worker's
// switch; never surfaced to the UI (see FailureReason for that).
//
// Named string (not iota) so Loki streams read "seed_exhausted" instead of
// "1" — operational debugging trumps a few bytes per log line.
type TerminalCause string

const (
	CauseSuccess       TerminalCause = "success"
	CauseSeedExhausted TerminalCause = "seed_exhausted" // all seeds terminally failed
	CauseUserCancel    TerminalCause = "user_cancel"    // user hit Cancel via API
	CauseHardTimeout   TerminalCause = "hard_timeout"   // job's allowed_seconds budget expired
	CauseRuntimeError  TerminalCause = "runtime_error"  // unexpected error from mate.Start
	CausePartial       TerminalCause = "partial"        // timed out / cancelled but produced results
)

// JobOutcome is the discriminated result of one scrape job.
//
// Constructed via OutcomeSuccess / OutcomePartial / OutcomeUserCancelled /
// OutcomeFailed — direct struct literals are discouraged because the
// constructors enforce the (Status, Cause, FailureReason) correlation.
//
// rawErr is unexported on purpose. Use Err() to retrieve it. The reason:
// raw errors are for ERROR-level support logging only; they MUST NOT leak
// into the UI's failure_reason. Keeping rawErr unexported makes it
// awkward enough that callers reach for FailureReason instead.
type JobOutcome struct {
	Status        models.Status
	Cause         TerminalCause
	FailureReason string
	ResultCount   int
	rawErr        error
}

// Err returns the raw error captured at outcome construction. Always nil for
// success / user-cancel outcomes. For failed outcomes, the error is logged
// at ERROR by the outer worker with the job_id attribute so support can
// correlate the user-facing FailureReason back to the underlying technical
// cause in Loki.
func (o JobOutcome) Err() error { return o.rawErr }

// OutcomeSuccess — every seed completed and we wrote at least one result row.
func OutcomeSuccess(resultCount int) JobOutcome {
	return JobOutcome{
		Status:      models.StatusCompleted,
		Cause:       CauseSuccess,
		ResultCount: resultCount,
	}
}

// OutcomePartial — the job didn't complete cleanly (timeout, mid-run cancel)
// but produced at least one result row. Existing webrunner logic already
// treats this as "Completed" — we keep that behavior but tag the cause so
// future code (e.g. a "job_scrape_partial" log line, a UI badge) can
// distinguish without touching the persisted Status string.
func OutcomePartial(resultCount int) JobOutcome {
	return JobOutcome{
		Status:      models.StatusCompleted,
		Cause:       CausePartial,
		ResultCount: resultCount,
	}
}

// OutcomeUserCancelled — the user hit Cancel via the API; the DB row's status
// transitioned through "aborting" and we caught it. Not a failure; no
// FailureReason needed.
func OutcomeUserCancelled() JobOutcome {
	return JobOutcome{
		Status: models.StatusCancelled,
		Cause:  CauseUserCancel,
	}
}

// OutcomeFailed — terminal failure with a sanitized user-facing reason and
// the raw error attached for support correlation. Use the appropriate cause:
// CauseSeedExhausted for proxy/network failures, CauseHardTimeout for
// allowed_seconds expiry, CauseRuntimeError for everything else.
func OutcomeFailed(cause TerminalCause, reason string, raw error) JobOutcome {
	return JobOutcome{
		Status:        models.StatusFailed,
		Cause:         cause,
		FailureReason: reason,
		rawErr:        raw,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./runner/webrunner/ -run "TestOutcome|TestJobOutcome" -count=1 -v
```
Expected: PASS for all 5 tests.

- [ ] **Step 5: Build everything to confirm no breakage**

```bash
go build ./...
```
Expected: clean (the new file isn't imported anywhere yet — that's the next task).

- [ ] **Step 6: Commit**

```bash
git add runner/webrunner/outcome.go runner/webrunner/outcome_test.go
git commit -m "feat(webrunner): add JobOutcome value type with invariant constructors"
```

---

## Task 2 — `classifyOutcome` pure function (extract the err-tree from scrapeJob)

**Files:**
- Create: `runner/webrunner/classify.go`
- Create: `runner/webrunner/classify_test.go`

The 80-line `if err != nil { if errors.Is(err, context.Canceled) { ... } else if errors.Is(err, context.DeadlineExceeded) { ... } else { ... } }` block at `webrunner.go:1082-1207` is the bug factory. Extract it as a pure function that's directly testable.

- [ ] **Step 1: Write the failing tests** in `runner/webrunner/classify_test.go`

```go
package webrunner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gosom/google-maps-scraper/models"
)

// classifyOutcome is the pure-function brain of scrapeJob. It takes the four
// signals scrapeJob already collects (mate's return error, whether a user
// cancellation was detected, the post-run result count, and the exit
// monitor's last seed error) and produces the JobOutcome that the outer
// worker logs and persists.
//
// Pure: no DB, no goroutines, no logger. Directly assertable per row.

func TestClassifyOutcome(t *testing.T) {
	t.Parallel()
	proxyErr := errors.New("playwright: net::ERR_PROXY_CONNECTION_FAILED at https://x")
	tests := []struct {
		name        string
		mateErr     error
		userCancel  bool
		resultCount int
		seedErr     error
		// expected
		wantStatus models.Status
		wantCause  TerminalCause
		// FailureReason: empty == "doesn't matter"; otherwise must equal exactly
		wantReasonExact string
		// FailureReason must NOT be the literal raw seed error (sanitizer guard)
		wantReasonNotRaw bool
	}{
		{
			name:        "happy path: mate returned nil with results",
			mateErr:     nil,
			resultCount: 5,
			wantStatus:  models.StatusCompleted,
			wantCause:   CauseSuccess,
		},
		{
			name:        "all seeds terminally failed (proxy down): cancelled with 0 results + seed error",
			mateErr:     context.Canceled,
			resultCount: 0,
			seedErr:     proxyErr,
			wantStatus:  models.StatusFailed,
			wantCause:   CauseSeedExhausted,
			// Must be sanitized — never the raw err.Error()
			wantReasonExact:  "Proxy connection failed",
			wantReasonNotRaw: true,
		},
		{
			name:        "user cancelled with results: partial success",
			mateErr:     context.Canceled,
			userCancel:  true,
			resultCount: 3,
			wantStatus:  models.StatusCompleted,
			wantCause:   CausePartial,
		},
		{
			name:       "user cancelled with 0 results: cancelled, not failed",
			mateErr:    context.Canceled,
			userCancel: true,
			wantStatus: models.StatusCancelled,
			wantCause:  CauseUserCancel,
		},
		{
			name:        "deadline exceeded with results: treat as success",
			mateErr:     context.DeadlineExceeded,
			resultCount: 9,
			wantStatus:  models.StatusCompleted,
			wantCause:   CausePartial,
		},
		{
			name:            "deadline exceeded with 0 results: hard-timeout failure",
			mateErr:         context.DeadlineExceeded,
			wantStatus:      models.StatusFailed,
			wantCause:       CauseHardTimeout,
			wantReasonExact: "job timed out with 0 results",
		},
		{
			name:       "runtime error: real error path, no seed-error context",
			mateErr:    errors.New("mate.Start: pipe broken"),
			wantStatus: models.StatusFailed,
			wantCause:  CauseRuntimeError,
			// Reason text is generic but non-empty (UI shows "Job failed. Reason: <reason>")
			wantReasonExact: "job failed due to a runtime error",
		},
		{
			name:        "context.Canceled with 0 results AND no seed error: external cancel, nothing to attribute",
			mateErr:     context.Canceled,
			resultCount: 0,
			seedErr:     nil,
			wantStatus:  models.StatusFailed,
			wantCause:   CauseSeedExhausted,
			// Generic but informative — keeps FailureReason non-empty so the
			// frontend renders "Job failed. Reason: Scraping aborted before any
			// results were collected"
			wantReasonExact: "Scraping aborted before any results were collected",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyOutcome(tc.mateErr, tc.userCancel, tc.resultCount, tc.seedErr)
			assert.Equal(t, tc.wantStatus, got.Status, "Status")
			assert.Equal(t, tc.wantCause, got.Cause, "Cause")
			assert.Equal(t, tc.resultCount, got.ResultCount, "ResultCount")
			if tc.wantReasonExact != "" {
				assert.Equal(t, tc.wantReasonExact, got.FailureReason, "FailureReason")
			}
			if tc.wantReasonNotRaw && tc.seedErr != nil {
				assert.NotEqual(t, tc.seedErr.Error(), got.FailureReason,
					"FailureReason must be sanitized — never the raw err.Error()")
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests, verify they fail**

```bash
go test ./runner/webrunner/ -run TestClassifyOutcome -count=1 -v
```
Expected: compile error `undefined: classifyOutcome`.

- [ ] **Step 3: Implement `runner/webrunner/classify.go`**

```go
package webrunner

import (
	"context"
	"errors"
)

// classifyOutcome is the pure decision logic that translates the signals
// collected during a scrape run into a JobOutcome. It replaces the err-type
// branching tree that previously lived inline in scrapeJob (~80 lines at
// webrunner.go:1082-1207). Extracting it makes every termination scenario
// directly assertable without spinning up scrapemate, Postgres, or
// goroutines — see classify_test.go.
//
// Inputs (all collected by scrapeJob from existing sources):
//   mateErr             — mate.Start's return value (nil on clean exit)
//   userInitiatedCancel — true when the DB job's status was already
//                          "aborting" or "cancelled" when we checked
//   resultCount         — COUNT(*) FROM results WHERE job_id = ...
//   seedErr             — exitMonitor.LastSeedError() (nil if no seed
//                          recorded an error)
//
// Output: a JobOutcome the outer worker uses to log the correct event AND
// persist the correct DB state — single source of truth.
func classifyOutcome(mateErr error, userInitiatedCancel bool, resultCount int, seedErr error) JobOutcome {
	if mateErr == nil {
		// mate.Start returned cleanly — happy path.
		return OutcomeSuccess(resultCount)
	}

	switch {
	case errors.Is(mateErr, context.Canceled):
		if userInitiatedCancel {
			// User pressed Cancel; the DB transition was confirmed by
			// scrapeJob's status check. Honor that even if results trickled
			// in mid-cancel — those results are kept (Partial), but the
			// status is Cancelled either way per the existing contract.
			if resultCount > 0 {
				return OutcomePartial(resultCount)
			}
			return OutcomeUserCancelled()
		}
		// Not a user cancel — our own exit monitor cancelled mate's context.
		// Why? Either every seed terminally failed (seedErr != nil) or the
		// exit monitor signalled completion legitimately (e.g. max_results
		// reached) and produced results.
		if resultCount > 0 {
			return OutcomePartial(resultCount)
		}
		// Zero results AND not user-cancelled. The exit monitor cancelled
		// because all seeds finished with no places. Surface the seed-level
		// error if we have one (sanitized for the UI).
		if seedErr != nil {
			return OutcomeFailed(CauseSeedExhausted, sanitizeSeedError(seedErr), seedErr)
		}
		// No seed error recorded — generic but honest.
		return OutcomeFailed(CauseSeedExhausted, "Scraping aborted before any results were collected", nil)

	case errors.Is(mateErr, context.DeadlineExceeded):
		// allowed_seconds budget expired. With results → partial success;
		// without → hard-timeout failure.
		if resultCount > 0 {
			return OutcomePartial(resultCount)
		}
		return OutcomeFailed(CauseHardTimeout, "job timed out with 0 results", mateErr)

	default:
		// Unexpected error from mate.Start. Anything not a context error
		// lands here. We don't surface the raw text to the UI (could leak
		// internal detail); generic message + raw err for support.
		return OutcomeFailed(CauseRuntimeError, "job failed due to a runtime error", mateErr)
	}
}
```

- [ ] **Step 4: Run tests, verify all pass**

```bash
go test ./runner/webrunner/ -run TestClassifyOutcome -count=1 -v
```
Expected: 8/8 PASS.

- [ ] **Step 5: Build clean**

```bash
go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add runner/webrunner/classify.go runner/webrunner/classify_test.go
git commit -m "feat(webrunner): extract classifyOutcome as pure function (no I/O, no goroutines)"
```

---

## Task 3 — Wire `JobOutcome` through `scrapeJob` and the outer worker

**Files:**
- Modify: `runner/webrunner/webrunner.go` lines 612-666 (outer worker), lines 677-1280 (`scrapeJob` body)

This is the surgical change. Two parts:
1. `scrapeJob` returns `JobOutcome` (instead of `error`).
2. Outer worker switches on `outcome.Status` to log the correct event.

The *internal* mechanics of `scrapeJob` (4 goroutines, channels, exit monitor) stay unchanged. Only the **outcome construction** at the bottom and the **return signature** change.

- [ ] **Step 1: Read `webrunner.go:612-1280` end-to-end**

This is the function we're surgery-ing. Don't skip — internalize how `jobSuccess`, `job.Status`, and `job.FailureReason` get set today so you can confidently delete those mutations.

- [ ] **Step 2: Modify `scrapeJob`'s signature + body**

Change the function signature:

```go
// Before:
func (w *webrunner) scrapeJob(ctx context.Context, job *web.Job) error {

// After:
func (w *webrunner) scrapeJob(ctx context.Context, job *web.Job) JobOutcome {
```

Inside, REPLACE the entire `if err != nil { if errors.Is(err, context.Canceled) { ... } else if errors.Is(err, context.DeadlineExceeded) { ... } else { ... } } else { jobSuccess = true }` block at lines 1082-1167 with this:

```go
// Collect the four signals classifyOutcome needs.
//
// userInitiatedCancel: a context.Canceled mateErr could mean either user
// cancellation OR our exit monitor finishing the job. Distinguish by reading
// the DB row — if the status had already transitioned to "aborting" or
// "cancelled" before we checked, the user initiated it.
userInitiatedCancel := false
if errors.Is(err, context.Canceled) {
	cancelCheckCtx, cancelCheckCancel := context.WithTimeout(context.Background(), 10*time.Second)
	currentJob, getErr := w.svc.Get(cancelCheckCtx, job.ID, "")
	cancelCheckCancel()
	if getErr != nil {
		// Can't read the DB → conservatively assume user cancelled
		// (matches prior fallback behavior at lines 1090-1094).
		w.logger.Debug("status_check_after_cancel_failed",
			slog.String("job_id", job.ID), slog.Any("error", getErr))
		userInitiatedCancel = true
	} else if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled {
		userInitiatedCancel = true
	}
}

// Read the result count from the DB once (was being read in 4 different
// places in the prior code). On error, default to 0 — pessimistic but safe.
var resultCount int
if w.db != nil {
	countCtx, countCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if dbErr := w.db.QueryRowContext(countCtx, `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); dbErr != nil {
		w.logger.Debug("result_count_query_failed",
			slog.String("job_id", job.ID), slog.Any("error", dbErr))
		resultCount = 0
	}
	countCancel()
}

// One pure function call. No more 80-line err-type ladder.
outcome := classifyOutcome(err, userInitiatedCancel, resultCount, exitMonitor.LastSeedError())

// Apply the outcome to the job for downstream persistence (the deferred
// Update at line 679 reads from job.* — preserve that contract).
job.Status = outcome.Status
job.FailureReason = outcome.FailureReason
job.ResultCount = outcome.ResultCount

// Continue with billing / sanity checks — the existing code below this
// point uses jobSuccess and job.Status. Replace jobSuccess derivation:
jobSuccess := (outcome.Status == web.StatusCompleted)
```

DELETE the `jobSuccess` declaration at the top of the function (it's now derived from the outcome). DELETE every line that mutates `job.Status` / `job.FailureReason` between the original 1082 and the billing section — those are now owned by `outcome`.

KEEP the post-run sanity checks (lines 1170-1186), the billing section (lines 1187-1267), and any other side-effect logic. Those continue to read `job.Status` / `jobSuccess` and may FURTHER mutate `job.Status` to Failed if billing fails. That's allowed — billing failure overrides outcome. Document that.

At the end of `scrapeJob`, RETURN the outcome:

```go
// (replace the implicit `return nil` at the end)
return outcome
```

If the billing block sets `job.Status = web.StatusFailed`, also reflect that in the outcome:

```go
// Inside the billing-failed branch (line ~1228):
job.Status = web.StatusFailed
job.FailureReason = "billing processing failed"
outcome = OutcomeFailed(CauseRuntimeError, "billing processing failed", err)
return outcome
```

- [ ] **Step 3: Modify the OUTER worker (webrunner.go:633-666)**

Change the call site:

```go
// Before:
if err := w.scrapeJob(ctx, &job); err != nil {
	duration := time.Since(t0)
	// ... log job_scrape_failed
} else {
	duration := time.Since(t0)
	// ... log job_scrape_succeeded
}

// After:
outcome := w.scrapeJob(ctx, &job)
duration := time.Since(t0)
params := map[string]any{
	"job_count": len(job.Data.Keywords),
	"duration":  duration.String(),
	"cause":     string(outcome.Cause),
}
if outcome.Err() != nil {
	params["error"] = outcome.Err().Error()
}
_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("web_runner", params))

switch outcome.Status {
case web.StatusCompleted:
	// Includes both clean success and partial (results obtained mid-cancel/timeout).
	// If you ever want a separate signal, branch on outcome.Cause==CausePartial.
	w.logger.Info("job_scrape_succeeded",
		slog.String("job_id", job.ID),
		slog.String("user_id", job.UserID),
		slog.Int("result_count", outcome.ResultCount),
		slog.String("cause", string(outcome.Cause)),
		slog.Duration("duration", duration),
	)
case web.StatusCancelled:
	w.logger.Info("job_scrape_cancelled",
		slog.String("job_id", job.ID),
		slog.String("user_id", job.UserID),
		slog.Duration("duration", duration),
	)
case web.StatusFailed:
	w.logger.Error("job_scrape_failed",
		slog.String("job_id", job.ID),
		slog.String("user_id", job.UserID),
		slog.String("cause", string(outcome.Cause)),
		slog.String("failure_reason", outcome.FailureReason),
		slog.Any("error", outcome.Err()),
		slog.Duration("duration", duration),
	)
default:
	// Should never happen — JobOutcome's constructors guarantee a valid Status.
	w.logger.Error("job_scrape_unknown_status",
		slog.String("job_id", job.ID),
		slog.String("status", string(outcome.Status)),
	)
}
```

- [ ] **Step 4: Build everything**

```bash
go build ./...
```
Expected: clean. If there are build errors, address them — most likely callers of `scrapeJob` whose error-handling paths need adapting.

- [ ] **Step 5: Run the existing webrunner test suite**

```bash
go test ./runner/webrunner/ -count=1
```
Expected: green. The startup test (`webrunner_startup_test.go`) doesn't touch `scrapeJob` so it should be unaffected.

- [ ] **Step 6: Run the full backend test suite**

```bash
PG_TEST_DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" \
  go test ./... -count=1 2>&1 | grep -E "FAIL|ok " | head -30
```
Pre-existing failures in `gmaps/` (`Test_getNthElementAndCast_DoesNotPanicOnNegativeIndex`) and `postgres/` (`TestPostgresRepository`) are unrelated and acceptable.

- [ ] **Step 7: Commit**

```bash
git add runner/webrunner/webrunner.go
git commit -m "refactor(webrunner): scrapeJob returns JobOutcome; outer worker logs by status"
```

---

## Task 4 — `SeedOutcome` typed event + `RecordSeedOutcome` API

**Files:**
- Create: `exiter/seed_outcome.go`
- Modify: `exiter/exiter.go`
- Create: `exiter/exiter_test.go`

This decouples "seed completed" from "seed failed" — the two were a single counter increment + a side-channel error record. Now they're one structured event.

- [ ] **Step 1: Write failing tests** in `exiter/exiter_test.go`

```go
package exiter

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// RecordSeedOutcome is the typed-event API replacing the older
// IncrSeedCompleted + RecordSeedError split. The new isDone() reads
// the recorded outcomes to fail-fast when every seed terminally failed,
// eliminating the 30s grace period that wasted CPU on a doomed run.

func TestRecordSeedOutcome_AccumulatesCounter(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(3)
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 5})
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 0})
	completed, total := e.GetSeedProgress()
	assert.Equal(t, 2, completed)
	assert.Equal(t, 3, total)
}

func TestRecordSeedOutcome_StoresLastError(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	first := errors.New("first failure")
	last := errors.New("last failure")
	e.RecordSeedOutcome(SeedOutcome{Err: first})
	e.RecordSeedOutcome(SeedOutcome{Err: last})
	assert.ErrorIs(t, e.LastSeedError(), last,
		"LastSeedError must reflect the most recently recorded failure")
}

func TestIsDone_FailFastOnAllSeedsTerminallyFailed(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(2)
	// Both seeds finished, both with terminal errors, no places found.
	// Per the prior code this would wait 30s before isDone()→true. Now: fast.
	e.RecordSeedOutcome(SeedOutcome{Err: errors.New("proxy fail"), RetriesLeft: 0})
	e.RecordSeedOutcome(SeedOutcome{Err: errors.New("dns fail"), RetriesLeft: 0})
	// Force startTime to "just now" so the prior 30s grace WOULD have fired
	// if the new logic didn't short-circuit.
	e.startTime = time.Now()
	assert.True(t, e.isDone(),
		"every seed terminally failed with 0 places — must short-circuit, not wait 30s")
}

func TestIsDone_StillGracesEmptySuccess(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(1)
	// Seed succeeded but produced 0 places — could be a search page that
	// rendered slowly. The 30s grace is preserved for THIS case.
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 0})
	e.startTime = time.Now() // reset grace clock
	assert.False(t, e.isDone(),
		"seed succeeded-but-empty: keep the 30s grace for slow renderers")
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./exiter/ -count=1 -v
```
Expected: compile errors (`undefined: SeedOutcome`, `undefined: RecordSeedOutcome`).

- [ ] **Step 3: Create `exiter/seed_outcome.go`**

```go
package exiter

// SeedOutcome is the typed event recorded once per terminal seed-job
// completion (success or final failure after retries). It carries enough
// signal for the exit monitor's isDone() to distinguish "every seed
// terminally failed → fail fast" from "every seed succeeded but produced
// no places → grace period for slow page renders".
//
// Callers (gmaps.GmapJob.Process) construct SeedOutcome inline — no
// constructor needed; zero-value (all-empty) is a valid "successful seed
// that found zero places" event.
type SeedOutcome struct {
	// Err is the seed-level error if the seed terminally failed (proxy
	// down, navigation error, retry-exhausted). Nil on success.
	Err error
	// RetriesLeft signals whether scrapemate may still retry this seed.
	// Always 0 in the current code (Process is only called after retries
	// are exhausted), but kept as a field so future callers can record
	// non-terminal failures without polluting LastSeedError.
	RetriesLeft int
	// PlacesFound is how many place-jobs this seed produced. 0 means the
	// seed succeeded but the search returned nothing — legit on rare
	// queries; signals to isDone() to grace.
	PlacesFound int
}

// IsTerminal reports whether this outcome means the seed is permanently
// done — either failed with no retries left, or succeeded.
func (s SeedOutcome) IsTerminal() bool {
	return s.RetriesLeft == 0
}

// IsTerminalFailure reports whether this seed terminally failed (no more
// retries) AND produced no usable result. Used by isDone() to fail-fast.
func (s SeedOutcome) IsTerminalFailure() bool {
	return s.IsTerminal() && s.Err != nil && s.PlacesFound == 0
}
```

- [ ] **Step 4: Modify `exiter/exiter.go`** to add `RecordSeedOutcome` + smarter `isDone()`

Add the field to track terminal-failure count:

```go
// In the exiter struct:
type exiter struct {
	// ... existing fields ...
	terminallyFailed int // count of seeds that recorded SeedOutcome.IsTerminalFailure()
}
```

Add the new method:

```go
// RecordSeedOutcome is the typed-event API for seed-completion. Atomically
// increments seedCompleted, stores the last error if any, and tracks the
// terminal-failure count for isDone()'s fail-fast logic. Replaces the
// older split call sequence IncrSeedCompleted(1) + RecordSeedError(err).
//
// The legacy methods (IncrSeedCompleted, RecordSeedError) remain for
// backwards-compat with callers that haven't migrated yet — they delegate
// to RecordSeedOutcome internally to avoid double-counting.
func (e *exiter) RecordSeedOutcome(o SeedOutcome) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seedCompleted++
	if o.Err != nil {
		e.lastSeedError = o.Err
	}
	if o.IsTerminalFailure() {
		e.terminallyFailed++
	}
	e.lastProgressTime = time.Now()
}
```

Modify `isDone()` to short-circuit terminal failures BEFORE the existing 30s grace check:

```go
// At the top of isDone(), AFTER the maxResults check, BEFORE the seedCount check:

// Fail-fast: every seed has terminally failed. The exit monitor's prior
// 30s grace existed because the counter alone couldn't distinguish
// "succeeded-but-empty" (legit slow render) from "all failed" (no point
// waiting). RecordSeedOutcome now carries the discriminator — short-circuit.
if e.seedCount > 0 && e.terminallyFailed >= e.seedCount {
	slog.Debug("exit_done_all_seeds_terminally_failed",
		slog.Int("seed_count", e.seedCount),
		slog.Int("terminally_failed", e.terminallyFailed),
	)
	return true
}
```

- [ ] **Step 5: Run tests, verify they pass**

```bash
go test ./exiter/ -count=1 -v
```
Expected: 4/4 PASS.

- [ ] **Step 6: Commit**

```bash
git add exiter/seed_outcome.go exiter/exiter.go exiter/exiter_test.go
git commit -m "feat(exiter): RecordSeedOutcome typed event + fail-fast on terminal seeds"
```

---

## Task 5 — Migrate `gmaps.GmapJob.Process` to use `RecordSeedOutcome`

**Files:**
- Modify: `gmaps/job.go` (the fetch-error branch added in PR #54)
- Modify: `gmaps/job_test.go` (`fakeExiter`)

- [ ] **Step 1: Update `gmaps/job_test.go`**

Add `RecordSeedOutcome` to `fakeExiter`:

```go
// In fakeExiter — add to the existing fields:
type fakeExiter struct {
	// ... existing fields ...
	lastSeedOutcome exiter.SeedOutcome // the last RecordSeedOutcome arg
}

// New method on fakeExiter:
func (f *fakeExiter) RecordSeedOutcome(o exiter.SeedOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSeedOutcome = o
	f.seedCompleted.Add(1)
	if o.Err != nil {
		f.lastSeedError = o.Err
	}
}
```

Update the existing `TestGmapJob_Process_FetchError_IncrementsSeedCompleted` to also verify the new API was the path taken:

```go
// At the end of the existing test:
gotOutcome := exiter.lastSeedOutcome
assert.Same(t, resp.Error, gotOutcome.Err, "RecordSeedOutcome must carry the raw fetch error")
assert.True(t, gotOutcome.IsTerminalFailure(), "fetch error after retries → terminal failure")
```

- [ ] **Step 2: Update `gmaps/job.go`**

Find the fetch-error branch in `Process()` (the block added by PR #54 starting around line 137). Replace the two-call pattern:

```go
// BEFORE:
if j.ExitMonitor != nil {
	j.ExitMonitor.IncrSeedCompleted(1)
	j.ExitMonitor.RecordSeedError(resp.Error)
}

// AFTER:
if j.ExitMonitor != nil {
	j.ExitMonitor.RecordSeedOutcome(exiter.SeedOutcome{
		Err:         resp.Error,
		RetriesLeft: 0, // Process is invoked AFTER retries — every fetch error here is terminal
		PlacesFound: 0,
	})
}
```

Find the SUCCESS branch later in `Process()` (`gmaps/job.go:248`) and similarly replace:

```go
// BEFORE:
if j.ExitMonitor != nil {
	j.ExitMonitor.IncrPlacesFound(len(next))
	j.ExitMonitor.IncrSeedCompleted(1)
}

// AFTER:
if j.ExitMonitor != nil {
	j.ExitMonitor.IncrPlacesFound(len(next)) // place-jobs counter is separate
	j.ExitMonitor.RecordSeedOutcome(exiter.SeedOutcome{
		Err:         nil,
		RetriesLeft: 0,
		PlacesFound: len(next),
	})
}
```

- [ ] **Step 3: Run gmaps tests**

```bash
go test ./gmaps/ -run "TestGmapJob_Process" -count=1 -v
```
Expected: PASS.

- [ ] **Step 4: Run exiter tests too** (the new one + the one in user_provisioning's path that exercises the seed counter — sanity check no regression)

```bash
go test ./exiter/ -count=1
PG_TEST_DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" \
  go test ./web/services/ -run TestProvision_Concurrent -count=1
```

- [ ] **Step 5: Commit**

```bash
git add gmaps/job.go gmaps/job_test.go
git commit -m "refactor(gmaps): use RecordSeedOutcome instead of split IncrSeedCompleted+RecordSeedError"
```

---

## Task 6 — Add goroutine-leak detection in webrunner test setup

**Files:**
- Create or modify: `runner/webrunner/webrunner_test.go` (the existing file is `webrunner_startup_test.go`; add a separate `webrunner_test.go` for `TestMain`)
- Modify: `go.mod`, `go.sum`

This is one defensive 5-line addition. New tests in webrunner that spawn goroutines (Phase 3 candidates) will fail their suite if any goroutine leaks. Cheap insurance.

- [ ] **Step 1: Add `goleak` as a direct dependency**

```bash
go get go.uber.org/goleak@latest
go mod tidy
```

- [ ] **Step 2: Create `runner/webrunner/webrunner_test.go`**

```go
package webrunner

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain installs a goroutine-leak guard for every test in this package.
// If any test (especially future ones in Phase 3 that exercise the
// scrapeJob coordinator) leaves a goroutine running, the package's tests
// fail loudly instead of silently leaking under -race.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
```

- [ ] **Step 3: Run the package suite to confirm no existing leaks**

```bash
go test ./runner/webrunner/ -count=1
```
Expected: green. If goleak reports a pre-existing leak, that's a real bug — fix it or add a targeted ignore (`goleak.IgnoreTopFunction("...")`) with a comment explaining why. Don't blanket-suppress.

- [ ] **Step 4: Commit**

```bash
git add runner/webrunner/webrunner_test.go go.mod go.sum
git commit -m "test(webrunner): goleak.VerifyTestMain — prevent goroutine leaks in package tests"
```

---

## Task 7 — End-to-end verification + open PR

**No code change.** Run a real scrape locally and verify the new behavior end-to-end.

- [ ] **Step 1: Start the local backend** with the merged changes

```bash
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" \
DATA_FOLDER="./tmp/data" \
  go run . -web
```

- [ ] **Step 2: Trigger a scrape that will fail** (e.g., temporarily set a wrong proxy in `.env`, OR use the dev frontend with the failing-proxy config)

- [ ] **Step 3: Verify the new logs**

In the backend output, you should see:
- `clerk_webhook_*` (already working)
- `gmap_seed_fetch_failed` (PR #54)
- `exit_done_all_seeds_terminally_failed` (this PR — fail-fast log)
- `job_scrape_failed` (this PR — replaces the prior `job_scrape_succeeded` lie)

- [ ] **Step 4: Verify timing**

Time from `gmap_seed_fetch_failed` to `job_scrape_failed`: should be **<5 seconds** (was 30s before this PR, was 61min before PR #54).

- [ ] **Step 5: Verify the DB row**

```bash
psql -d google_maps_scraper -c "SELECT id, status, failure_reason FROM jobs ORDER BY created_at DESC LIMIT 1"
```

Expected: `status=failed`, `failure_reason="Proxy connection failed"` (or whatever sanitizer mapped to).

- [ ] **Step 6: Push and open PR against `develop`**

```bash
git push -u origin <branch-name>
gh pr create --base develop --title 'refactor(webrunner): JobOutcome value-return + typed seed events' --body "$(cat <<'BODY'
## Summary
[Filled in at PR-creation time — see plan for full body]
BODY
)"
```

---

## Risk register

| Risk | Mitigation |
|---|---|
| `scrapeJob` signature change breaks an unseen caller | The function is only called from one site (`webrunner.go:633`). Build will fail loudly if anything else depended on the old `error` return. Verified by `go build ./...` in Task 3 step 4. |
| `classifyOutcome` ordering bug — wrong cause for an err combination | Pure-function table-driven test (Task 2) covers all 8 documented scenarios. Adding a 9th (e.g., a new `mate.Start` error) is a 5-line table-row + matching switch case. |
| Existing `IncrSeedCompleted` / `RecordSeedError` callers double-count | The new `RecordSeedOutcome` runs alongside (it doesn't *delegate from* the old methods — that would be a bigger change). Task 5 migrates the only known caller in our tree (`gmaps/job.go`). External callers via the `Exiter` interface are theoretical — search confirms none exist. |
| Pre-existing goroutine leaks fail Task 6's `goleak` setup | If they do, that's GOOD — it surfaces real bugs. Address them with a targeted `goleak.IgnoreTopFunction` only after writing a follow-up issue capturing the underlying leak. Don't blanket-suppress. |

## Definition of Done

- [ ] All 7 tasks committed in order, each task's tests passing
- [ ] `go build ./...` clean
- [ ] `go test ./runner/webrunner/ ./exiter/ ./gmaps/ -count=1 -race` green
- [ ] Manual E2E (Task 7) shows `job_scrape_failed` log + sanitized `failure_reason` + <5s fail-fast
- [ ] PR opened against `develop`, links to this plan
- [ ] Follow-up issues filed for the deferred items: errgroup migration, Exiter interface split, Phase 3 coordinator state machine
