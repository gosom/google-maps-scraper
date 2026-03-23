# Security & Infrastructure Audit

**Date:** 2026-03-23
**Status:** Audit Complete

Audit of 20 common production-readiness concerns against our backend (google-maps-scraper-2) and frontend (google-maps-scraper-webapp).

---

## Summary

| # | Concern | Verdict | Action |
|---|---------|---------|--------|
| 1 | No rate limiting on API routes | SECURE | None |
| 2 | Auth tokens in localStorage | SECURE | None |
| 3 | No input sanitisation | SECURE | None |
| 4 | Hardcoded API keys in frontend | SECURE | None |
| 5 | Stripe webhooks no signature verification | SECURE | None |
| 6 | No database indexing | SECURE | None |
| 7 | No error boundaries in UI | SECURE | None |
| 8 | Sessions that never expire | DELEGATED | None (Clerk handles) |
| 9 | No pagination on queries | SECURE | Minor: paginate API key list |
| 10 | Password reset links don't expire | N/A | None (Clerk handles) |
| 11 | No env var validation at startup | PARTIAL | Plan: validate S3/Stripe in prod |
| 12 | Images uploaded to server | SECURE | None |
| 13 | No CORS policy | SECURE | None |
| 14 | Emails sent synchronously | N/A | None (no email sending) |
| 15 | No database connection pooling | SECURE | None |
| 16 | Admin routes with no role checks | GAP | Plan: add RBAC when admin UI added |
| 17 | No health check endpoint | SECURE | None |
| 18 | No logging in production | SECURE | None |
| 19 | No backup strategy | GAP | Plan: add automated pg_dump |
| 20 | No TypeScript on AI code | SECURE | None |

**Result: 14 secure, 3 not applicable, 3 need action.**

---

## Detailed Findings

### 1. Rate Limiting on API Routes

**Verdict: SECURE**

Comprehensive rate limiting implemented across all tiers.

| Tier | Limit | Burst | Where |
|------|-------|-------|-------|
| Public (per IP) | 3 req/s | 10 | `web/web.go:149` |
| Free API key | 2 req/s | 5 | `web/web.go:172` |
| Paid API key | 10 req/s | 30 | `web/web.go:172` |
| Session (web UI) | 5 req/s | 20 | `web/web.go:172` |

Implementation: `web/middleware/middleware.go:227-353` — token bucket algorithm with 10-minute TTL cleanup for idle entries. Returns HTTP 429 with `Retry-After` header.

---

### 2. Auth Tokens in localStorage

**Verdict: SECURE**

Authentication delegated to Clerk which uses httpOnly cookies. No manual token storage in localStorage or sessionStorage anywhere in the frontend codebase. Clerk's `ClerkProvider` wraps the entire app (`src/app/layout.tsx:6-7`). The backend validates Clerk JWTs server-side (`web/auth/auth.go:120-174`).

---

### 3. Input Sanitisation

**Verdict: SECURE**

**SQL injection:** All database queries use parameterized placeholders (`$1`, `?`). No string concatenation in SQL. Verified across `postgres/repository.go`, `web/sqlite/sqlite.go`, `web/services/concurrent_limit.go`, and all billing queries.

**Input validation:** Server-side validation at `web/utils/validation.go:18-83` enforces:
- Max 5 keywords, max 1000 results, max depth 20, max 9999 reviews
- Go struct tag validation via `validator` package (`web/handlers/api.go:23`)

**Frontend validation:** `src/components/dashboard/NewJobForm.tsx:36-95` validates all numeric inputs with range checks before submission.

---

### 4. Hardcoded API Keys in Frontend

**Verdict: SECURE**

- `.gitignore` properly excludes `.env` files in both repos
- Only `NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY` (public by design) is exposed to the browser
- `CLERK_SECRET_KEY`, `STRIPE_SECRET_KEY` are server-only
- No `sk_`, `whsec_`, or secret values found in `/src` source files

---

### 5. Stripe Webhook Signature Verification

**Verdict: SECURE**

`billing/service.go:130`:
```go
event, err := webhook.ConstructEvent(payload, signatureHeader, s.webhookSigningKey)
```

Returns 400 on invalid signature (line 131-134). Webhook signing key stored in environment variable, never hardcoded. Audit trail logging at line 136.

---

### 6. Database Indexing

**Verdict: SECURE**

43+ indexes across migration files covering all commonly queried fields:

