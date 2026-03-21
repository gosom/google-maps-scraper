# Phase 4 Performance Fixes — Code Reviews

**Date**: 2026-03-20
**Phase**: 4 — Performance
**Status**: Complete

---

## Table of Contents

1. [Fix 1: Remove per-result COUNT(*) query (DB-H1)](#fix-1-remove-per-result-count-query)
2. [Fix 2: Reset backoff on success in fetchJobs (DB-H7)](#fix-2-reset-backoff-on-success)
3. [Fix 3: Add LIMIT to unbounded queries (DB-H8+DB-H9)](#fix-3-add-limit-to-unbounded-queries)
4. [Fix 4: Only poll DB when job slots available (WR-M5)](#fix-4-only-poll-db-when-slots-available)
5. [Fix 5: Narrow querySelectorAll in JS extraction (IE-H1)](#fix-5-narrow-queryselectorall)
6. [Fix 6: Add connection timeouts on proxy dials (MP-M5)](#fix-6-add-connection-timeouts-on-proxy-dials)

---

## Fix 2: Backoff Reset — gopls Review
**gopls**: 0 diagnostics — no errors, warnings, or hints on `provider.go`
**Verdict**: NEEDS CHANGES (minor)

### Findings

**Backoff logic correctness (PASS with notes)**
- `currentDelay` initialised to `baseDelay` (50 ms) at line 172 — correct.
- On success (`len(jobs) > 0`, line 223): jobs are dispatched, then `currentDelay = baseDelay` resets the backoff at line 232 — **correct branch, correct placement** (after all jobs are sent, before next iteration).
- On empty results (`else if` branch, line 234): waits `currentDelay`, then doubles it with `time.Duration(float64(currentDelay) * float64(factor))`, capped at `maxDelay` (300 ms) — growth and cap are correct.
- Backoff sequence: 50 ms -> 100 ms -> 200 ms -> 300 ms (capped). Resets to 50 ms on any successful fetch. This is sound.

**Issue 1 — Indentation defect (line 233)**
`jobs = jobs[:0]` on line 233 is indented at one tab level instead of three, misaligned with the surrounding block. It is still syntactically inside the `if len(jobs) > 0` block (Go is brace-delimited, not indentation-sensitive), so **behaviour is correct**, but it will likely be flagged by `gofmt` and is misleading to readers. Should be re-indented to match line 232.

**Issue 2 — Redundant `else if` condition (line 234)**
`} else if len(jobs) == 0 {` is redundant. Since the slice length cannot be negative, when `len(jobs) > 0` is false the only possibility is `len(jobs) == 0`. This should be simplified to `} else {` for clarity.

**time.Duration usage (PASS)**
- `time.Duration(float64(currentDelay) * float64(factor))` is the idiomatic way to multiply a `Duration` by an integer factor when the factor could be non-integer in the future. No precision/drift issues — the intermediate `float64` multiplication of values in the nanosecond range is exact for powers of two up to extremely large values, well beyond `maxDelay`.
- `time.After(currentDelay)` is used correctly inside a `select` with `ctx.Done()`, preventing goroutine leaks on cancellation.

**No drift issues (PASS)**
- The delay is recomputed each iteration from `currentDelay` (not accumulated), so there is no timer drift.
- `time.After` creates a fresh timer each loop — no stale timer reuse.

### Summary
The backoff-reset-on-success logic is functionally correct and well-placed. Two minor cleanup items: fix the indentation on line 233 and simplify the redundant `else if` to `else`.

## Fix 1: COUNT Removal — gopls Review
**gopls**: Zero diagnostics on `resultwriter.go` — no type errors, no unused imports, no unreachable code.
**Verdict**: APPROVED

**Findings**:

1. **Initial count query (line 158)**: Correct. Uses parameterized `$1` with `r.jobID`, safe from SQL injection. Runs once at startup, scoped to the job via `WHERE job_id = $1`. Gracefully falls back to `totalWritten = 0` on error (line 161), which is the safe default — it may re-count a few duplicates but `ON CONFLICT DO NOTHING` handles that idempotently.

2. **In-memory counter incremented properly (lines 225, 244)**: Yes. `totalWritten += insertedCount` is updated after every batch save, both in the main loop (line 225) and the trailing flush (line 244). The counter is only incremented when `insertedCount > 0`, avoiding unnecessary exiter notifications.

3. **RowsAffected() usage (lines 568-575)**: Correct. `result.RowsAffected()` is called on the `sql.Result` from `tx.ExecContext` after `tx.Commit()`. This is valid — Go's `database/sql.Result` interface retains the value from execution time; it does not require an open transaction. With `ON CONFLICT DO NOTHING`, PostgreSQL correctly reports only actually-inserted rows via `RowsAffected`, making this an accurate count.

4. **int vs int64 consistency / overflow risk**: `totalWritten` is `int` (line 156), `insertedCount` is `int` (line 575, cast from `int64`). `GetMaxResults()` returns `int`, `IncrResultsWritten()` takes `int`. All types are consistent across the call chain. On 64-bit systems (all modern targets), `int` is 64-bit so no overflow risk. On 32-bit systems the max would be ~2.1 billion results, which is far beyond any realistic job size. No issue.

5. **Minor observation**: Batch size is hardcoded to 1 (line 151). This means one DB round-trip per result. This is intentional for precise max-results control but is worth noting as a throughput trade-off. The comment documents this clearly.

6. **Concurrency safety**: `totalWritten` is a local variable in `Run()`, not shared across goroutines, so no mutex is needed. Correct.

## Fix 3: LIMIT on Queries — gopls Review
**gopls**: Zero diagnostics on both `repository.go` and `job_file_repository.go` — clean.
**Verdict**: APPROVED

**Findings**:

1. **Default only applies when limit is 0**: Correct. In `repository.go` line 92-93 (`Select`) and line 346-347 (`GetDeletedJobs`), the guard is `if params.Limit == 0 { params.Limit = 1000 }`. This only fires when no limit was specified. Callers that pass explicit limits (e.g., `SelectPending` passes `Limit: 1` in `web/service.go:92`) are unaffected because their limit is non-zero.

2. **Existing pagination not broken**: The `LIMIT` clause at lines 127-129 uses `if params.Limit > 0`, which will always be true after the default is applied. The parameterized `$N` placeholder and `args` append are correct. The same pattern is replicated in `GetDeletedJobs` (lines 380-382). No regression.

3. **SQL injection safety**: All dynamic values use `$N` parameterized placeholders with `args` slices passed to `QueryContext`. The `argNum` counter is correctly incremented. String concatenation is only used for SQL keywords (`WHERE`, `AND`, `ORDER BY`, `LIMIT`) and `$N` placeholders — never for user-supplied values. Safe.

4. **job_file_repository.go**: `ListByJobID` (line 207) and `ListByUserID` (line 256) use hardcoded `LIMIT 1000` in `const` query strings. This is a simpler approach since these methods don't accept pagination parameters. The limit is baked into the SQL literal, so no injection vector. Correct.

5. **Limit value of 1000**: Reasonable default. Jobs table rows per user are unlikely to exceed this in normal usage. For the `All()` call in `web/service.go:44` which passes `Limit: 0` (zero-value of `SelectParams`), the default kicks in, preventing unbounded scans on the dashboard listing endpoint. This is the key protection.

6. **No OFFSET support**: Neither `Select` nor `GetDeletedJobs` support `OFFSET` for true pagination — they only cap with `LIMIT`. This is fine for the current use case (dashboard listing), but if cursor/page-based pagination is needed later, `OFFSET` or keyset pagination would need to be added. Not a blocker.

## Fix 4: DB Polling Backpressure (WR-M5) — gopls Review
**gopls**: 0 diagnostics (clean)
**Verdict**: APPROVED

**Findings**:
- The backpressure check at line 378 (`if len(jobSemaphore) >= cap(jobSemaphore)`) is correct and idiomatic Go. Using `len()` and `cap()` on a buffered channel is the standard pattern for semaphore-based backpressure.
- **Race safety**: `len()` on a buffered channel is safe in Go — it is a built-in operation that reads the channel's internal counter atomically. The value may be stale by the time it is acted upon, but this is acceptable here: the worst case is one extra `SelectPending` call when slots just filled, which is harmless since the `jobSemaphore <- struct{}{}` send on line 397 will simply block until a slot frees.
- The `jobSemaphore` channel is local to the `work()` function (created at line 368), only shared with goroutines spawned within the same function, so there are no unexpected concurrent writers.
- The `continue` on line 379 correctly skips the DB query and waits for the next ticker tick, avoiding unnecessary database load.
- **Go best practices**: This is the canonical way to implement semaphore backpressure in Go. The alternative (`select` with `default`) would be less clear. Using `len/cap` as a pre-check before an expensive I/O call (DB query) is a well-established optimization pattern.

## Fix 5: querySelectorAll Narrowing (IE-H1) — gopls Review
**gopls**: 0 diagnostics (clean)
**Verdict**: APPROVED

**Findings**:
- The old `querySelectorAll('*')` has been fully eliminated — grep confirms zero remaining instances in the file.
- The replacement selector at line 663 is: `'div[style], span[style], img, [class*="photo"], [class*="image"], [class*="gallery"], [style*="background"]'`. This is syntactically valid CSS. The comma-separated compound selector correctly targets:
  - `div[style]`, `span[style]` — elements most likely to carry inline `background-image`
  - `img` — standard image elements
  - `[class*="photo"]`, `[class*="image"]`, `[class*="gallery"]` — Google Maps class-name patterns for photo containers
  - `[style*="background"]` — catch-all for any element with inline background styles
- This is a major improvement over `querySelectorAll('*')` which forced `getComputedStyle()` on every DOM node — an O(n) layout/style recalculation per node. The narrowed selector reduces the working set by orders of magnitude on a typical Google Maps page (thousands of nodes down to dozens).
- The other `querySelectorAll` calls in the file (lines 525, 656, 672, 720, 786, 789, 790, 791, 901) are all already targeted selectors — no issues.
- **JS syntax**: The backtick-delimited template literal containing the JS is well-formed. No escaping issues between Go raw strings and JS.

## Fix 6: Proxy Dial Timeouts (MP-M5) — gopls Review
**gopls**: 0 diagnostics (clean)
**Verdict**: APPROVED

**Findings**:
- At line 342, `net.DialTimeout("tcp", address, 10*time.Second)` correctly replaces what was previously `net.Dial`. The signature `net.DialTimeout(network, address string, timeout time.Duration)` is used correctly with all three arguments.
- No remaining `net.Dial` calls exist in the file (grep confirmed zero matches for `net.Dial` excluding `DialTimeout`).
- **Both paths covered**: The `tryConnectToProxy` method (line 340) is the sole point of proxy connection. It is called from:
  1. `handleHTTPS` line 225 (direct path)
  2. `handleHTTP` line 289 (direct path)
  3. `tryFallbackProxies` line 360 (fallback path)
  All connection paths go through this single method, so the timeout applies universally.
- **10s timeout**: This is a reasonable value for proxy dial timeouts. It is long enough to accommodate slow network conditions or geographic distance to proxy servers, but short enough to fail fast when a proxy is truly down. Industry standard for proxy/upstream connect timeouts is typically 5-15s. The Go `net/http` default transport uses 30s for general dials, so 10s for a proxy intermediary is appropriately tighter.
- The `time` package is correctly imported at line 12.
- **Go best practices**: `net.DialTimeout` is the idiomatic way to add timeouts to raw TCP dials. The alternative (using a `net.Dialer` with `Timeout` field) is equivalent but more verbose for a simple case like this.

---

## Fix 2: Post-Review Fix Applied

Reviewer found two minor issues:
1. **Indentation defect** on line 233: `jobs = jobs[:0]` was misindented — fixed to 3 tabs
2. **Redundant `else if`**: `else if len(jobs) == 0` simplified to `else`

gopls: zero diagnostics after fix. **Updated verdict: APPROVED**.

---

## Phase 4 Summary

| Fix | Finding | Verdict |
|-----|---------|---------|
| 1. COUNT(*) removal | DB-H1 | **APPROVED** |
| 2. Backoff reset | DB-H7 | **APPROVED** (post-review fix) |
| 3. LIMIT on queries | DB-H8+H9 | **APPROVED** |
| 4. DB polling backpressure | WR-M5 | **APPROVED** |
| 5. querySelectorAll narrow | IE-H1 | **APPROVED** |
| 6. Proxy dial timeouts | MP-M5 | **APPROVED** |

All 6 fixes implemented, reviewed, and approved. gopls: zero diagnostics project-wide.

---

## Final Review: requesting-code-review Methodology

**Reviewer**: Claude (requesting-code-review skill)
**Scope**: All 7 files modified in Phase 4
**Date**: 2026-03-20

### Per-File Findings

#### 1. resultwriter.go

**Severity**: 1 Low, 1 Info

- **LOW — RowsAffected() called after tx.Commit() (line 568)**: The `sql.Result` from `tx.ExecContext` (line 555) is accessed via `result.RowsAffected()` *after* `tx.Commit()` (line 561). In Go's `database/sql`, the `Result` interface value is captured at execution time and remains valid after commit. This works correctly with `pgx` (the driver in use). However, it is unconventional — most codebases call `RowsAffected()` before `Commit()`. If the driver were ever swapped for one that invalidates the result after commit, this would silently return wrong data. **Verdict**: Acceptable but worth a comment explaining the ordering is intentional.

- **INFO — Batch size of 1 is a throughput bottleneck**: `maxBatchSize = 1` (line 151) means one DB round-trip per result. The comment documents this is intentional for precise limit control. However, the limit check (line 188) could equally work with larger batches — check `totalWritten + len(buff) >= maxResults` before flushing. This would allow batch sizes of 5-10 for significantly better throughput while maintaining limit accuracy. Not a bug, but a missed optimization.

- **PASS — Initial COUNT(*) query**: The startup count at line 158 runs once, is parameterized, and gracefully falls back to 0 on error. The `ON CONFLICT DO NOTHING` in the insert ensures the in-memory counter stays consistent even if duplicates appear. The counter is a local variable (not shared across goroutines), so no concurrency risk.

- **PASS — Limit check correctness**: The check `totalWritten >= maxResults` (line 188) runs *before* inserting the current entry. This means the entry that triggers the limit is dropped (not inserted). For batch size 1, this is precise. The `return nil` exits cleanly without error, which is the correct behavior for hitting a soft limit.

#### 2. provider.go

**Severity**: 0 issues (all previously identified issues were fixed)

- **PASS — Backoff reset**: `currentDelay = baseDelay` at line 232 correctly resets after successful job fetch, placed after all jobs are dispatched to the channel. The exponential backoff sequence (50ms -> 100ms -> 200ms -> 300ms cap) is sound.

- **PASS — Indentation and else-if**: Both issues from the prior review (misindented line 233, redundant `else if`) have been fixed. The code now reads cleanly.

- **PASS — Channel blocking safety**: The `p.jobc <- job` send on line 226 is wrapped in a `select` with `ctx.Done()`, preventing goroutine leaks if the consumer stops reading. The `time.After` in the backoff branch is also paired with `ctx.Done()`.

- **PASS — No timer leak**: `time.After` creates a one-shot timer each iteration. While it is not garbage-collected until it fires, the max delay of 300ms means at most one 300ms timer is outstanding at a time. No meaningful resource leak.

#### 3. repository.go

**Severity**: 1 Info

- **INFO — Default limit silently truncates**: When `params.Limit == 0`, the default of 1000 is applied silently. If a user has more than 1000 jobs, the API will return exactly 1000 with no indication that results were truncated. The caller (e.g., dashboard) has no way to know more exist. Consider returning a `hasMore` flag or total count header. Not a correctness bug for current usage but a UX concern.

- **PASS — No regression on SelectPending**: `SelectPending` in `web/service.go:92` passes `Limit: 1`, which is non-zero. The default-1000 guard does not fire. No behavioral change.

- **PASS — argNum counter correctness**: The `argNum` variable starts at 1 and is incremented for each dynamic condition. The `LIMIT` clause uses the correct `$N` placeholder at the current `argNum` value. Tested mentally with all combinations (no filters, status only, user only, both): all produce correct parameterized SQL.

#### 4. job_file_repository.go

**Severity**: 0 issues

- **PASS — Hardcoded LIMIT 1000**: Both `ListByJobID` (line 207) and `ListByUserID` (line 258) use `LIMIT 1000` in const query strings. This is appropriate — job files are few per job (typically 1-3 file types), so 1000 is extremely generous while still preventing unbounded scans.

- **PASS — No pagination needed**: Unlike the jobs table, job_files are an internal implementation detail (S3 upload tracking), not user-facing. 1000 is more than sufficient.

#### 5. webrunner.go (semaphore backpressure)

**Severity**: 1 Info

- **INFO — TOCTOU gap in semaphore check is benign**: The `len(jobSemaphore) >= cap(jobSemaphore)` check on line 378 is inherently racy — a slot could free between the check and the `continue`. This is documented as acceptable in the prior review and confirmed: the worst case is skipping one tick cycle (1 second) when a slot just freed, or doing one extra DB query when a slot just filled. The `jobSemaphore <- struct{}{}` blocking send on line 397 is the real concurrency gate. No bug.

- **PASS — SelectPending returns at most 1 job**: `web/service.go:92` passes `Limit: 1`, so the `for i := range jobs` loop (line 393) iterates at most once. The semaphore acquire on line 397 will block (not overflow) if the slot filled between the check and the send. Correct.

- **PASS — consecutiveErrors reset**: The counter resets to 0 on line 391 after a successful `SelectPending`, even if the returned slice is empty (no pending jobs). The threshold of 10 consecutive errors before returning a fatal error is reasonable.

#### 6. optimized_extractor.go (narrowed querySelectorAll)

**Severity**: 1 Low

- **LOW — Potential false negatives from narrowed selector**: The replacement selector `'div[style], span[style], img, [class*="photo"], [class*="image"], [class*="gallery"], [style*="background"]'` is a significant improvement over `querySelectorAll('*')`. However, if Google Maps ever uses `<a>`, `<button>`, or `<figure>` elements with `background-image` set via CSS class (not inline style), these would be missed. The `[style*="background"]` catch-all mitigates this for inline styles, but class-based backgrounds on non-div/span elements would be invisible. This is an acceptable trade-off given the massive performance gain, but worth monitoring if image extraction rates drop.

- **PASS — Other querySelectorAll calls unaffected**: Lines 525, 656, 672, 720, 786, 789-791, 901 all use targeted selectors already. No regressions.

- **PASS — JS syntax correctness**: The CSS selector string is valid. Comma-separated compound selectors are standard CSS2.1. No escaping issues between Go backtick strings and JavaScript template literals.

#### 7. proxy.go (net.DialTimeout)

**Severity**: 0 issues

- **PASS — Single connection point**: `tryConnectToProxy` (line 340) is the sole TCP dial point. All paths (direct HTTPS line 225, direct HTTP line 289, fallback line 360) go through it. The 10s timeout applies universally.

- **PASS — No deadlock in fallback path**: `tryFallbackProxies` holds `ps.mu.Lock()` (line 352) and calls `tryConnectToProxy` (line 360). `tryConnectToProxy` does NOT acquire any lock — it receives the proxy pointer as a parameter and only performs network I/O. No deadlock risk.

- **PASS — Timeout value**: 10 seconds is appropriate for proxy dial. Long enough for slow networks, short enough to fail fast for dead proxies. The fallback mechanism ensures the system recovers by trying other proxies.

- **NOTE — Blocking under lock**: `tryFallbackProxies` holds `ps.mu.Lock()` while performing network dials (up to 10s * N proxies). During this time, all other `handleHTTPS`/`handleHTTP` calls that reach the fallback path will block on `ps.mu.Lock()`. For the primary path (`handleHTTPS` line 220, `handleHTTP` line 284), only `RLock` is used, so normal requests proceed. This is acceptable — fallback is a rare error path, and serializing fallback attempts prevents thundering-herd proxy switching.

### Cross-Cutting Concerns

1. **Fix 1 + Fix 4 interaction — no conflict**: The result writer (Fix 1) runs inside the scrape job goroutine, while the semaphore backpressure (Fix 4) controls how many such goroutines are spawned. These are independent — the semaphore limits job-level concurrency, and the in-memory counter tracks result-level counts within a single job. No interaction.

2. **Fix 3 + Fix 4 interaction — positive synergy**: The default LIMIT 1000 on `Select` (Fix 3) prevents the `SelectPending` path from returning unbounded rows. `SelectPending` already uses `Limit: 1`, but the defensive default in `Select` adds a safety net if new callers are added without explicit limits. The semaphore check (Fix 4) avoids even hitting the DB. These compose well.

3. **Fix 6 + proxy fallback — timeout multiplication**: With N proxies, `tryFallbackProxies` can take up to `10s * (N-1)` worst-case (all proxies timing out). For 5 proxies, that is 40 seconds while holding the write lock. This is acceptable for an error path but worth noting for deployments with many proxy backends.

4. **Fix 5 (JS selector) is entirely client-side**: The narrowed `querySelectorAll` runs in the browser context via Playwright. It has zero interaction with any server-side fixes (1-4, 6). No cross-cutting risk.

### Remaining Performance Issues

1. **Batch size 1 in enhancedResultWriterWithExiter**: As noted, this is one DB round-trip per result. For jobs with thousands of results, this is significant overhead. A batch size of 5-10 with an adjusted limit check (`totalWritten + len(buff) >= maxResults`) would reduce DB round-trips by 5-10x while maintaining limit accuracy.

2. **No connection pooling configuration visible**: The `sql.DB` is passed in but no `SetMaxOpenConns`, `SetMaxIdleConns`, or `SetConnMaxLifetime` configuration is visible in these files. The default Go `sql.DB` pool is unlimited open connections, which could cause issues under load. (This may be configured elsewhere — not confirmed in scope.)

3. **fmt.Printf debug statements in optimized_extractor.go**: Lines 73, 83, 88, 95, 100, 644 use `fmt.Printf` for debug output. These should be behind a logger or removed before production. They write to stdout unconditionally, which can pollute logs and affect performance in high-throughput scenarios.

### Overall Assessment

| Severity | Count | Details |
|----------|-------|---------|
| Critical | 0 | — |
| High | 0 | — |
| Medium | 0 | — |
| Low | 2 | RowsAffected after Commit ordering; narrowed CSS selector potential false negatives |
| Info | 3 | Silent truncation at 1000; batch-size-1 throughput; TOCTOU gap (benign) |

**Overall Verdict**: **APPROVED**. All 7 files pass review. No correctness bugs found. Two low-severity observations are documented for future consideration. The fixes are well-implemented, do not regress existing behavior, and compose cleanly with each other.
