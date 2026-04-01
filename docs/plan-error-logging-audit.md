# Error Logging & Wrapping Audit — Action Plan

**Date**: 2026-03-21
**Reference**: "Go errors: to wrap or not to wrap?" (March 2026)
**Codebase**: google-maps-scraper Go 1.25.4

---

## What Already Follows the Article (No Action Needed)

### 1. Structured JSON logging via slog — GOOD
The logger at `pkg/logger/logger.go` uses `slog.NewJSONHandler()` with rotating files. Grafana/Loki native. No changes needed.

### 2. snake_case message names everywhere — GOOD
All `slog.Error`, `slog.Debug`, `slog.Warn` calls use snake_case event names (`reserve_stock_failed`, `job_cost_estimation_failed`). Consistent with article and Loki best practices.

### 3. user_id on all handler-level error logs — GOOD (Phase 5 fix)
All `slog.Error` calls in `web/handlers/*.go` include `user_id`, `path`, `method`. Grafana searchability is already operational.

### 4. No `fmt.Printf` in production code — GOOD (Phase 5 fix)
All 43 `fmt.Printf` calls in `optimized_extractor.go` replaced with structured `slog`. Zero remaining in non-test `.go` files.

### 5. Domain sentinel errors defined — GOOD
- `models.ErrNotFound`
- `models.ErrAPIKeyNotFound`
- `models.ErrWebhookConfigNotFound`
- `models.ErrDeliveryNotFound`
- `web/auth.ErrInvalidAPIKey`

### 6. `%v` at external system boundaries — GOOD
Google Sheets (`pkg/googlesheets`), AWS Lambda (`runner/lambdaaws`), and scraping (`gmaps/reviews.go`) correctly use `%v` to avoid exposing third-party error types.

### 7. Generic error messages to HTTP clients — MOSTLY GOOD (Phase 1+5 fixes)
Billing, integration, auth, and web handlers all return generic messages. Remaining exceptions listed below.

---

## What Needs Action

### ACTION 1: Fix remaining `err.Error()` in HTTP responses
**Priority**: HIGH — Security/information leakage
**Article rule**: Never leak internal errors to clients

| File | Line(s) | Pattern | Fix |
|------|---------|---------|-----|
| `web/handlers/api.go` | ~55 | JSON decode `err.Error()` → client | Replace with `"Invalid request body"` |
| `web/handlers/api.go` | ~60 | Validation `err.Error()` → client | Keep — validation errors are user-facing field names (acceptable) |
| `web/handlers/api.go` | ~99 | Job validation `err.Error()` → client | Keep — same as above, user-correctable input errors |
| `web/handlers/api.go` | ~141 | Balance check `err.Error()` → client | Replace with `"Failed to check balance"` |
| `web/handlers/webhook.go` | ~136,264 | `"invalid webhook URL: " + err.Error()` | Replace with `"Invalid webhook URL"` (SSRF filter details leak) |

**Verdict**: Fix lines 55, 141 in api.go and 136, 264 in webhook.go. Lines 60 and 99 are acceptable — they return validation errors that help users fix their input (`"Keywords is required"`, etc).

### ACTION 2: Fix `slog.String("error", err.Error())` → `slog.Any("error", err)`
**Priority**: MEDIUM — Loses error chain and type info in logs
**Article rule**: Structured logging should preserve the full error

| File | Line(s) | Current | Fix |
|------|---------|---------|-----|
| `postgres/integration.go` | ~55,67 | `slog.String("error", err.Error())` | `slog.Any("error", err)` |

**Verdict**: Fix both. `slog.Any("error", err)` preserves the error type in JSON output, making Grafana queries on error types possible.

### ACTION 3: Add `job_id` to result writer error logs
**Priority**: MEDIUM — Missing context for debugging job failures
**Article rule**: Structured logging should include all relevant entity IDs

| File | Line(s) | Missing Field |
|------|---------|---------------|
| `postgres/resultwriter.go` | ~422,431,557,563 | `job_id` |