| Index | Table | Columns | Migration |
|-------|-------|---------|-----------|
| `idx_jobs_status` | jobs | status, created_at | 000005 |
| `idx_jobs_user_id` | jobs | user_id | 000006 |
| `idx_jobs_user_not_deleted` | jobs | user_id WHERE deleted_at IS NULL | 000011 |
| `idx_results_job_id` | results | job_id | 000009 |
| `idx_results_user_id` | results | user_id | 000009 |
| `idx_results_user_job` | results | user_id, job_id | 000009 |
| `idx_billing_events_user_time` | billing_events | user_id, occurred_at | 000017 |
| `idx_billing_events_job_time` | billing_events | job_id, occurred_at | 000017 |
| `idx_api_keys_user_id` | api_keys | user_id | 000023 |

Includes composite indexes and conditional (WHERE) indexes for soft deletes.

---

### 7. Error Boundaries in UI

**Verdict: SECURE**

- `src/app/dashboard/error.tsx` — Next.js error boundary for dashboard routes with reset button
- `src/components/error-boundary.tsx` — Generic error boundary with auth error detection and recovery paths
- Error messages sanitized via `getUserErrorMessage()` to prevent information leakage
- 68+ try-catch instances across components

---

### 8. Sessions That Never Expire

**Verdict: DELEGATED TO CLERK**

Session management fully delegated to Clerk. Clerk JWTs have built-in expiry (configurable in Clerk dashboard). Backend validates JWT claims via `clerkhttp.RequireHeaderAuthorization()` (`web/auth/auth.go:120`). No custom session table or manual token management.

API keys do not expire by design (standard practice — users can revoke them). Last-used timestamp tracked for audit (`web/auth/auth.go:197-202`).

---

### 9. Pagination on Database Queries

**Verdict: SECURE (minor gap)**

All list endpoints enforce pagination:
- `postgres/repository.go:91` — default LIMIT 1000 on job queries
- `web/handlers/api.go:384-395` — results pagination with default limit 50, max 1000
- `web/handlers/billing.go:155` — billing history default limit 50
- `web/services/results.go:134,216` — `LIMIT $2 OFFSET $3` on all result queries

**Minor gap:** `postgres/api_key.go:94` — `ListByUserID` returns all API keys without LIMIT. Low risk since per-user API key count is naturally small, but should be paginated for correctness.

---

### 10. Password Reset Links

**Verdict: N/A**

No custom password reset. Authentication fully delegated to Clerk, which handles password resets with automatic token expiry.

---

### 11. Environment Variable Validation at Startup

**Verdict: PARTIAL — needs hardening for production**

**Validated at startup:**
- `CLERK_SECRET_KEY` required (`runner/webrunner/webrunner.go:66-68`)
- `API_KEY_SERVER_SECRET` minimum 32 bytes (`runner/webrunner/webrunner.go:81-82`)
- PostgreSQL DSN required (`runner/webrunner/webrunner.go:109-113`)
- DB connectivity ping with 10s timeout (`runner/webrunner/webrunner.go:143-150`)
- Production auth bypass blocked (`main.go:36-40`)
- Frontend: full Zod schema validation (`src/env.js:9-76`)

**Not validated (silent fallback):**
- `STRIPE_SECRET_KEY` — silently skipped if empty, billing silently unavailable
- `STRIPE_WEBHOOK_SECRET` — same
- S3 credentials — silently falls back to local storage
- `ALLOWED_ORIGINS` — defaults to localhost-only if missing (safe but undocumented)

**Risk:** App starts in production without Stripe or S3 and silently degrades. Should fail-fast when `APP_ENV=production` and these are missing.

---

### 12. Images Uploaded to Server

**Verdict: SECURE**

No direct user image uploads. The app scrapes images from Google Maps and stores results as CSV files. When S3 is configured (`runner/webrunner/webrunner.go:215-221`), files are uploaded to S3 with proper Content-Type. Falls back to local storage if S3 not configured (acceptable for dev, but see point 11 — should be required in prod).

---

### 13. CORS Policy

**Verdict: SECURE**

Whitelist-based CORS at `web/middleware/middleware.go:31-52`. Only origins in the `ALLOWED_ORIGINS` env var are reflected. Tests confirm unlisted and `null` origins are rejected (`web/middleware/middleware_test.go:15-88`). Applied to all routes via middleware chain (`web/web.go:250-262`).

---

### 14. Emails Sent Synchronously

**Verdict: N/A**

No email sending functionality. The `email` field in job config controls email *extraction* from scraped websites, not email sending. No SMTP, SendGrid, or SES integration exists.

---

### 15. Database Connection Pooling

**Verdict: SECURE**

Comprehensive connection pool configuration:

