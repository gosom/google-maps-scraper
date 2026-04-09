# Fix Premature Context Cancellation During Consent Popup Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the bug where scraping jobs fail after ~6 seconds with "scrapemate inactivity timeout / context canceled with 0 results" by removing premature seed-completion signals from error paths and adding a grace period to the exit monitor.

**Architecture:** Two-part fix — (A) remove `IncrSeedCompleted(1)` calls from all error/retry paths in `GmapJob.BrowserActions()` and `SearchJob.BrowserActions()` so retries can run, and (B) add a startup grace period to the exit monitor's `isDone()` to prevent premature exit when zero places are found within the first 30 seconds.

**Tech Stack:** Go, Playwright, scrapemate

---

## Root Cause

`IncrSeedCompleted(1)` is called on EVERY error return in `GmapJob.BrowserActions()` (lines 270, 289, 320, 335, 347, 356, 373) and `SearchJob.BrowserActions()` (line 122). When the consent popup click fails or the feed takes too long to load, the seed is marked "completed" before scrapemate can retry.

The exit monitor checks every 1 second: if `seedCompleted >= seedCount && placesFound == 0`, it calls `cancelFunc()` — killing the entire job context. Scrapemate's retry mechanism never gets a chance.

**Correct behavior:** Only call `IncrSeedCompleted` on the SUCCESS path (line 224 in `job.go`, lines 97+132 in `searchjob.go`), after places are actually found or the search definitively returns no results.

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `gmaps/job.go` | Modify lines 270, 289, 320, 335, 347, 356, 373 | Remove `IncrSeedCompleted` from error paths |
| `gmaps/searchjob.go` | Modify line 122 | Remove `IncrSeedCompleted` from navigation error path |
| `exiter/exiter.go` | Modify `isDone()` at line 273 | Add 30s grace period for zero-places-found check |

---

## Chunk 1: Solution A — Remove Premature IncrSeedCompleted

### Task 1: Remove IncrSeedCompleted from GmapJob error paths

**Files:**
- Modify: `gmaps/job.go:267-375` (7 error-path IncrSeedCompleted calls)

- [ ] **Step 1: Identify all error-path IncrSeedCompleted calls**

These are the lines to REMOVE (the `IncrSeedCompleted` call only, NOT the error handling):

```
Line 269-271: page.Goto error → REMOVE IncrSeedCompleted
Line 288-290: clickRejectCookiesIfRequired error → REMOVE IncrSeedCompleted
Line 319-321: waitForFeedWithFallback error → REMOVE IncrSeedCompleted
Line 334-336: page.Content error (single place) → REMOVE IncrSeedCompleted
Line 346-348: feed not found → REMOVE IncrSeedCompleted
Line 355-357: scroll error → REMOVE IncrSeedCompleted
Line 372-374: page.Content error (final) → REMOVE IncrSeedCompleted
```

Keep the SUCCESS path at line 224 intact:
```go
// Line 222-225 — KEEP THIS (success path after places found)
if j.ExitMonitor != nil {
    j.ExitMonitor.IncrPlacesFound(len(next))
    j.ExitMonitor.IncrSeedCompleted(1)
}
```

- [ ] **Step 2: Apply the changes**

For each error block, change from:
```go
if err != nil {
    resp.Error = err
    if j.ExitMonitor != nil {
        j.ExitMonitor.IncrSeedCompleted(1)  // REMOVE THIS LINE
    }
    return resp
}
```

To:
```go
if err != nil {
    resp.Error = err
    return resp
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./gmaps/...`
Expected: Clean build, no errors

- [ ] **Step 4: Commit**

```bash
git add gmaps/job.go
git commit -m "fix: remove premature IncrSeedCompleted from GmapJob error paths

IncrSeedCompleted was called on every error return in BrowserActions,
causing the exit monitor to think the seed was done before retries
could run. This triggered premature context cancellation with 0
results. Now only called on the success path after places are found."
```

### Task 2: Remove IncrSeedCompleted from SearchJob navigation error path

