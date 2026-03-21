# Phase 5 Cleanup & Observability Fixes — Code Reviews

**Date**: 2026-03-20
**Phase**: 5 — Cleanup & Production Observability
**Status**: Complete
**Goal**: Grafana/Loki-ready structured logging with user traceability

---

## Table of Contents

1. [Fix 1: Replace 43 fmt.Printf with slog in optimized_extractor.go (IE-H4)](#fix-1-replace-fmtprintf-with-slog)
2. [Fix 2: Fix 7 http.Error leaking err.Error() in integration.go + web.go](#fix-2-fix-httperror-leaking-internal-errors)
3. [Fix 3: Remove dead code — createSchema, encjob, LogUsage, AppStateMethod (DB-M8+L3+AK-L7+IE-H2+H3)](#fix-3-remove-dead-code)
4. [Fix 4: Fix contains() case-insensitive bug in performance.go](#fix-4-fix-contains-case-sensitivity)
5. [Fix 5: Add user_id context to all error log paths for Grafana traceability](#fix-5-add-userid-context-to-error-logs)
6. [Fix 6: Shared micro-credit constant + billing refund float64 fix](#fix-6-shared-microcredit-constant)

---

## Fix 1: fmt.Printf → slog — Combined Review

**gopls**: Zero diagnostics on `gmaps/images/optimized_extractor.go`. No errors, warnings, or hints reported.

**Code Review Findings**:

1. **fmt.Printf completely eliminated**: Confirmed zero `fmt.Printf` or `fmt.Println` calls remain in the file. All 43 call sites replaced.
2. **Structured slog fields**: All 43 slog calls use structured fields (`slog.String`, `slog.Int`, `slog.Any`, `slog.Duration`) — no string concatenation in messages.
3. **snake_case convention**: All message keys and field names use snake_case consistently (e.g., `"trying_extraction_method"`, `"method_index"`, `"scroll_number"`). No camelCase or PascalCase field names found.
4. **Correct log levels**:
   - `slog.Debug` for operational tracing (scroll progress, element counts, extraction steps) — 39 calls
   - `slog.Warn` for recoverable failures (extraction method failed, tab processing failed) — 2 calls
   - No `slog.Error` or `slog.Info` in this file, which is appropriate since this is a library/extraction module, not a request handler
5. **No string concatenation in log messages**: Messages are static string literals; dynamic data is passed via structured fields.
6. **Import cleanup**: `"log/slog"` is imported; `"fmt"` remains only for `fmt.Errorf` (error wrapping), which is correct.

**Verdict**: APPROVED — Clean, consistent structured logging migration. All 43 replacements follow project conventions.

---

## Fix 2: http.Error Leaks — Combined Review

**gopls**: Zero diagnostics on both `web/handlers/integration.go` and `web/handlers/web.go`. No errors, warnings, or hints reported.

**Code Review Findings**:

1. **No err.Error() in HTTP responses**: Confirmed zero instances of `err.Error()` in either `integration.go` or `web.go`. All `http.Error()` calls use generic static messages (e.g., `"Failed to exchange token"`, `"Failed to encrypt access token"`, `"Job not found"`, `"CSV file not found"`).
2. **Server-side error logging preserved**: Every error path that sends a generic message to the client also logs the full error via `slog.Error` or `slog.Warn` with `slog.Any("error", err)`, preserving debuggability.
3. **Pattern consistency**: All 7 fixed instances in integration.go follow the same pattern:
   - `h.log.Error("event_name", ...structured fields..., slog.Any("error", err))`
   - `http.Error(w, "Generic message", http.StatusXxx)`
4. **web.go pattern**: Uses `h.Deps.Logger.Warn(...)` / `h.Deps.Logger.Error(...)` with nil-checks, consistent with the handler's dependency injection pattern.
5. **Remaining err.Error() in other files**: Note that `webhook.go`, `billing.go`, and `api.go` still contain `err.Error()` in HTTP responses (12+ instances). These were out of scope for Fix 2 but should be addressed in a future fix.

**Verdict**: APPROVED — All 7 targeted instances properly fixed. Generic messages to clients, full errors logged server-side. No information leakage in the scoped files.

---

## Fix 3: Dead Code Removal — Combined Review

**gopls**: Zero diagnostics on all 6 target files:
- `postgres/repository.go` — clean
- `postgres/provider.go` — clean
- `postgres/api_key.go` — clean
- `models/api_key.go` — clean
- `gmaps/images/optimized_extractor.go` — clean
- `gmaps/images/performance.go` — clean

**Code Review Findings**:

1. **createSchema removed from postgres/repository.go**: Confirmed absent. The comment on line 29 (`"We don't create schema here anymore since we're using migrations"`) documents the rationale. The only remaining `createSchema` in .go files is `web/sqlite/sqlite.go` which is a separate SQLite package that still actively uses it (line 227: `return db, createSchema(db)`). No breakage.
2. **encjob struct removed from postgres/provider.go**: Confirmed absent. The file contains only `job`-related types through `provider` and `decodeJob`. No references to `encjob` remain anywhere in .go source files.
3. **LogUsage / APIKeyUsageLog removed from models/api_key.go and postgres/api_key.go**: Confirmed absent. The `models/api_key.go` file contains only `APIKey` struct, `APIKeyRepository` interface, and `ErrAPIKeyNotFound`. No dead usage-logging code remains. `postgres/api_key.go` implements only the interface methods — no orphaned methods.
4. **AppStateMethod removed from gmaps/images/**: Confirmed absent. No references to `AppStateMethod` in any .go file.
5. **extractFromAppInitState removed from gmaps/images/**: Confirmed absent. No references in any .go file.
6. **No collateral damage**: Grep for all removed symbols (`createSchema`, `encjob`, `LogUsage`, `APIKeyUsageLog`, `AppStateMethod`, `extractFromAppInitState`) across all .go files returns only `web/sqlite/sqlite.go` for `createSchema` (which is a different, actively-used function in a different package). No compile errors from gopls on any file.
7. **performance.go intact**: `HybridImageExtractor` remains with `ExtractImagesHybrid` using only DOM-based extraction (via `ImageProcessor.ProcessBusiness`). The removed `AppStateMethod`/`extractFromAppInitState` were correctly identified as dead — they were never called by the hybrid extractor's actual code path.

**Verdict**: APPROVED — All 6 dead code items cleanly removed with no remaining references or broken dependencies. No false positives (nothing removed that was actually used).

---

## Fix 4: contains() — Combined Review

**gopls**: 0 diagnostics on `gmaps/images/performance.go`. No errors, warnings, or type issues.

**Code Review Findings**:

1. **Correctness**: The `contains()` function at line 249-251 correctly implements case-insensitive substring matching using `strings.Contains(strings.ToLower(s), strings.ToLower(substr))`. This replaces the prior custom `indexSubstring` approach with standard library calls, which is both simpler and correct.

2. **Retry pattern matching**: The `shouldRetryImmediately()` function (line 227-246) uses lowercase transient error strings (`"timeout"`, `"connection reset"`, etc.) and passes them through the case-insensitive `contains()` helper. This means error messages like `"Context Deadline Timeout"` or `"CONNECTION RESET"` will now match correctly, which was the original bug.

3. **Performance consideration**: `strings.ToLower()` allocates new strings on every call. Since this is only invoked on error paths (max 5 iterations over short strings), the allocation cost is negligible. No hot-path concern.

4. **No unused import**: The `"strings"` import was already present in the file (line 7), so no new dependencies were introduced.

**Verdict**: APPROVED -- Clean, correct fix. Standard library replaces custom logic. No edge cases or performance concerns on the error-only code path.

---

## Fix 5: user_id in Error Logs — Combined Review

**gopls**: 0 diagnostics across all 6 files (`api.go`, `webhook.go`, `apikey.go`, `billing.go`, `integration.go`, `auth/auth.go`). No type errors or unused imports.

**Code Review Findings**:

1. **internalError signature** (api.go:29): Updated to `func internalError(w http.ResponseWriter, log *slog.Logger, err error, userMsg string, extra ...slog.Attr)`. The variadic `...slog.Attr` parameter is backward compatible -- existing callers without extra attrs continue to compile (Go variadic rules). All callers now pass `slog.String("user_id", ...)`, `slog.String("path", ...)`, `slog.String("method", ...)` as extra attrs.

2. **Post-auth error paths verified** -- all `internalError` and direct `slog.Error`/`Logger.Error` calls include `user_id` after authentication:
   - `api.go`: GetJobs (259), GetUserJobs (281), GetJobResults (411), GetJobCosts (456), GetUserResults (486), EstimateJobCost (533,547,555), createJob (228,236), Scrape cost estimation (116)
   - `webhook.go`: ListWebhooks (67), CreateWebhook (143,164,184), UpdateWebhook (237,278), RevokeWebhook (314)
   - `apikey.go`: ListAPIKeys (54), CreateAPIKey (107,121,126), RevokeAPIKey (165)
   - `billing.go`: GetCreditBalance (44), GetBillingHistory (169)
   - `integration.go`: HandleGoogleCallback (133,141,158), HandleExportJob (233,285), HandleGetStatus (189)
   - `auth/auth.go`: failed_to_retrieve_user_from_clerk (135), user_has_no_email (153), failed to create user record (160), failed_to_grant_signup_bonus (166)

3. **Pre-auth error paths correctly omit user_id**:
   - `billing.go` HandleStripeWebhook (110,117,128): Uses package-level `slog.Error` with `path` and `method` only. This is correct -- Stripe webhooks are not user-authenticated requests, so no user_id is available.
   - Auth middleware rejection paths return 401 without logging user_id (no user to log).

4. **Minor observation**: In `billing.go` lines 110-129, the Stripe webhook handler uses the package-level `slog.Error()` rather than `h.Deps.Logger.Error()`. This is intentional (the handler may not have a logger instance for unauthenticated webhook endpoints) and includes `path`/`method` for traceability.

5. **auth/auth.go line 160**: Uses bare `slog.Error("failed to create user record", ...)` (package-level) rather than `m.logger.Error(...)`. Includes `user_id`, `path`, and `method`. Functionally correct, though stylistically inconsistent with the rest of the file which uses `m.logger`. Non-blocking.

**Verdict**: APPROVED -- All post-auth error paths include user_id, path, and method. Pre-auth paths correctly omit user_id. The internalError variadic signature is backward compatible. One minor style inconsistency (package-level vs instance logger in auth.go:160) is non-blocking.

---

## Fix 6: MicroUnit + Refund — Combined Review

**gopls**: 0 diagnostics across all 6 files (`models/pricing_rule.go`, `web/services/estimation.go`, `postgres/pricing_rule.go`, `web/services/concurrent_limit.go`, `web/handlers/api.go`, `billing/service.go`). No type errors.

**Code Review Findings**:

1. **Single source of truth**: `models.MicroUnit = 1_000_000` is defined at `models/pricing_rule.go:11` as an untyped integer constant. Grep confirms this is the only `1_000_000` literal in `.go` files (the only other occurrences are in `docs/phase2-fix-reviews.md` documentation). No `1000000` literals exist in `.go` files either.

2. **All conversion sites now use models.MicroUnit**:
   - `estimation.go:44`: `microUnit = models.MicroUnit` (package-level alias for brevity)
   - `estimation.go:68`: `creditsToMicro()` uses `microUnit`
   - `estimation.go:73`: `microToCredits()` uses `microUnit`
   - `postgres/pricing_rule.go:49`: `int64(math.Round(f * models.MicroUnit))`
   - `concurrent_limit.go:110-111`: `int64(math.Round(balanceFloat * models.MicroUnit))` and `int64(math.Round(opts.EstimatedCost * models.MicroUnit))`
   - `api.go:157`: `int64(math.Round(bFloat*models.MicroUnit))`
   - `api.go:560`: `int64(math.Round(balanceFloat * models.MicroUnit))`
   - `billing/service.go:824-825`: `int64(math.Round(balanceFloat * models.MicroUnit))` and `int64(math.Round(creditsToDeduct * models.MicroUnit))`
   - `billing/service.go:857,867`: Division by `models.MicroUnit` for back-conversion
   - `billing/service.go:891`: `20 * models.MicroUnit` for cap threshold

3. **Refund path now uses int64 micro-credits** (billing/service.go:808-901):
   - Balance fetched as `::text` from DB, parsed to float, converted to `balanceMicro` via `int64(math.Round(balanceFloat * models.MicroUnit))` (line 824)
   - Deduction amount converted to `deductMicro` the same way (line 825)
   - Cap comparison is `actualDeductMicro > balanceMicro` -- pure int64, no float64 comparison (line 851)
   - New balance computed as `newBalanceMicro := balanceMicro - actualDeductMicro` -- int64 subtraction (line 864)
   - Back-conversion to float64 only for DB storage and logging (lines 867-868)
   - The cap threshold uses `20 * models.MicroUnit` (line 891) rather than a magic number

4. **Remaining float64 in creditsToDeduct calculation** (line 785-788): `creditsToDeduct = (float64(charge.AmountRefunded) / float64(amountCents)) * creditsGranted`. This proportional calculation uses float64, but it is immediately converted to int64 micro-credits at line 825 before any comparison. The float64 intermediate is acceptable here since it represents a ratio calculation, not a monetary comparison.

5. **creditsGranted scanned as float8** (line 761): `credits_purchased::float8` is scanned into `var creditsGranted float64`. This is fine because credits_purchased is always a whole number (integer credits), so the float64 representation is exact for values up to 2^53.

**Verdict**: APPROVED -- The `models.MicroUnit` constant is the single source of truth with zero remaining magic number literals. The refund path correctly uses int64 micro-credits for all balance comparisons and deductions, eliminating the prior float64 comparison bug. Float64 is only used for intermediate ratio calculations and DB storage, never for monetary comparisons.

