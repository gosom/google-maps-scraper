# Phase 2 Data Integrity Fixes — Code Reviews

**Date**: 2026-03-20
**Phase**: 2 — Data Integrity
**Status**: Complete

---

## Table of Contents

1. [Fix 1: Re-enable ON CONFLICT in result writers (DB-C3)](#fix-1-re-enable-on-conflict-in-result-writers)
2. [Fix 2: sync.Pool use-after-return in image extraction (IE-C1)](#fix-2-syncpool-use-after-return-in-image-extraction)
3. [Fix 3: scroll() int/float64 type assertion (SC-H2)](#fix-3-scroll-intfloat64-type-assertion)
4. [Fix 4: WithBrowserReuseLimit copy-paste bug (WR-C1)](#fix-4-withbrowserreuselimit-copy-paste-bug)
5. [Fix 5: Decimal types for money (BL-H1+BL-H2)](#fix-5-decimal-types-for-money)

---

## Fix 1: ON CONFLICT — gopls-lsp Review
**Verdict**: APPROVED

**gopls Diagnostics**: Both `resultwriter.go` and `fallback_resultwriter.go` report zero diagnostics (no errors, warnings, or unused code).

**Findings**:

1. **ON CONFLICT matches unique index**: All three ON CONFLICT clauses use `(cid, job_id)`, which exactly matches the unique index `idx_results_unique_per_job ON results(cid, job_id)` created in migration `000014_fix_results_unique_constraint.up.sql`. This is correct.

2. **Column/placeholder/args count is consistent**: The enhanced writers insert 38 columns, generate 38 placeholders per row (`$1`-`$38`), and append exactly 38 args per entry. No mismatch.

3. **Basic `resultWriter.batchSave` correctly omits ON CONFLICT**: It only inserts the `data` column (no `cid` or `job_id`), so the unique index does not apply. The comment on line 271 documents this intentionally.

4. **`RowsAffected()` after commit is valid**: In `batchSaveEnhancedWithCount`, `result` is captured from `tx.ExecContext` before commit. The `sql.Result` interface stores the affected row count at exec time, so reading it after commit is safe and returns the correct count (excluding ON CONFLICT skipped rows).

5. **Minor observations (non-blocking)**:
   - JSON marshal errors are silently discarded with `_` (13 occurrences per batch method). These would only fail on truly unserializable types (channels, funcs), which `gmaps.Entry` does not contain, so this is acceptable.
   - `fallbackResultWriter` uses `context.Background()` instead of the passed `ctx`, intentionally ignoring cancellation. This is documented but means graceful shutdown won't interrupt in-flight fallback writes.
   - `fallbackResultWriter` swallows insert errors and continues (lines 53-55, 64-66). This is by design for resilience but could silently lose data — acceptable for a fallback path.

**SQL Correctness**: The `DO NOTHING` strategy is appropriate here — duplicate results within the same job should be silently skipped rather than updated, since re-scraped data is identical.

## Fix 2: sync.Pool — gopls-lsp Review
**Verdict**: APPROVED

**gopls diagnostics**: 0 errors, 0 warnings — clean.

**Pool type safety**:
- `Get()` at line 215 uses a hard type assertion (`.([]BusinessImage)`). This is safe because `sync.Pool.New` is always set in `NewImageBufferPool()` (line 65) and unconditionally returns `make([]BusinessImage, 0, 50)`. A comma-ok assertion would be more defensive but is not required here.
- `Put()` at lines 219-223 correctly resets slice length to 0 (`images = images[:0]`) preserving capacity, then returns to pool. Correct.

**Slice copy correctness — no use-after-return**:
- **Success path** (lines 143-152): A fresh `resultCopy` is heap-allocated via `make`, data is `copy()`-ed, and the pool buffer is returned via `Put()`. The returned `ScrapeResult` references only `resultCopy`. No aliasing with the pooled buffer.
- **Error path with partial images** (lines 122-130): Same copy-then-return pattern. `imagesCopy` is allocated, data copied, pool buffer returned. `result.Images` is reassigned to the copy before returning. Correct.
- **Error path with no images** (lines 127-129): Pool buffer returned, `result.Images` set to `nil`. Correct.
- **Retry path** (lines 108-110): Pool buffer returned before recursive call. No leaked references.

**No unsafe defer**: The original bug (IE-C1) was a `defer pool.Put(buf)` that returned the buffer to the pool while it was still referenced by the returned `ScrapeResult`. The fix correctly removes all `defer`-based pool returns and uses explicit `Put()` calls at each exit point. The only `defer` in the function is `defer cancel()` (line 100) for the context timeout, which is correct and expected.

**Nil safety**: `Get()` always returns a non-nil slice (guaranteed by `Pool.New`). All `Put()` call sites pass a non-nil slice. No nil dereference risk.

**Minor note (unrelated to fix)**: The `contains()` helper at line 248 claims to be case-insensitive in its comment but performs literal byte comparison. This does not affect pool correctness.

## Fix 2: sync.Pool — requesting-code-review Review
**Verdict**: APPROVED

**File**: `gmaps/images/performance.go`

### Return Path Analysis

All four return paths in `processWithRetry` (lines 79-153) were traced:

| # | Path | Buffer acquired? | Buffer returned to pool? | Caller data safe? |
|---|------|-----------------|-------------------------|-------------------|
| 1 | Max retries exceeded (L81-85) | No — returns before `Get()` at L91 | N/A | N/A |
| 2 | Transient error, retry (L107-110) | Yes (L91) | Yes — `Put()` at L109 before recursion | No data escapes |
| 3 | Non-retryable error, partial (L114-132) | Yes (L91) | Yes — `Put()` at L125 or L128 | Copy made at L123-124; or set to nil at L129 |
| 4 | Success (L135-153) | Yes (L91) | Yes — `Put()` at L145 | Copy made at L143-144; new ScrapeResult returned at L147 |

### Recursive Retry Path (Path 2)
Correct. The pooled buffer is returned via `Put()` on line 109 **before** the recursive call on line 110. No reference to the pooled buffer survives past the `Put()`.

### Nil Safety in `Put()`
`Put()` (L219-223) does `images[:0]` then `pool.Put(images)`. If `images` were nil, `nil[:0]` is valid Go and yields nil, so `sync.Pool` would store nil. A subsequent `Get()` would type-assert nil to `[]BusinessImage` successfully (nil is a valid slice), but with capacity 0, defeating the pool's purpose. **However, this path is unreachable** — `Put()` is only ever called with the buffer from `Get()`, which always comes from `sync.Pool.New` (non-nil, cap 50). No call site passes nil.

### Shared Memory Check
No path remains where the caller and pool share a backing array:
- **Path 3** (partial results): explicit `copy()` at L123-124, then `Put()` of the original, then `result.Images` reassigned to the copy.
- **Path 4** (success): explicit `copy()` at L143-144, then `Put()` of the original, then a **new** `ScrapeResult` struct is returned with only the copy.
- If `append` on L118 or L138 triggers a reallocation (more than 50 images), the old backing array goes to `Put()` while the new one is used for the copy — still safe.

### Minor Observation (non-blocking)
The `contains` helper (L248-253) is documented as "case-insensitive" but performs exact byte comparison. This does not affect the pool fix but could cause `shouldRetryImmediately` to miss errors with different casing (e.g., "Timeout" vs "timeout").

### Conclusion
The fix correctly addresses the use-after-return vulnerability. Every path that acquires a pooled buffer either returns it to the pool after making a safe copy, or returns it before recursing. No shared backing arrays can escape to the caller.

## Fix 3: scroll() — requesting-code-review Review
**Verdict**: APPROVED

### 1. Verification of the fix

The fix at `/Users/yasseen/Documents/google-maps-scraper-2/gmaps/job.go` lines 531-535 correctly changes the type assertion from `scrollHeight.(int)` to `scrollHeight.(float64)`, then converts with `int(heightF)`:

```go
heightF, ok := scrollHeight.(float64)
if !ok {
    return cnt, fmt.Errorf("scrollHeight is not a number")
}
height := int(heightF)
```

This is the correct fix. JavaScript's `Number` type always deserializes as `float64` in Go when going through `interface{}` (Playwright's `page.Evaluate` returns `interface{}`). The old `.(int)` assertion would **always** fail at runtime, meaning `scroll()` never completed a single iteration -- it returned an error immediately on the first scroll attempt.

### 2. Could `scrollHeight` ever be nil?

Yes. If the JavaScript expression `el.scrollHeight` evaluates in a context where `el` is null (e.g., the selector `div[role='feed']` matched nothing), the JS would throw an error caught by the `if err != nil` check on line 527, so `scrollHeight` would not reach the type assertion. If the JS somehow resolved with `null`/`undefined`, Playwright maps that to Go `nil`. In that case, `nil.(float64)` would yield `ok == false`, and the function returns `fmt.Errorf("scrollHeight is not a number")` -- a reasonable error. No nil-panic risk exists because the code uses the comma-ok form (`heightF, ok := ...`).

### 3. Is `int(heightF)` safe for realistic scroll heights?

Yes. Scroll heights are pixel values, typically in the range of hundreds to tens of thousands. `float64` has 53 bits of mantissa precision, so all integers up to 2^53 are represented exactly. `int` on 64-bit Go is 64 bits. The `int()` conversion truncates toward zero, but `scrollHeight` is always a non-negative integer value from the DOM (no fractional part), so truncation is a no-op. Rounding is not a concern here.

### 4. Other `page.Evaluate` calls -- same bug pattern?

Searched all `page.Evaluate` calls across the codebase:

| File | Return type from JS | Assertion used | Bug? |
|---|---|---|---|
| `gmaps/job.go:526` (this fix) | `float64` (scroll height) | `.(float64)` then `int()` | Fixed |
| `gmaps/place.go:501` | `string` / complex object | Type switch (`string`, `[]byte`, `nil`, `default`) | No |
| `gmaps/images/optimized_extractor.go:523` | Discarded (`_, _`) | N/A | No |
| `gmaps/images/optimized_extractor.go:651` | Complex JS object (array of URLs) | Handled as `interface{}` | No |
| `gmaps/images/optimized_extractor.go:779` | `string` (URL) | Used as `interface{}` for debug print | No |
| `gmaps/images/optimized_extractor.go:887` | `boolean` (scrolledAny) | Used as `interface{}` for truthiness | No |
| `gmaps/images/extractor.go:218,534` | Discarded (`_, err`) | N/A | No |
| `gmaps/images/extractor.go:1457` | `boolean` | Used as `interface{}` for truthiness | No |
| `gmaps/images/performance.go:343` | `string` / complex object | Handled similarly to place.go | No |

No other call site asserts a JS number to Go `int`. The bug was unique to `scroll()`.

### 5. Does fixing scroll() change caller behavior?

The sole caller is `BrowserActions` at `job.go:371`:
```go
_, err = scroll(ctx, page, j.MaxDepth, scrollSelector)
```

Before the fix, `scroll()` always failed on the first iteration, returning `(1, error)`. The caller treated this as a fatal error (`resp.Error = err; return resp`), meaning search result pages never had their content scrolled -- only the initial viewport of results was captured.

After the fix, `scroll()` will now actually iterate up to `maxDepth` times, scrolling the feed and loading more results. This means:
- Jobs will return **significantly more results** (the intended behavior).
- Scrape time per job will increase (more scroll iterations + wait times).
- The `maxDepth` parameter now actually controls behavior rather than being dead code.

This is a **behavior-restoring** fix, not a behavior-changing one. The code was always intended to scroll; it simply never worked.

### Summary

The fix is correct, minimal, and uses the idiomatic comma-ok pattern to guard against unexpected types. No nil-panic risk. No truncation risk. No other call sites share this bug. The only behavioral impact is that scrolling now works as originally designed.

## Fix 4: BrowserReuseLimit — gopls-lsp Review
**Verdict**: APPROVED

**gopls Diagnostics**: All 4 runner files report zero diagnostics — no errors, warnings, or unresolved symbols.

| File | Line | `WithPageReuseLimit(2)` | `WithBrowserReuseLimit(200)` | Guard condition |
|---|---|---|---|---|
| `webrunner.go` | 978-979 | Present | Present | `!w.cfg.DisablePageReuse` |
| `filerunner.go` | 225-226 | Present | Present | `!r.cfg.DisablePageReuse` |
| `databaserunner.go` | 120-121 | Present | Present | `!cfg.DisablePageReuse` |
| `lambdaaws.go` | 175-176 | Present | Present | `!input.DisablePageReuse` |

**Findings**:

1. **All 4 runners are now consistent**: Each runner calls `WithPageReuseLimit(2)` followed by `WithBrowserReuseLimit(200)`, gated behind the same `DisablePageReuse` check. The lambda runner (reference implementation) was already correct; the other three now match it exactly.

2. **Function resolves in gopls**: `scrapemateapp.WithBrowserReuseLimit` produces zero diagnostics, confirming it is a real exported function in the `github.com/gosom/scrapemate v0.9.4` dependency. No compile error risk.

3. **Bug impact was significant**: Before the fix, the three non-lambda runners called `WithPageReuseLimit` twice — first with `2`, then with `200`. The second call likely overwrote the first, setting page reuse limit to 200 (far too high) while browser reuse limit was never set (defaulting to whatever the library default is). After the fix, pages are reused 2 times and browsers 200 times, as intended.

4. **No other configuration drift**: Checked all four runners' option-building blocks. Beyond the fixed line, the option sets are structurally consistent (each runner adds stealth mode, JS options, exit-on-inactivity, concurrency, and the reuse limits in the same order).

5. **Minor style difference (non-blocking)**: The lambda runner uses two separate `append` calls (lines 175-176) while the other three use a single `append` with a multi-line argument list. Functionally identical.

## Fix 4: BrowserReuseLimit — requesting-code-review Review
**Verdict**: APPROVED

**Summary**: The bug was a copy-paste error where `webrunner.go`, `filerunner.go`, and `databaserunner.go` all called `WithPageReuseLimit` twice (with values 2 and 200), instead of calling `WithPageReuseLimit(2)` then `WithBrowserReuseLimit(200)`. Only the lambda runner (`lambdaaws.go`) was correct. The fix changes the second call in all three files to `WithBrowserReuseLimit(200)`.

**Findings**:

1. **All 4 runners are now consistent**: Every runner sets the same pair of options when page reuse is enabled:
   - `lambdaaws.go:175-176` — `WithPageReuseLimit(2)`, `WithBrowserReuseLimit(200)`
   - `webrunner.go:978-979` — `WithPageReuseLimit(2)`, `WithBrowserReuseLimit(200)`
   - `filerunner.go:225-226` — `WithPageReuseLimit(2)`, `WithBrowserReuseLimit(200)`
   - `databaserunner.go:120-121` — `WithPageReuseLimit(2)`, `WithBrowserReuseLimit(200)`

2. **Values are reasonable**: Page reuse limit of 2 is aggressive (each page/tab is reused only twice before being discarded), which prevents stale DOM state from accumulating across scrapes. Browser reuse limit of 200 means the underlying Chromium process is recycled every 200 page creations, which prevents long-running browser memory leaks without paying the heavy startup cost too frequently. The 100:1 ratio between browser and page limits is sensible.

3. **Behavioral regression analysis**: Before the fix, the three non-lambda runners had `WithPageReuseLimit` called twice with values 2 then 200. The behavior depended on whether the second call overwrites the first (likely, since these are option functions that set a config field). If so, the effective page reuse limit was 200 (too high) and the browser reuse limit was never set (defaulting to unlimited or zero). After the fix:
   - Pages now rotate after 2 uses instead of 200 — this is **more aggressive** page recycling, which may slightly increase overhead but improves scrape reliability. This is the intended behavior matching the lambda runner.
   - Browsers now rotate after 200 page creations, where previously they never rotated. This **is** a behavioral change: browser processes will now be killed and restarted periodically. This is a positive change that prevents memory leaks, but operators should be aware that browser restarts will occur during long-running jobs.

4. **No risk of breakage**: Both `WithPageReuseLimit` and `WithBrowserReuseLimit` are imported from `github.com/gosom/scrapemate v0.9.4` (the `scrapemateapp` package). The lambda runner has been using these exact values in production, confirming they work correctly with this library version.

5. **Guard condition is correct**: All four runners gate these options behind `!DisablePageReuse` (or `!input.DisablePageReuse` for lambda), so users who opt out of page reuse are unaffected.

## Fix 3: scroll() — gopls-lsp Review
**Verdict**: APPROVED

**Diagnostics**: gopls reports zero diagnostics for `job.go` — no type errors, no unused variables, no issues.

**Type assertion correctness**: The fix correctly uses `.(float64)` instead of `.(int)` to assert the return value of `page.Evaluate()`. This is correct because Playwright's `Evaluate` returns JavaScript values, and JavaScript's `Number` type always maps to Go's `float64` through the JSON unmarshalling layer. A `.(int)` assertion would always fail at runtime since the underlying type is never `int`. The assertion includes a comma-ok guard (`heightF, ok := scrollHeight.(float64)`) so a type mismatch returns a clean error instead of panicking.

**int conversion safety (float64 to int truncation)**: `int(heightF)` on line 535 truncates the fractional part. For `scrollHeight`, which is a pixel count returned by the DOM, this is a non-negative integer value represented as float64, so truncation is lossless in practice. Scroll heights are always whole-pixel values.

**Overflow risk**: `scrollHeight` is a DOM pixel value. On a 64-bit system, `int` is 64-bit, so overflow is impossible for any realistic scroll height. On a 32-bit system, `int` is 32-bit (max ~2.1 billion pixels), which is still far beyond any real page. No overflow concern.

**Other `.(int)` assertions on `page.Evaluate` results**: None found in the file. The previous `.(int)` pattern has been fully replaced. No remaining instances of the same bug class.

**Minor observation (non-blocking, pre-existing)**: The `waitTime2` logic on lines 519-523 has `initialTimeout * cnt` compared against `initialTimeout`. On iteration 1 (cnt=1), 500*1=500 equals initialTimeout=500, so the cap does not trigger. On iteration 2+, it always caps to `maxWait2`. This is existing behavior unrelated to the fix.

## Fix 5: Decimal Money — gopls-lsp Review
**Verdict**: APPROVED

**gopls Diagnostics**: All 5 files report zero diagnostics — no type errors, no unused imports, no unresolved symbols.

| File | Diagnostics |
|---|---|
| `web/services/estimation.go` | 0 |
| `models/pricing_rule.go` | 0 |
| `postgres/pricing_rule.go` | 0 |
| `web/services/concurrent_limit.go` | 0 |
| `web/handlers/api.go` | 0 |

**Findings**:

1. **Micro-credit conversion is type-safe**: All conversions from float64 to int64 micro-credits use `int64(math.Round(x * 1_000_000))`, which correctly rounds before truncating to int64. The `creditsToMicro()` helper in `estimation.go` centralises this for the estimation service. The `microToCredits()` reverse conversion divides by `microUnit` (1,000,000) as a float64 constant, which is exact.

2. **CostEstimate struct preserves JSON backward compatibility**: All public fields (`ActorStartCost`, `PlacesCost`, `ContactDetailsCost`, `ReviewsCost`, `ImagesCost`, `TotalEstimatedCost`) remain `float64` with `json:"..."` tags. The new `totalMicro` field is unexported (lowercase) and has no JSON tag, so it is never serialised. Existing API consumers see no change in the response shape.

3. **`TotalMicro()` method exists and is used correctly**: Defined at `estimation.go:100` as a pointer receiver method on `*CostEstimate`. It is called in two places:
   - `estimation.go:310` — `CheckSufficientBalance` compares `balanceMicro < estimate.TotalMicro()` (int64 vs int64, correct).
   - `api.go:547` — `EstimateJobCost` handler uses `balanceMicro >= estimate.TotalMicro()` for the `sufficient_balance` response field (int64 vs int64, correct).

4. **Interface compliance**: `pricingRuleRepository` in `postgres/pricing_rule.go` implements `models.PricingRuleRepository` by returning `map[string]int64`. The interface definition in `models/pricing_rule.go:22` declares `GetActiveDefaultPrices(ctx context.Context) (map[string]int64, error)`. Return type matches exactly. The `NewPricingRuleRepository` function returns the interface type, confirming compile-time compliance.

5. **DB values scanned as text**: All three locations that read `credit_balance` from PostgreSQL cast to text in SQL (`COALESCE(credit_balance, 0)::text`) and scan into a Go `string`, then parse with `strconv.ParseFloat`. This avoids the `database/sql` driver's own float64 scanning, which could introduce rounding. The same pattern is used for `price_credits::text` in the pricing rule repository. This is the correct approach for NUMERIC/DECIMAL columns.

6. **Transactional balance check (concurrent_limit.go)**: The authoritative check at lines 105-118 converts both balance and cost to micro-credits under a `SELECT ... FOR UPDATE` lock. This eliminates the TOCTOU race. The conversion uses `int64(math.Round(opts.EstimatedCost * 1_000_000))` — note this converts from the float64 `EstimatedCost` field rather than passing micro-credits directly. This is a minor design observation: `EstimatedCost` was itself derived from micro-credits via `microToCredits(totalMicro)`, so the round-trip (int64 -> float64 -> int64) could theoretically lose precision for very large values. In practice, `float64` has 53 bits of mantissa, so all integers up to 2^53 (~9 quadrillion micro-credits, or ~9 billion credits) are represented exactly. No real-world job cost would approach this limit. Non-blocking.

7. **Magic number `1_000_000` duplicated across files**: The `microUnit` constant is defined in `estimation.go` but `concurrent_limit.go:110-111`, `postgres/pricing_rule.go:49`, `api.go:148`, and `api.go:541` all use the literal `1_000_000` instead. This is a minor maintainability concern — if the multiplier ever changed, these sites could be missed. Non-blocking, but a shared constant in the `models` package would be cleaner.

8. **No unused imports**: All imports in all 5 files are used. `math` is used for `math.Round`, `strconv` for `ParseFloat`, `database/sql` for DB access, etc. gopls confirms zero "unused import" diagnostics.

**Summary**: The micro-credit pattern is correctly implemented across all 5 files. Integer arithmetic eliminates IEEE 754 rounding in all monetary comparisons. The `CostEstimate` struct maintains full JSON backward compatibility. The only non-blocking observation is the duplicated `1_000_000` magic number across 4 files that could be extracted to a shared constant.

## Fix 5: Decimal Money — requesting-code-review Review
**Verdict**: APPROVED

### 1. Full flow trace: DB -> estimation -> balance comparison

The flow is clean and consistent:

1. **DB layer** (`postgres/pricing_rule.go`): `GetActiveDefaultPrices` casts `price_credits` to `::text` in SQL, scans as `string`, parses with `strconv.ParseFloat`, and converts to micro-credits via `int64(math.Round(f * 1_000_000))`. Returns `map[string]int64`.

2. **Model layer** (`models/pricing_rule.go`): `PriceCredits` is now `string` to avoid IEEE 754 at the model boundary. The repository interface returns `map[string]int64` (micro-credits), not `PricingRule` structs, so the string field is only used for storage/display, never for arithmetic.

3. **Estimation layer** (`web/services/estimation.go`): `loadPrices` caches prices as `map[string]int64` (micro-credits). `EstimateJobCost` performs all arithmetic in `int64` micro-credits. Only at the end does it convert back to `float64` for JSON-facing fields. The authoritative total is stored in the unexported `totalMicro` field, exposed via `TotalMicro()`.

4. **Balance check** (`estimation.go:CheckSufficientBalance`): Scans `credit_balance` as `::text`, parses to float, converts to micro-credits, compares `balanceMicro < estimate.TotalMicro()`. Integer comparison -- correct.

5. **Transactional check** (`concurrent_limit.go:CreateJobWithLimit`): Same pattern -- scans balance as `::text`, parses, converts with `int64(math.Round(balanceFloat * 1_000_000))`, compares `balanceMicro < costMicro`. Integer comparison -- correct.

6. **API handler** (`api.go:EstimateJobCost`): Balance fetched as `::text`, parsed, converted to micro-credits. `sufficient_balance` check uses `balanceMicro >= estimate.TotalMicro()`. Integer comparison -- correct.

7. **API handler** (`api.go:Scrape`, unlimited job guard): Balance fetched as `::text`, parsed, converted to micro-credits with `int64(math.Round(bFloat*1_000_000))`, compared against `unlimitedJobMinBalanceMicro` (int64 constant). Integer comparison -- correct.

### 2. Micro-credit conversion consistency

All conversion sites use the identical formula `int64(math.Round(value * 1_000_000))`:

| Location | Code |
|---|---|
| `estimation.go:70` | `creditsToMicro()` helper -- `int64(math.Round(credits * microUnit))` where `microUnit = 1_000_000` |
| `postgres/pricing_rule.go:49` | `int64(math.Round(f * 1_000_000))` -- hardcoded literal |
| `concurrent_limit.go:110-111` | `int64(math.Round(balanceFloat * 1_000_000))` and `int64(math.Round(opts.EstimatedCost * 1_000_000))` -- hardcoded literal |
| `api.go:148` | `int64(math.Round(bFloat*1_000_000))` -- hardcoded literal |
| `api.go:541` | `int64(math.Round(balanceFloat * 1_000_000))` -- hardcoded literal |

The multiplier is consistent (always `1_000_000`). **Non-blocking observation**: `postgres/pricing_rule.go`, `concurrent_limit.go`, and `api.go` use hardcoded `1_000_000` literals instead of the `microUnit` constant or the `creditsToMicro()` helper defined in `estimation.go`. This creates a maintenance risk -- if the multiplier ever changes, 5 locations must be updated instead of 1. However, since these are in different packages (`postgres`, `services`, `handlers`), sharing the helper would require moving it to a shared package. This is a refactoring opportunity, not a correctness issue.

### 3. Remaining float64 comparisons for money

**Within the 5 changed files**: No float64 comparisons for monetary values remain. All balance-vs-cost checks use `int64` micro-credits.

**Outside the 5 changed files** (not part of this fix, but noted for awareness):
- `billing/service.go:808-810`: Refund path scans `credit_balance` as `float64` directly (no `::text` cast), then performs `if actualDeduction > currentBalance` (float64 comparison at line 840). This is a potential issue for edge-case values but is outside the scope of this fix.
- `billing/service.go:248-249` and `416-417`: Same pattern for credit grant paths -- `float64` scan for balance logging/audit records. The actual balance updates use SQL `NUMERIC` arithmetic, so the float64 is only for transaction record `balance_before`/`balance_after` fields, not for decision-making.
- `web/auth/auth.go:265-266`: Scans balance as `float64` for signup bonus audit record. Update uses SQL `NUMERIC` arithmetic. Safe.

**Verdict on coverage**: The fix correctly targets all decision-making comparisons. Remaining float64 usages in billing/auth are either for logging or delegate comparison to PostgreSQL `NUMERIC`.

### 4. `creditsToMicro` edge case analysis

`int64(math.Round(credits * microUnit))`:

- **Rounding correctness**: `0.007 * 1e6 = 7000.0` (exact). `0.0005 * 1e6 = 500.0` (exact). `0.004 * 1e6 = 4000.0` (exact). All default prices convert exactly. For arbitrary DB values like `0.0033`, `0.0033 * 1e6 = 3299.9999999999995` in float64, and `math.Round` correctly produces `3300`. The rounding eliminates representation errors for values with up to 6 decimal places (matching the DB's `NUMERIC(18,6)` precision).
- **Overflow**: Max int64 is ~9.2e18. At 1e6 micro-credits per credit, this supports up to ~9.2 trillion credits. No realistic balance approaches this.
- **Sub-micro-credit precision loss**: Values with more than 6 decimal places (e.g., `0.0000001`) would round to 0. The DB schema uses `NUMERIC(18,6)`, so the DB itself only stores 6 decimal places. No precision loss possible.

### 5. JSON backward compatibility

The `CostEstimate` struct retains all `float64` fields with identical JSON tags. The new `totalMicro int64` field is unexported and tagless -- invisible in JSON. API responses are byte-identical for the same inputs.

The `EstimateJobCost` endpoint returns `estimate` (the `*CostEstimate` struct) plus `current_credit_balance` (float64) and `sufficient_balance` (bool). The balance is now parsed from string rather than scanned as float64, but `strconv.ParseFloat` produces identical float64 values for well-formed numeric strings. No visible change.

### 6. Callers of changed interfaces

- **`GetActiveDefaultPrices`**: Only called by `estimation.go:loadPrices()`. Return type changed to `map[string]int64`, and `loadPrices` was updated to match. No other callers exist.
- **`EstimateJobCost`**: Called in `api.go:104` (Scrape) and `api.go:513` (EstimateJobCost handler). Both access `.TotalEstimatedCost` (float64, unchanged) and `.TotalMicro()` (new). No breakage.
- **`PricingRule.PriceCredits`**: Changed from `float64` to `string`. No code outside `postgres/pricing_rule.go` accesses this field directly -- the repository returns `map[string]int64`, not `PricingRule` structs. No breakage.

### 7. The `JobLimitOpts.EstimatedCost` round-trip

In `api.go:160-163`, the estimate passes through `float64`:
```
totalMicro (int64) -> microToCredits -> EstimatedCost (float64) -> math.Round(x * 1e6) -> costMicro (int64)
```

This round-trip is lossless for all integers up to 2^53 (~9 quadrillion micro-credits = ~9 billion credits). A cleaner approach would pass `totalMicro` directly via an `int64` field on `JobLimitOpts`, but this is a style improvement, not a correctness issue.

### 8. Out-of-scope but flagged for future work

The `billing/service.go` refund path (lines 806-860) still uses `float64` for balance comparison (`actualDeduction > currentBalance`). This was not part of the fix scope (which targets the estimation/pre-flight flow), but should be addressed in a future pass for full consistency.

---

## Phase 2 Summary

### All Fixes Complete

| Fix | Finding | gopls-lsp | code-review | Final |
|-----|---------|-----------|-------------|-------|
| 1. ON CONFLICT | DB-C3 (CRITICAL) | **APPROVED** | **Fixed** post-review (SyncWriter added) | **APPROVED** |
| 2. sync.Pool | IE-C1 (CRITICAL) | **APPROVED** | **APPROVED** | **APPROVED** |
| 3. scroll() | SC-H2 (HIGH) | **APPROVED** | **APPROVED** | **APPROVED** |
| 4. BrowserReuseLimit | WR-C1 (CRITICAL) | **APPROVED** | **APPROVED** | **APPROVED** |
| 5. Decimal money | BL-H1+H2 (HIGH) | **APPROVED** | **APPROVED** | **APPROVED** |

### Files Modified

| File | Change |
|------|--------|
| `postgres/resultwriter.go` | Uncommented ON CONFLICT (cid, job_id) DO NOTHING in 2 methods |
| `postgres/fallback_resultwriter.go` | Replaced TOCTOU check-then-insert with ON CONFLICT DO NOTHING |
| `runner/webrunner/writers/synchronized_dual_writer.go` | Added ON CONFLICT + RowsAffected tracking + duplicate skip |
| `gmaps/images/performance.go` | Removed unsafe defer pool.Put, explicit copy-then-return on all paths |
| `gmaps/job.go` | Changed scroll() type assertion from int to float64 |
| `runner/webrunner/webrunner.go` | WithPageReuseLimit(200) → WithBrowserReuseLimit(200) |
| `runner/filerunner/filerunner.go` | Same copy-paste fix |
| `runner/databaserunner/databaserunner.go` | Same copy-paste fix |
| `web/services/estimation.go` | Micro-credits arithmetic, balance comparison as int64 |
| `models/pricing_rule.go` | PriceCredits float64 → string, return type map[string]int64 |
| `postgres/pricing_rule.go` | Scan as ::text, convert to micro-credits |
| `web/services/concurrent_limit.go` | Balance scan as string, comparison in micro-credits |
| `web/handlers/api.go` | Balance checks use micro-credits |

### Post-Review Fixes Applied
- **Fix 1**: `SynchronizedDualWriter` was missing ON CONFLICT — added with RowsAffected tracking and duplicate-skip logic
- **Fix 1**: Empty-string CID collision documented as known limitation (at most 1 empty-CID entry per job)

### Follow-Up Items (from reviewers, not blocking)
- `contains()` in performance.go claims case-insensitive but does exact comparison
- `1_000_000` micro-credit multiplier hardcoded in 4 files instead of shared constant
- `billing/service.go` refund path still uses float64 for balance comparison
- `JobLimitOpts.EstimatedCost` round-trips through float64 unnecessarily

### gopls Status
Zero diagnostics project-wide across all modified files.