**Files:**
- Modify: `gmaps/searchjob.go:120-123`

- [ ] **Step 1: Remove IncrSeedCompleted from Goto error**

Change line 120-124 from:
```go
if err != nil {
    resp.Error = err
    if j.ExitMonitor != nil {
        j.ExitMonitor.IncrSeedCompleted(1)  // REMOVE THIS LINE
    }
    return resp
}
```

To:
```go
if err != nil {
    resp.Error = err
    return resp
}
```

Keep lines 131-133 (success path) intact:
```go
// KEEP — success path after response received
if j.ExitMonitor != nil {
    j.ExitMonitor.IncrSeedCompleted(1)
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./gmaps/...`
Expected: Clean build

- [ ] **Step 3: Commit**

```bash
git add gmaps/searchjob.go
git commit -m "fix: remove premature IncrSeedCompleted from SearchJob error path"
```

---

## Chunk 2: Solution B — Exit Monitor Grace Period

### Task 3: Add startup grace period to isDone()

**Files:**
- Modify: `exiter/exiter.go:248-280` (`isDone()` function)
- Modify: `exiter/exiter.go:47-51` (`New()` constructor)

- [ ] **Step 1: Add startTime field to exiter struct**

In `exiter/exiter.go`, add a `startTime` field to the struct (after line 41):

```go
type exiter struct {
    seedCount             int
    seedCompleted         int
    placesFound           int
    placesCompleted       int
    resultsWritten        int
    maxResults            int
    cancellationTriggered bool
    lastProgressTime      time.Time
    startTime             time.Time // When the exiter was created

    mu         *sync.Mutex
    cancelFunc context.CancelFunc
}
```

Update `New()` to initialize it:
```go
func New() Exiter {
    return &exiter{
        mu:               &sync.Mutex{},
        lastProgressTime: time.Now(),
        startTime:        time.Now(),
    }
}
```

- [ ] **Step 2: Add grace period to isDone() zero-places check**

In `isDone()`, modify the zero-places-found check (lines 273-280):

From:
```go
// 2b. Seeds are complete but no places were found.
if e.seedCount > 0 && e.seedCompleted >= e.seedCount && e.placesFound == 0 {
    slog.Debug("exit_done_zero_places_found", ...)
    return true
}
```

To:
```go
// 2b. Seeds are complete but no places were found.
// Grace period: wait at least 30s before declaring zero-results exit,
// giving retries time to succeed after transient errors (consent popup, network).
if e.seedCount > 0 && e.seedCompleted >= e.seedCount && e.placesFound == 0 {
    elapsed := time.Since(e.startTime)
    if elapsed < 30*time.Second {
        slog.Debug("exit_grace_period_active",
            slog.Int("seed_completed", e.seedCompleted),
            slog.Int("seed_count", e.seedCount),
            slog.Duration("elapsed", elapsed),
        )
        return false
    }
    slog.Debug("exit_done_zero_places_found",
        slog.Int("seed_completed", e.seedCompleted),
        slog.Int("seed_count", e.seedCount),
        slog.Int("places_found", e.placesFound),
    )
    return true
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./exiter/...`
Expected: Clean build

- [ ] **Step 4: Run existing tests**

Run: `go test ./exiter/...`
Expected: All pass (or no test files)

- [ ] **Step 5: Full build verification**

Run: `go build ./...`
Expected: Clean build, no errors anywhere

- [ ] **Step 6: Commit**

```bash
git add exiter/exiter.go
git commit -m "fix: add 30s grace period to exit monitor zero-places check

Prevents the exit monitor from canceling the job context within the
first 30 seconds when seedCompleted >= seedCount but placesFound == 0.
This gives scrapemate retries time to succeed after transient errors
like consent popup timeouts or slow page loads."
```

---

## Verification

After both chunks are complete:

- [ ] Run `go build ./...` — must pass
- [ ] Run `go vet ./...` — must pass
- [ ] Test manually: submit a scraping job and verify it survives consent popup handling