| Setting | Postgres | SQLite |
|---------|----------|--------|
| MaxOpenConns | 25 (env configurable) | 1 |
| MaxIdleConns | 10 (env configurable) | 1 |
| ConnMaxLifetime | 5 min | 30 min |
| ConnMaxIdleTime | 2 min | — |

Hard fail if `DB_MAX_OPEN_CONNS=0` to prevent unbounded pools (`runner/webrunner/webrunner.go:130-131`).

---

### 16. Admin Routes with No Role Checks

**Verdict: GAP — no RBAC system**

The app has no admin-specific routes and no role-based access control. All endpoints are user-scoped with ownership checks. "Admin access" is implemented as passing `userID=""` to bypass ownership in repository methods (e.g., `postgres/repository.go:37` — "Pass userID="" to bypass ownership check (admin access)").

**Current state:** No admin panel exists, so no immediate risk. But the ad-hoc `userID=""` pattern for admin access is not auditable and has no formal role enforcement.

**When needed:** Before adding any admin UI, implement:
- `role` column on users table (`admin`, `user`)
- Role-checking middleware
- Explicit admin route group with middleware enforcement

---

### 17. Health Check Endpoint

**Verdict: SECURE**

`GET /health` at `web/web.go:126`. No auth required. Returns DB connectivity status and version. Test coverage at `web/handlers/health_test.go`.

---

### 18. Logging in Production

**Verdict: SECURE**

Full structured logging via `log/slog` with JSON output (`pkg/logger/logger.go:32-45`):
- Configurable level via `LOG_LEVEL` env var
- Output to stdout, file, or both via `LOG_OUTPUT`
- Automatic log rotation by size (`LOG_MAX_SIZE_MB`, default 100MB)
- Automatic retention cleanup (`LOG_RETENTION_DAYS`, default 14 days)
- Component-level loggers (`NewWithComponent()`)
- Structured fields throughout: user_id, job_id, error, path, method

---

### 19. Database Backup Strategy

**Verdict: GAP — no automated backups**

No `pg_dump` scripts, no backup cron jobs, no WAL archiving, no backup documentation. The only backup is `.env` file backup in `deploy-staging.sh`. Database recovery relies entirely on whatever the hosting provider offers (managed database snapshots).

**Action needed:**
- Automated `pg_dump` on schedule (daily minimum)
- Backup verification (periodic restore test)
- WAL streaming to external storage for point-in-time recovery
- Document disaster recovery procedure

---

### 20. TypeScript on AI-Generated Code

**Verdict: SECURE**

Full TypeScript with strict mode:
- `tsconfig.json` — `"strict": true`, `"noUncheckedIndexedAccess": true`, `"checkJs": true`
- TypeScript 5.9.3 with all `@types/*` packages
- All components use `.tsx` with explicit typed props
- Typed API client with generics (`src/lib/api/api-client.ts`)
- ESLint + TypeScript parser for linting

---

## Action Plan

### Priority 1: Database Backup Strategy (Point 19)

**Risk:** Data loss from bad migration, operator error, or infrastructure failure.

Tasks:
- Add `pg_dump` backup script to `scripts/`
- Configure cron-based backup schedule (daily)
- Upload backups to S3 with retention policy (30 days)
- Document restore procedure in `docs/`
- Add backup verification step (monthly restore test)

### Priority 2: Env Var Validation in Production (Point 11)

**Risk:** App silently runs without Stripe/S3 in production.

Tasks:
- In `runner/webrunner/webrunner.go`, when `APP_ENV=production`:
  - Require `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` (fail-fast)
  - Require S3 credentials (fail-fast)
  - Require `ALLOWED_ORIGINS` (no silent localhost default)
- Log a clear startup summary of enabled/disabled features

### Priority 3: RBAC Preparation (Point 16)

**Risk:** Low now (no admin UI), but needed before admin features.

Tasks:
- Add `role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin'))` to users table
- Create `RequireRole(role string)` middleware
- Replace ad-hoc `userID=""` admin bypass with explicit role checks
- Gate future admin routes behind role middleware

### Low Priority: API Key Pagination (Point 9)

Tasks:
- Add LIMIT/OFFSET to `postgres/api_key.go:ListByUserID`
- Default limit 100 (sufficient for all realistic use cases)

---

## Out of Scope (Verified N/A)

| # | Concern | Why N/A |
|---|---------|---------|
| 10 | Password reset links | Clerk handles password resets |
| 14 | Sync email sending | No email sending functionality |
| 12 | Direct image uploads | No user uploads; S3 for scraped results |