The result writer knows the `jobID` (it's a field on the struct or passed as parameter). These error logs should include it for Grafana searchability: `slog.String("job_id", jobID)`.

**Verdict**: Fix — add `job_id` to all result writer error logs.

---

## What Could Be Improved But Is Acceptable As-Is

### ACCEPTABLE 1: Heavy `%w` wrapping within the `postgres` package (~100+ instances)
**Article says**: Bare `return err` within a package; wrapping is noise.
**Our situation**: The `postgres` package has methods like `Get`, `Create`, `Update` that wrap with `fmt.Errorf("getting job: %w", err)` within the same package.
**Why it's acceptable**: The `postgres` package is large with many methods. The wrapping adds operation context (`getting`, `creating`, `updating`) that helps distinguish which DB call failed when multiple methods call the same underlying queries. The alternative (bare returns) would lose "which operation" context in a package with 15+ files.
**Verdict**: No action. The within-package wrapping adds value here because the package is large.

### ACCEPTABLE 2: Heavy `%w` wrapping within `billing/service.go` (~50+ instances)
**Same reasoning**: billing/service.go is 900+ lines with many transaction steps. The wrapping (`"failed to get balance: %w"`, `"failed to insert credit: %w"`) distinguishes which step in a multi-step transaction failed.
**Verdict**: No action. Transaction-step wrapping is valuable for debugging.

### ACCEPTABLE 3: Bare `return err` in `postgres/user.go`, `postgres/webhook.go`
**Article says**: Bare returns crossing package boundaries lose context.
**Our situation**: These are simple CRUD methods where the caller (handler) already knows what operation it was performing and logs with full context via `slog.Error`.
**Why it's acceptable**: The handler-level slog with `user_id`, `path`, `method` provides all the context needed for Grafana debugging. Adding wrapping at the repository level would be redundant with the handler's structured logging.
**Verdict**: No action. The slog pattern at the handler layer compensates.

### ACCEPTABLE 4: `%w` at DB boundary in `postgres/repository.go` instead of `%v`
**Article says**: Use `%v` at system boundaries to avoid exposing DB driver errors.
**Our situation**: The repository methods wrap with `%w`, which technically exposes `pgx` errors to callers.
**Why it's acceptable**: All callers (handlers) check for domain sentinels (`models.ErrNotFound`) or treat the error as opaque (log + return 500). No caller in the codebase does `errors.Is(err, pgx.SomeError)`. The risk is theoretical.
**Verdict**: No action now. If we ever add a caching layer in front of the DB, switch `%w` to `%v` in the repository layer at that time.

### ACCEPTABLE 5: Incomplete `sql.ErrNoRows` translation in `billing/service.go` and `config/config.go`
**Article says**: Translate DB errors to domain sentinels at the boundary.
**Our situation**: Billing checks `sql.ErrNoRows` and returns `fmt.Errorf("no payment found: %w")` rather than a sentinel. Config returns defaults on `sql.ErrNoRows`.
**Why it's acceptable**: Billing callers don't branch on "not found" — they treat all errors as 500. Config correctly falls back to defaults. No caller is broken by the missing sentinel.
**Verdict**: No action. Would be cleaner with sentinels but no functional impact.

### ACCEPTABLE 6: Validation errors exposed via `err.Error()` in api.go lines 60, 99
**Article says**: Never leak `err.Error()` to clients.
**Exception**: The `go-playground/validator` package returns human-readable field validation errors like `"Keywords is required"`. These are **intended** for the API consumer to fix their request. Hiding them behind a generic message would make the API unusable.
**Verdict**: No action. Validation errors are user-facing by design.

---

## Summary

| Category | Status | Action |
|----------|--------|--------|
| JSON structured logging | Already good | None |
| snake_case log messages | Already good | None |
| user_id on handler errors | Already good | None |
| No fmt.Printf in prod | Already good | None |
| Domain sentinels defined | Already good | None |
| %v at external boundaries | Already good | None |
| Generic HTTP error messages | Mostly good | **Fix 4 remaining leaks** |
| slog.String("error",...) | 2 instances wrong | **Fix 2 instances** |
| job_id on result writer logs | Missing | **Fix 4 log calls** |
| Within-package %w wrapping | Heavy but acceptable | None |
| Bare returns across boundaries | Few, compensated by slog | None |
| %w at DB boundary | Correct for now | Review if adding cache layer |
| sql.ErrNoRows translation | Incomplete in billing/config | None (no functional impact) |
| Validation errors to client | Intentional | None |

**Total actions needed**: 3 small fixes (10 lines of code total)
- 4 `err.Error()` leaks in HTTP responses
- 2 `slog.String("error",...)` → `slog.Any("error",...)`
- 4 missing `job_id` fields in result writer logs
