# Rate-Limiting, Tier Billing, and Webhook Circuit Breaker Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three coordinated improvements to the API platform: (1) IETF-compliant + legacy `RateLimit-*` response headers on every API response so clients can self-throttle; (2) wire the paid-vs-free tier signal through auth → middleware so paid users actually receive the higher limits the middleware was already configured to grant; (3) add a webhook circuit breaker that auto-disables flapping receivers after N consecutive delivery failures.

**Architecture:** All three features share a small shared foundation — tier resolution happens once in the auth middleware (piggybacking on the existing user-row load) and propagates via `context.Context` to both the rate limiter (Phase 2) and the new response-header writer (Phase 3). The webhook circuit breaker (Phase 4) is independent of the API rate-limit work but uses the same TDD + migration cadence. Each phase produces working, deployable software on its own.

**Tech Stack:** Go 1.26, `database/sql` over `pgx/v5`, `golang.org/x/time/rate` token buckets, PostgreSQL 15+. Tests use stdlib `testing` + `net/http/httptest`. Migrations follow the existing `scripts/migrations/000NNN_*.{up,down}.sql` pattern.

---

## Background research (informs the values we'll choose)

| Source | Relevant detail |
|---|---|
| **IETF [draft-ietf-httpapi-ratelimit-headers-10](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-ratelimit-headers)** (Sept 2025) | Modern format: `RateLimit: "policy-name";r=50;t=30` (remaining, time-to-reset) and `RateLimit-Policy: "policy-name";q=100;w=10` (quota, window). Legacy `X-RateLimit-*` documented as deprecated but acknowledged in Appendix D. |
| **Stripe** | 100 ops/s baseline. Webhook auto-disable: **3 days** of continuous failures. Headers: `Stripe-Rate-Limited-Reason` + `Retry-After`. |
| **GitHub** | Per-token limits. Sends `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` (Unix timestamp), `X-RateLimit-Used`, `X-RateLimit-Resource`. |
| **Anthropic** | Long-form `anthropic-ratelimit-*` family of headers (one per limit type). |
| **Linear** | Quote-style `X-RateLimit-Requests-Limit/Remaining/Reset` + `X-RateLimit-Complexity-Limit/Remaining/Reset` for their GraphQL. |
| **Shopify** | Auto-disable after **8 consecutive failures** in 4 hours. |
| **GitLab** | **4** consecutive failures → temp disable, 40 total → permanent. |
| **WooCommerce** | **5** failures → disable. |
| **Reference implementation** ([InvokeBot](https://invokebot.com/research/webhook-reliability-patterns.html)) | `autoDisableAfter: 10` is the common starting point. |

### Decisions

- **Headers**: emit BOTH the modern `RateLimit` + `RateLimit-Policy` structured fields AND the legacy `X-RateLimit-Limit / -Remaining / -Reset` triplet on every authenticated response. The modern form is correct; the legacy form is what every existing HTTP client library still parses out of the box. Two extra bytes per request, zero downside.
- **Free-tier rate**: keep current **2 req/s, burst 5** (already conservative; no public complaints).
- **Paid-tier rate**: keep current **10 req/s, burst 30** (already configured; this phase wires it up).
- **Session-cookie rate**: keep current **5 req/s, burst 20**.
- **Tier definition** (from spec): `paid` = `users.total_credits_purchased > 0` (once paid, always paid — refunds do NOT demote).
- **Webhook auto-disable threshold**: **10 consecutive failed deliveries** — matches the InvokeBot reference implementation, more reactive than Stripe (3 days) and Shopify (8 in 4h). Each delivery attempt already retries 5 times internally with exponential backoff (cap 1h), so the counter only ticks up after a delivery has truly exhausted its retry budget. High-volume customers trip the breaker fast (good feedback); low-volume customers still trip before the endpoint silently rots.
- **Re-enable**: user-driven via dashboard `PATCH /webhooks/{id}` setting `health_state = 'healthy'` (also resets `consecutive_failures`). No automatic probe in v1.

---

## File structure

### Files to create

| Path | Responsibility |
|---|---|
| `scripts/migrations/000037_webhook_circuit_breaker.up.sql` | Add `consecutive_failures`, `health_state`, `disabled_at`, `disabled_reason` to `webhook_configs` |
| `scripts/migrations/000037_webhook_circuit_breaker.down.sql` | Reverse |
| `web/middleware/ratelimit_headers.go` | Header-writer wrapping a limiter snapshot; emits both modern + legacy `RateLimit-*` headers |
| `web/middleware/ratelimit_headers_test.go` | Tests for header emission |
| `web/middleware/tier_resolver_test.go` | Tier-resolution unit tests |
| `web/services/webhook_health.go` | Circuit-breaker state machine (record success, record failure, should-deliver?, disable, re-enable) |
| `web/services/webhook_health_test.go` | State-machine tests |
| `models/webhook_health.go` | `WebhookHealthState` enum + helpers |

### Files to modify

| Path | Change |
|---|---|
| `models/user.go` | Add `Tier string` field (`"free"` or `"paid"`) — populated in DB layer, not stored |
| `postgres/user.go` | Include `total_credits_purchased` in SELECT; map to `User.Tier` |
| `web/auth/auth.go` | Stash `Tier` into context (`UserTierKey`); add `GetUserTier(ctx)` accessor |
| `web/middleware/middleware.go` | (a) Extract limiter-snapshot accessor; (b) `PerAPIKeyRateLimit` reads `UserTierKey` instead of always picking free |
| `web/web.go` | Wire `RateLimitHeaders` middleware on apiRouter after each limiter |
| `models/webhook.go` | Add `WebhookConfig.HealthState`, `ConsecutiveFailures`, `DisabledAt`, `DisabledReason` |
| `postgres/webhook.go` | Update SELECT/INSERT/UPDATE queries to include new columns; add `RecordDeliverySuccess`/`RecordDeliveryFailure`/`Reenable` |
| `web/services/webhook_delivery.go` | Pre-delivery health check (skip if disabled); post-attempt call into `webhook_health` |
| `web/handlers/webhook.go` | `PATCH /webhooks/{id}` accepts `{"reenable": true}` to clear disabled state |
| `web/handlers/webhook_test.go` | Tests for re-enable handler |
| `api-reference/jobs.mdx` (docs repo) | Document `RateLimit-*` headers |
| `api-reference/authentication.mdx` (docs repo) | Update rate-limit section to mention tier + headers |
| `api-reference/webhooks.mdx` (docs repo) | Document `health_state` + auto-disable behaviour + re-enable flow |

---

## Phase 0: Audit (no code changes — for the executing engineer's context)

This phase is reading only. The executor should skim each file and verify the audit before starting Phase 1, since the audit drives every following decision.

| Area | Current state | Reference |
|---|---|---|
| **Rate-limiter machinery** | `keyRateLimiter` struct holds per-key `*rate.Limiter` in TTL-evicted map. `PerAPIKeyRateLimit(freeRate, freeBurst, paidRate, paidBurst, fallbackRate, fallbackBurst)` exists with three internal limiter pools but the dispatcher always picks `free` because `auth.GetAPIKeyPlanTier(ctx)` returns empty. No response headers written on success. On 429: `Retry-After: 1` + JSON body. | `web/middleware/middleware.go` lines 326–509 |
| **Current limits in production** | Public router: none. apiRouter: `PerAPIKeyRateLimit(2, 5, 10, 30, 5, 20)`. `POST /jobs`: extra `PerUserRateLimit(1, 3)`. `POST /support`: extra `PerUserRateLimit(1/60, 2)`. | `web/web.go` lines 245, 264, 285, 343 |
| **Auth + user load** | `Authenticate` middleware fetches `models.User` via `userRepo.GetByID(ctx, userID)` on every authenticated request and stores `userID`/`role` in context. No tier propagation. | `web/auth/auth.go` lines 95–222 |
| **User model** | `models.User` fields: `ID, Email, Role, StripeCustomerID, RefundDeficitCredits, CreatedAt, UpdatedAt`. **No tier field.** SELECT in `postgres/user.go` does not project `total_credits_purchased`. | `models/user.go` lines 15–34; `postgres/user.go` line 45 |
| **Tier signal source** | `users.total_credits_purchased` is a `NUMERIC(18,6)` column on the `users` table, incremented on `checkout.session.completed` (`billing/service.go` line 570) and decremented on refunds. **Per spec, refunds must NOT demote**, so we read `total_credits_purchased > 0` to determine tier but never store a denormalized flag. The credit_transactions ledger row with `type='purchase'` is the audit trail. | `scripts/migrations/000012_add_credit_system.up.sql` line 25; `billing/service.go` line 570 |
| **Webhook delivery loop** | `web/services/webhook_delivery.go` issues HTTP POST with 10s timeout, exponential backoff (2^attempt capped 1h), max 5 attempts. Failure handling: `handleRetry()` / `markFailed()`. No circuit breaker. `webhook_configs` table has `revoked_at` for user-driven disable but no system-driven disable column. | `web/services/webhook_delivery.go` lines 264–346; `scripts/migrations/000027_add_webhook_configs.up.sql` |
| **Latest migration number** | 000036. Next available: **000037**. Naming pattern: `000NNN_description.{up,down}.sql`. | `scripts/migrations/` |

---

## Chunk 1: Phase 1 — Tier resolution infrastructure (shared foundation)

This phase wires the paid-vs-free tier signal from the database through to a context key without changing any external behaviour. It is the prerequisite for Phases 2 and 3.

### Task 1: Add `Tier` field to the User model

**Files:**
- Modify: `models/user.go` (add field to the struct)

- [ ] **Step 1: Add the field**

Open `models/user.go` and add `Tier` to the `User` struct after `RefundDeficitCredits`. The field is computed by the DB layer; it is not persisted as its own column.

```go
// Tier is the user's billing tier ("free" or "paid"), computed from
// total_credits_purchased on each read. "paid" once total_credits_purchased
// > 0; never demotes on refunds (per product spec). Used by the API rate
// limiter to grant paid customers their higher quota.
Tier string
```

Constants in the same file (for type-safe comparisons):

```go
const (
	UserTierFree = "free"
	UserTierPaid = "paid"
)
```

- [ ] **Step 2: Commit**

```bash
git add models/user.go
git commit -m "models(user): add Tier field (computed, not persisted)"
```

### Task 2: Populate `Tier` from the database

**Files:**
- Modify: `postgres/user.go` (add `total_credits_purchased` to the SELECT in `GetByID`; map to `User.Tier`)

- [ ] **Step 1: Write the failing test**

Create `postgres/user_test.go` (or extend if it exists) with an integration test that inserts a user, sets `total_credits_purchased = 0`, calls `GetByID`, and asserts `Tier == UserTierFree`. Then update the row to `total_credits_purchased = 1.50`, call `GetByID` again, and assert `Tier == UserTierPaid`.

```go
func TestUserRepository_GetByID_TierResolution(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewUserRepository(db)

	const uid = "user_tier_test"
	mustExec(t, db,
		`INSERT INTO users (id, email, role, total_credits_purchased)
		 VALUES ($1, $2, 'user', 0)`,
		uid, "tier-test@example.com")

	u, err := repo.GetByID(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, models.UserTierFree, u.Tier)

	mustExec(t, db,
		`UPDATE users SET total_credits_purchased = 1.50 WHERE id = $1`, uid)

	u, err = repo.GetByID(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, models.UserTierPaid, u.Tier)
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./postgres/ -run TestUserRepository_GetByID_TierResolution -count=1
```

Expected: FAIL (`Tier` is always empty because we don't project the column yet).

- [ ] **Step 3: Update the SELECT statement and scan**

Find the `GetByID` query in `postgres/user.go`. Add `total_credits_purchased::text` to the projection (NUMERIC → text for exact decimal preservation, same pattern as `web/services/credit.go`). Add a scan target for a local `totalPurchasedStr` and convert to a tier string before returning.

```go
const q = `SELECT
	id, email, role, stripe_customer_id, refund_deficit_credits,
	created_at, updated_at,
	COALESCE(total_credits_purchased, 0)::text
FROM users WHERE id = $1`

var (
	u                  models.User
	totalPurchasedStr  string
)
if err := r.db.QueryRowContext(ctx, q, id).Scan(
	&u.ID, &u.Email, &u.Role, &u.StripeCustomerID, &u.RefundDeficitCredits,
	&u.CreatedAt, &u.UpdatedAt,
	&totalPurchasedStr,
); err != nil {
	// existing error handling
}

// Decimal-safe ">0" check: anything other than "0" or "0.<zeros>" means paid.
u.Tier = models.UserTierFree
if d, err := decimal.NewFromString(totalPurchasedStr); err == nil && d.GreaterThan(decimal.Zero) {
	u.Tier = models.UserTierPaid
}
```

Apply the same change to **every** other `User`-returning function in `postgres/user.go` (`GetByEmail`, `Create`, `Update`, etc.) so callers see a consistent `Tier`. Use `git grep "FROM users"` inside the postgres package as a checklist.

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./postgres/ -run TestUserRepository_GetByID_TierResolution -count=1
```

Expected: PASS.

- [ ] **Step 5: Run the full postgres + auth packages**

```bash
go test ./postgres/ ./web/auth/ -count=1
```

Expected: all PASS (no regressions in existing user-row tests).

- [ ] **Step 6: Commit**

```bash
git add postgres/user.go postgres/user_test.go
git commit -m "postgres(user): populate User.Tier from total_credits_purchased"
```

### Task 3: Propagate `Tier` through auth context

**Files:**
- Modify: `web/auth/auth.go` (add `UserTierKey`, `GetUserTier`, store in context next to `UserIDKey`)
- Test: `web/auth/auth_test.go`

- [ ] **Step 1: Add the context key + accessor**

In `web/auth/auth.go`, near the existing `UserIDKey` / `UserRoleKey` declarations:

```go
// UserTierKey holds the authenticated user's billing tier ("free" or "paid")
// for downstream consumers (rate limiter, response-header writer). Set by
// authenticateRequest after the user row is loaded; never empty for an
// authenticated request.
const UserTierKey contextKey = "user_tier"

// GetUserTier returns the user tier from the request context, or
// models.UserTierFree if absent. Callers must treat unset as free.
func GetUserTier(ctx context.Context) string {
	if v, ok := ctx.Value(UserTierKey).(string); ok && v != "" {
		return v
	}
	return models.UserTierFree
}
```

- [ ] **Step 2: Populate the context in both auth paths**

In `authenticateRequest`, the Clerk-JWT branch (around line 156) currently does:

```go
ctx := r.Context()
ctx = context.WithValue(ctx, UserIDKey, userID)
ctx = context.WithValue(ctx, UserRoleKey, dbUser.Role)
next.ServeHTTP(w, r.WithContext(ctx))
```

Add `UserTierKey` to it:

```go
ctx = context.WithValue(ctx, UserTierKey, dbUser.Tier)
```

The API-key branch (around line 202) currently calls `m.userRepo.GetByID` to fetch role. Do the same for tier — `apiUser.Tier` is already populated by the postgres layer:

```go
if apiUser, err := m.userRepo.GetByID(r.Context(), userID); err == nil {
	ctx = context.WithValue(ctx, UserRoleKey, apiUser.Role)
	ctx = context.WithValue(ctx, UserTierKey, apiUser.Tier)
}
```

- [ ] **Step 3: Write the failing test**

In `web/auth/auth_test.go`, add a `TestAuthenticate_PropagatesUserTier` that uses `httptest.NewRecorder` with a stub `userRepo` returning a user with `Tier=UserTierPaid`. Assert that the inner handler sees `GetUserTier(ctx) == UserTierPaid`.

```go
func TestAuthenticate_PropagatesUserTier(t *testing.T) {
	stubRepo := &stubUserRepo{user: &models.User{ID: "u1", Tier: models.UserTierPaid}}
	mw := newAuthMiddlewareForTest(t, stubRepo)

	var observedTier string
	handler := mw.Authenticate(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observedTier = auth.GetUserTier(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+testJWT(t, "u1"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, models.UserTierPaid, observedTier)
}
```

- [ ] **Step 4: Run it, verify it passes**

```bash
go test ./web/auth/ -run TestAuthenticate_PropagatesUserTier -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/auth/auth.go web/auth/auth_test.go
git commit -m "auth: propagate User.Tier via UserTierKey context value"
```

### Phase 1 acceptance gate

Before moving to Phase 2:

```bash
go build ./...
go test ./postgres/ ./web/auth/ ./web/handlers/ -count=1
```

All must be green. No production behaviour changes yet — Phase 1 is pure plumbing.

---

## Chunk 2: Phase 2 — Wire tier to the rate limiter

This phase makes `PerAPIKeyRateLimit` actually use the paid-tier limits for paid users.

### Task 4: Replace the always-free dispatcher

**Files:**
- Modify: `web/middleware/middleware.go` (the `PerAPIKeyRateLimit` body around lines 503–509)
- Modify: `web/middleware/middleware_test.go`

- [ ] **Step 1: Write the failing test**

In `web/middleware/middleware_test.go`, add a test that:
1. Builds the middleware with free=`rate.Limit(1)` burst 1, paid=`rate.Limit(100)` burst 10.
2. Fires 5 requests with `UserTierKey=paid` in context → all should return 200.
3. Fires 5 requests with `UserTierKey=free` (or empty) → only the first should succeed (burst=1), the rest 429.

```go
func TestPerAPIKeyRateLimit_PaidTierGetsHigherBucket(t *testing.T) {
	mw := PerAPIKeyRateLimit(
		rate.Limit(1), 1,    // free: 1/s burst 1
		rate.Limit(100), 10, // paid: 100/s burst 10
		rate.Limit(1), 1,    // session: 1/s burst 1
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))

	fire := func(tier, apiKeyID string) int {
		req := httptest.NewRequest("GET", "/", nil)
		ctx := context.WithValue(req.Context(), auth.UserTierKey, tier)
		ctx = context.WithValue(ctx, auth.APIKeyIDKey, apiKeyID)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(ctx))
		return rec.Code
	}

	// Paid user: 5 rapid requests, all 200 (burst 10 allows it).
	for i := 0; i < 5; i++ {
		require.Equal(t, 200, fire(models.UserTierPaid, "key-paid"))
	}

	// Free user: 5 rapid requests, first one 200, rest 429.
	require.Equal(t, 200, fire(models.UserTierFree, "key-free"))
	for i := 0; i < 4; i++ {
		require.Equal(t, 429, fire(models.UserTierFree, "key-free"))
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./web/middleware/ -run TestPerAPIKeyRateLimit_PaidTierGetsHigherBucket -count=1
```

Expected: FAIL — paid requests get the free bucket and are 429ed after request 2.

- [ ] **Step 3: Update the dispatcher**

In `web/middleware/middleware.go` find the tier-selection switch (currently using `auth.GetAPIKeyPlanTier` which always returns ""). Replace with `auth.GetUserTier`:

```go
tier := auth.GetUserTier(req.Context())
var krl *keyRateLimiter
switch tier {
case models.UserTierPaid:
	krl = paidKRL
default:
	krl = freeKRL // includes "free" and any unexpected value
}
```

For session-cookie auth where there is no API key ID, the existing fallback branch using `sessionKRL` keyed on user ID is kept untouched.

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./web/middleware/ -run TestPerAPIKeyRateLimit_PaidTierGetsHigherBucket -count=1
```

Expected: PASS.

- [ ] **Step 5: Run the broader suite**

```bash
go test ./web/middleware/ ./web/auth/ -count=1
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add web/middleware/middleware.go web/middleware/middleware_test.go
git commit -m "middleware: route paid users to paid limiter bucket"
```

### Task 5: Retire the unused `GetAPIKeyPlanTier`

The previous always-empty helper is now dead code. Keep it as a thin alias to `GetUserTier` for one release, then remove.

- [ ] **Step 1: Replace its body**

```go
// Deprecated: GetAPIKeyPlanTier is retained for one release to ease the
// transition from the old "plan tier on API key" model. New code should
// call GetUserTier instead. Will be removed in a future cleanup.
func GetAPIKeyPlanTier(ctx context.Context) string {
	return GetUserTier(ctx)
}
```

- [ ] **Step 2: Commit**

```bash
git add web/auth/auth.go
git commit -m "auth: deprecate GetAPIKeyPlanTier in favor of GetUserTier"
```

### Phase 2 acceptance gate

Manual smoke test instructions (against a local backend):

```bash
# Promote a test user to paid:
psql $DSN -c "UPDATE users SET total_credits_purchased = 5 WHERE id = 'TEST_USER';"

# Fire 20 requests in 2 seconds from that user's API key.
# Expect: all 200 (was 429 on request 6 before this phase).
for i in {1..20}; do
  curl -s -o /dev/null -w "%{http_code} " \
    https://api.brezelscraper.com/api/v1/jobs \
    -H "Authorization: Bearer bscraper_TEST_KEY"
  sleep 0.1
done
echo
```

---

## Chunk 3: Phase 3 — Standard `RateLimit-*` response headers

### Task 6: Build the limiter snapshot accessor

The header writer needs to read the current bucket state without consuming a token. Today the `*rate.Limiter` is hidden inside a closure.

**Files:**
- Modify: `web/middleware/middleware.go` (export a snapshot helper from the keyRateLimiter)
- Modify: `web/middleware/middleware_test.go` (test snapshot semantics)

- [ ] **Step 1: Add `Snapshot` to `keyRateLimiter`**

```go
// LimiterSnapshot is the rate-limit state at a point in time, suitable for
// rendering as RateLimit headers. Limit/Burst are the policy; Remaining is
// the integer floor of tokens currently in the bucket; ResetSeconds is the
// time until the bucket is fully refilled (0 if already full).
type LimiterSnapshot struct {
	Limit        rate.Limit // tokens per second (policy)
	Burst        int        // max tokens (policy)
	Remaining    int        // tokens currently available (rounded down)
	ResetSeconds int        // ceiling of seconds until bucket is full
}

// Snapshot returns the current state of the limiter for `key` WITHOUT
// consuming a token. Safe to call from a response-header writer that runs
// after the request has already been admitted.
func (k *keyRateLimiter) Snapshot(key string) LimiterSnapshot {
	lim := k.get(key)
	tokens := lim.Tokens()
	remaining := int(math.Floor(tokens))
	if remaining < 0 {
		remaining = 0
	}
	resetSeconds := 0
	if float64(k.b)-tokens > 0 {
		deficit := float64(k.b) - tokens
		resetSeconds = int(math.Ceil(deficit / float64(k.r)))
	}
	return LimiterSnapshot{
		Limit:        k.r,
		Burst:        k.b,
		Remaining:    remaining,
		ResetSeconds: resetSeconds,
	}
}
```

Note: `rate.Limiter.Tokens()` is exported and was added in Go 1.19 — no version bump required for Go 1.26.

- [ ] **Step 2: Write the test**

```go
func TestKeyRateLimiter_SnapshotDoesNotConsume(t *testing.T) {
	krl := newKeyRateLimiter(rate.Limit(5), 10, time.Minute)
	const key = "k"

	// Consume 3 tokens.
	for i := 0; i < 3; i++ {
		require.True(t, krl.get(key).Allow())
	}

	s := krl.Snapshot(key)
	require.Equal(t, rate.Limit(5), s.Limit)
	require.Equal(t, 10, s.Burst)
	require.GreaterOrEqual(t, s.Remaining, 6) // could be 7 with refill, but at least 6
	require.LessOrEqual(t, s.Remaining, 7)

	// A second snapshot immediately should give the same value (no token consumed).
	s2 := krl.Snapshot(key)
	require.Equal(t, s.Remaining, s2.Remaining)
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./web/middleware/ -run TestKeyRateLimiter_SnapshotDoesNotConsume -count=1
git add web/middleware/middleware.go web/middleware/middleware_test.go
git commit -m "middleware: add LimiterSnapshot accessor for header writer"
```

### Task 7: Expose snapshots from `PerAPIKeyRateLimit`

The header writer (next task) needs a way to ask "what's the snapshot for the limiter that just admitted this request?". The cleanest path: have `PerAPIKeyRateLimit` stash the snapshot into the request context after the Allow() check.

**Files:**
- Modify: `web/middleware/middleware.go`

- [ ] **Step 1: Add a context key for the snapshot**

```go
type ctxKey int

const (
	rateLimitSnapshotKey ctxKey = iota
)

// snapshotWithContext stores a LimiterSnapshot in a copy of ctx, suitable for
// downstream middlewares (e.g. RateLimitHeaders) to render. Internal: not
// exported because callers should consume snapshots only via the header
// writer, not by reaching into the limiter state directly.
func snapshotWithContext(ctx context.Context, s LimiterSnapshot) context.Context {
	return context.WithValue(ctx, rateLimitSnapshotKey, s)
}

// SnapshotFromContext returns the LimiterSnapshot the limiter middleware
// stored, or the zero value if none was set (e.g. on routes without a
// limiter — the header writer should skip emission in that case).
func SnapshotFromContext(ctx context.Context) (LimiterSnapshot, bool) {
	s, ok := ctx.Value(rateLimitSnapshotKey).(LimiterSnapshot)
	return s, ok
}
```

- [ ] **Step 2: Stash the snapshot in the dispatcher**

In `PerAPIKeyRateLimit`'s handler, after the tier-selected limiter has decided whether to allow the request, take a snapshot and attach it:

```go
allowed := krl.get(key).Allow()
snap := krl.Snapshot(key)

if !allowed {
	// Existing 429 response. Add Snapshot to context so future tooling can
	// log it; also write the headers inline since the 429 path bypasses
	// downstream middleware.
	writeRateLimitHeaders(w, snap)
	rateLimitJSON(w)
	return
}

req = req.WithContext(snapshotWithContext(req.Context(), snap))
next.ServeHTTP(w, req)
```

`writeRateLimitHeaders` is defined in the next task.

- [ ] **Step 3: Commit**

```bash
git add web/middleware/middleware.go
git commit -m "middleware: stash limiter snapshot in request context"
```

### Task 8: Build the header-writer middleware

**Files:**
- Create: `web/middleware/ratelimit_headers.go`
- Create: `web/middleware/ratelimit_headers_test.go`

- [ ] **Step 1: Write the failing test**

```go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	mw "github.com/gosom/google-maps-scraper/web/middleware"
)

func TestRateLimitHeaders_EmitsBothModernAndLegacy(t *testing.T) {
	// Outer: PerAPIKeyRateLimit. Inner: RateLimitHeaders (writes from ctx snapshot).
	limiter := mw.PerAPIKeyRateLimit(rate.Limit(10), 30, rate.Limit(10), 30, rate.Limit(10), 30)
	headers := mw.RateLimitHeaders()

	h := limiter(headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)

	// Modern structured field
	require.Equal(t, `"api";r=29;t=1`, rec.Header().Get("RateLimit"))
	require.Equal(t, `"api";q=30;w=3`, rec.Header().Get("RateLimit-Policy"))

	// Legacy triplet
	require.Equal(t, "30", rec.Header().Get("X-RateLimit-Limit"))
	require.Equal(t, "29", rec.Header().Get("X-RateLimit-Remaining"))
	require.NotEmpty(t, rec.Header().Get("X-RateLimit-Reset"))
}
```

- [ ] **Step 2: Run the test, verify it fails**

```bash
go test ./web/middleware/ -run TestRateLimitHeaders_EmitsBothModernAndLegacy -count=1
```

Expected: FAIL — middleware doesn't exist yet.

- [ ] **Step 3: Implement**

```go
package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// RateLimitHeaders writes IETF draft-ietf-httpapi-ratelimit-headers-10
// structured-field headers AND the legacy X-RateLimit-* triplet on every
// response. Must be installed AFTER the limiter middleware in the chain so
// the snapshot is in context by the time we run.
//
// Modern format (draft-10):
//   RateLimit-Policy: "api";q=30;w=3
//   RateLimit:        "api";r=29;t=1
//
// Legacy format (still parsed by most HTTP client libraries):
//   X-RateLimit-Limit:     30
//   X-RateLimit-Remaining: 29
//   X-RateLimit-Reset:     <unix epoch seconds when bucket is full>
func RateLimitHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if snap, ok := SnapshotFromContext(r.Context()); ok {
				writeRateLimitHeaders(w, snap)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeRateLimitHeaders is called from both the success path (via the
// RateLimitHeaders middleware) and the 429 path inside PerAPIKeyRateLimit.
// The "api" policy name is shared across tiers — the limit/burst differ but
// the policy label stays stable so clients can identify the same policy
// across requests.
func writeRateLimitHeaders(w http.ResponseWriter, s LimiterSnapshot) {
	if s.Limit == 0 && s.Burst == 0 {
		return // no limiter ran; nothing to advertise
	}

	// Window expressed as ceil(burst / rate) — the time it takes to refill a
	// fully-drained bucket. Minimum 1 to avoid w=0.
	window := 1
	if s.Limit > 0 {
		w := int(float64(s.Burst) / float64(s.Limit))
		if w > window {
			window = w
		}
	}

	// Modern (RFC draft-10): structured field list with one item, "api".
	w.Header().Set("RateLimit-Policy", fmt.Sprintf(`"api";q=%d;w=%d`, s.Burst, window))
	w.Header().Set("RateLimit", fmt.Sprintf(`"api";r=%d;t=%d`, s.Remaining, s.ResetSeconds))

	// Legacy headers (still expected by curl, k6, most HTTP clients).
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(s.Burst))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(s.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Duration(s.ResetSeconds)*time.Second).Unix(), 10))
}
```

- [ ] **Step 4: Run the test, verify it passes**

```bash
go test ./web/middleware/ -run TestRateLimitHeaders -count=1
```

Expected: PASS.

- [ ] **Step 5: Add a 429 case test**

```go
func TestRateLimitHeaders_AlsoEmittedOn429(t *testing.T) {
	limiter := mw.PerAPIKeyRateLimit(rate.Limit(1), 1, rate.Limit(1), 1, rate.Limit(1), 1)
	headers := mw.RateLimitHeaders()
	h := limiter(headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// First request consumes the only token.
	{
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		require.Equal(t, 200, rec.Code)
	}
	// Second is rate-limited; expect headers ON the 429 too.
	{
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		require.Equal(t, 429, rec.Code)
		require.Equal(t, "0", rec.Header().Get("X-RateLimit-Remaining"))
		require.NotEmpty(t, rec.Header().Get("RateLimit"))
	}
}
```

- [ ] **Step 6: Commit**

```bash
git add web/middleware/ratelimit_headers.go web/middleware/ratelimit_headers_test.go
git commit -m "middleware: emit IETF + legacy RateLimit headers on every response"
```

### Task 9: Wire the header middleware in `web/web.go`

**Files:**
- Modify: `web/web.go`

- [ ] **Step 1: Install `RateLimitHeaders` after each limiter chain**

The apiRouter's `Use(...)` block (line ~261) currently ends with `RequestLogger`. Add `RateLimitHeaders()` right after `PerAPIKeyRateLimit`:

```go
apiRouter.Use(
	webmiddleware.MaxBodySize(1<<20),
	webmiddleware.PerAPIKeyRateLimit(rate.Limit(2), 5, rate.Limit(10), 30, rate.Limit(5), 20),
	webmiddleware.RateLimitHeaders(), // ← NEW
	webmiddleware.RequestID,
	webmiddleware.InjectLogger(ans.logger),
	webmiddleware.RequestLogger(ans.logger),
)
```

- [ ] **Step 2: Manual smoke test against a local instance**

```bash
curl -i -H "Authorization: Bearer bscraper_..." \
  http://localhost:8080/api/v1/credits/balance | head -20
```

Expected output (subset):

```
HTTP/1.1 200 OK
RateLimit-Policy: "api";q=5;w=2
RateLimit: "api";r=4;t=1
X-RateLimit-Limit: 5
X-RateLimit-Remaining: 4
X-RateLimit-Reset: 1748XXXXXX
```

- [ ] **Step 3: Commit**

```bash
git add web/web.go
git commit -m "web: wire RateLimitHeaders middleware on the authenticated API router"
```

### Phase 3 acceptance gate

```bash
go test ./web/middleware/ -count=1
curl -i https://api.brezelscraper.com/api/v1/credits/balance \
  -H "Authorization: Bearer $TEST_KEY" \
  | grep -i ratelimit
```

Expected: all 5 header fields present (`RateLimit`, `RateLimit-Policy`, `X-RateLimit-Limit/Remaining/Reset`).

---

## Chunk 4: Phase 4 — Webhook circuit breaker

This phase is independent of Phases 1–3. It can be merged in either order.

### Task 10: Migration — add health-state columns to `webhook_configs`

**Files:**
- Create: `scripts/migrations/000037_webhook_circuit_breaker.up.sql`
- Create: `scripts/migrations/000037_webhook_circuit_breaker.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 000037_webhook_circuit_breaker.up.sql
--
-- Adds a circuit-breaker / health-state model to webhook_configs so the
-- delivery loop can auto-disable receivers that have been failing
-- consecutively. Existing rows are seeded as 'healthy' with 0 consecutive
-- failures (no behaviour change on rollout).

ALTER TABLE webhook_configs
	ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0
		CHECK (consecutive_failures >= 0),
	ADD COLUMN health_state TEXT NOT NULL DEFAULT 'healthy'
		CHECK (health_state IN ('healthy', 'degraded', 'disabled')),
	ADD COLUMN disabled_at TIMESTAMPTZ NULL,
	ADD COLUMN disabled_reason TEXT NULL;

-- Index to support the delivery-loop predicate
--   WHERE revoked_at IS NULL AND health_state != 'disabled'
-- on hot paths.
CREATE INDEX IF NOT EXISTS idx_webhook_configs_active
	ON webhook_configs (user_id)
	WHERE revoked_at IS NULL AND health_state != 'disabled';

COMMENT ON COLUMN webhook_configs.consecutive_failures IS
	'Running counter of non-2xx delivery attempts since the last 2xx. Reset to 0 on success.';
COMMENT ON COLUMN webhook_configs.health_state IS
	'healthy=delivering normally; degraded=>=5 consecutive failures (reserved for future banner UX); disabled=auto-disabled after 10 consecutive failures, user must re-enable.';
COMMENT ON COLUMN webhook_configs.disabled_at IS
	'When the circuit breaker tripped. NULL when health_state != ''disabled''.';
COMMENT ON COLUMN webhook_configs.disabled_reason IS
	'Short machine-readable reason (e.g. "10_consecutive_failures"). NULL when not disabled.';
```

- [ ] **Step 2: Write the down migration**

```sql
-- 000037_webhook_circuit_breaker.down.sql
DROP INDEX IF EXISTS idx_webhook_configs_active;

ALTER TABLE webhook_configs
	DROP COLUMN IF EXISTS disabled_reason,
	DROP COLUMN IF EXISTS disabled_at,
	DROP COLUMN IF EXISTS health_state,
	DROP COLUMN IF EXISTS consecutive_failures;
```

- [ ] **Step 3: Apply migration locally + verify**

```bash
psql $DSN -f scripts/migrations/000037_webhook_circuit_breaker.up.sql
psql $DSN -c "\d webhook_configs" | grep -E "consecutive_failures|health_state|disabled_at|disabled_reason"
```

Expected: all four columns visible with the documented defaults.

- [ ] **Step 4: Commit**

```bash
git add scripts/migrations/000037_webhook_circuit_breaker.up.sql scripts/migrations/000037_webhook_circuit_breaker.down.sql
git commit -m "migration: add health-state columns to webhook_configs (000037)"
```

### Task 11: Update model + repository

**Files:**
- Modify: `models/webhook.go` — add fields
- Modify: `postgres/webhook.go` — include in SELECT/UPDATE; add `RecordDeliverySuccess`, `RecordDeliveryFailure`, `Reenable`

- [ ] **Step 1: Add fields to `models.WebhookConfig`**

```go
// Health state from the circuit breaker. Defaults to "healthy" on creation.
// 'disabled' means our delivery loop will NOT attempt new deliveries until
// the user re-enables the webhook (or, eventually, until a health probe
// passes). Distinct from RevokedAt which is user-initiated revocation.
HealthState         string     // 'healthy' | 'degraded' | 'disabled'
ConsecutiveFailures int
DisabledAt          *time.Time
DisabledReason      *string
```

Add the constants too:

```go
const (
	WebhookHealthHealthy  = "healthy"
	WebhookHealthDegraded = "degraded"
	WebhookHealthDisabled = "disabled"

	// AutoDisableThreshold is the consecutive-failure count that trips
	// the circuit breaker. 10 was chosen to match the common reference
	// implementation (InvokeBot's webhook-reliability-patterns guide) and
	// to be more reactive than Stripe's "3 days of failures" / Shopify's
	// 8-in-4h. Each delivery attempt has its own 5-retry budget with
	// exponential backoff (cap 1h), so the counter only ticks up after a
	// delivery has truly given up.
	AutoDisableThreshold = 10

	// DegradedThreshold is reserved for a future UX banner ("this webhook
	// has been failing — check your endpoint"). No behavioural change yet.
	DegradedThreshold = 5
)
```

- [ ] **Step 2: Update all SELECT queries**

Every query in `postgres/webhook.go` that returns a `WebhookConfig` must project the new columns. Use `git grep "FROM webhook_configs"` to find all of them.

For the `Get`/`GetByID`/`ListByUserID` queries:

```go
const q = `SELECT id, user_id, name, url, encrypted_secret, resolved_ip,
	verified_at, created_at, updated_at, revoked_at,
	consecutive_failures, health_state, disabled_at, disabled_reason
FROM webhook_configs WHERE ...`
```

Add new fields to every `Scan` call. The compiler will catch every miss.

- [ ] **Step 3: Add state-mutating methods**

```go
// RecordDeliverySuccess clears consecutive_failures and restores health_state
// to 'healthy' for the given config. Idempotent: a no-op when the row is
// already healthy and the counter is already 0.
func (r *webhookConfigRepository) RecordDeliverySuccess(ctx context.Context, configID string) error {
	const q = `
		UPDATE webhook_configs
		SET consecutive_failures = 0,
		    health_state = 'healthy',
		    disabled_at = NULL,
		    disabled_reason = NULL,
		    updated_at = NOW()
		WHERE id = $1
		  AND (consecutive_failures > 0 OR health_state != 'healthy')`
	_, err := r.db.ExecContext(ctx, q, configID)
	return err
}

// RecordDeliveryFailure increments consecutive_failures atomically. When the
// threshold is crossed, health_state transitions to 'disabled'. Returns the
// new state so the caller can emit a notification if it just tripped.
func (r *webhookConfigRepository) RecordDeliveryFailure(ctx context.Context, configID, reason string) (newState string, justDisabled bool, err error) {
	const q = `
		UPDATE webhook_configs
		SET consecutive_failures = consecutive_failures + 1,
		    health_state = CASE
		        WHEN consecutive_failures + 1 >= $2 THEN 'disabled'
		        WHEN consecutive_failures + 1 >= $3 THEN 'degraded'
		        ELSE 'healthy'
		    END,
		    disabled_at = CASE
		        WHEN consecutive_failures + 1 >= $2 AND disabled_at IS NULL THEN NOW()
		        ELSE disabled_at
		    END,
		    disabled_reason = CASE
		        WHEN consecutive_failures + 1 >= $2 AND disabled_reason IS NULL THEN $4
		        ELSE disabled_reason
		    END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING health_state, (health_state = 'disabled' AND disabled_at = updated_at) AS just_disabled`
	err = r.db.QueryRowContext(ctx, q, configID,
		models.AutoDisableThreshold, models.DegradedThreshold, reason,
	).Scan(&newState, &justDisabled)
	return newState, justDisabled, err
}

// Reenable clears the disabled flag and resets the failure counter. Used by
// the dashboard "re-enable webhook" action and (eventually) a health-probe
// task.
func (r *webhookConfigRepository) Reenable(ctx context.Context, configID, ownerUserID string) error {
	const q = `
		UPDATE webhook_configs
		SET consecutive_failures = 0,
		    health_state = 'healthy',
		    disabled_at = NULL,
		    disabled_reason = NULL,
		    updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, configID, ownerUserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return models.ErrWebhookConfigNotFound
	}
	return nil
}
```

- [ ] **Step 4: Add tests**

In `postgres/webhook_test.go` (create if absent) or `postgres/webhook_integration_test.go`:

```go
func TestWebhookConfig_RecordDeliveryFailure_TripsAfter10(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWebhookConfigRepository(db)
	ctx := context.Background()
	cfgID := seedWebhookConfig(t, db, "user_1", nil)

	// 9 failures: stays not-disabled.
	for i := 0; i < 9; i++ {
		state, justDisabled, err := repo.RecordDeliveryFailure(ctx, cfgID, "test")
		require.NoError(t, err)
		require.NotEqual(t, models.WebhookHealthDisabled, state)
		require.False(t, justDisabled)
	}
	// 10th: trips.
	state, justDisabled, err := repo.RecordDeliveryFailure(ctx, cfgID, "test")
	require.NoError(t, err)
	require.Equal(t, models.WebhookHealthDisabled, state)
	require.True(t, justDisabled)

	// 11th: stays disabled but justDisabled = false.
	state, justDisabled, err = repo.RecordDeliveryFailure(ctx, cfgID, "test")
	require.NoError(t, err)
	require.Equal(t, models.WebhookHealthDisabled, state)
	require.False(t, justDisabled)
}

func TestWebhookConfig_RecordDeliverySuccess_ResetsCounter(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWebhookConfigRepository(db)
	ctx := context.Background()
	cfgID := seedWebhookConfig(t, db, "user_1", nil)

	for i := 0; i < 5; i++ {
		_, _, err := repo.RecordDeliveryFailure(ctx, cfgID, "test")
		require.NoError(t, err)
	}
	require.NoError(t, repo.RecordDeliverySuccess(ctx, cfgID))

	cfg, err := repo.GetByID(ctx, cfgID, "user_1")
	require.NoError(t, err)
	require.Equal(t, 0, cfg.ConsecutiveFailures)
	require.Equal(t, models.WebhookHealthHealthy, cfg.HealthState)
}
```

- [ ] **Step 5: Run + commit**

```bash
go test ./postgres/ -count=1
git add models/webhook.go postgres/webhook.go postgres/webhook_test.go
git commit -m "webhook: add circuit-breaker state mutations + tests"
```

### Task 12: Pre-delivery health check + post-attempt state update

**Files:**
- Modify: `web/services/webhook_delivery.go`

- [ ] **Step 1: Skip disabled configs at the top of the delivery loop**

Find the function that resolves the list of webhook configs to fan out to per job (likely `enqueueDeliveriesForJob` or similar). Add a filter:

```go
configs, err := s.configs.ListActiveByUserID(ctx, userID)
// ListActiveByUserID already excludes revoked_at IS NOT NULL.
// Additionally skip auto-disabled receivers.
active := make([]*models.WebhookConfig, 0, len(configs))
for _, c := range configs {
	if c.HealthState == models.WebhookHealthDisabled {
		s.log.Debug("webhook_delivery_skipped_disabled",
			slog.String("config_id", c.ID),
			slog.String("user_id", userID),
			slog.Int("consecutive_failures", c.ConsecutiveFailures))
		continue
	}
	active = append(active, c)
}
```

- [ ] **Step 2: Call `RecordDeliverySuccess` / `RecordDeliveryFailure` after each attempt**

Wherever the existing code calls `handleRetry()` / `markFailed()`, also call the new methods:

```go
resp, err := s.httpClient.Do(req)
switch {
case err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300:
	if rsErr := s.configs.RecordDeliverySuccess(ctx, cfg.ID); rsErr != nil {
		s.log.Warn("webhook_record_success_failed", slog.Any("error", rsErr), slog.String("config_id", cfg.ID))
	}
	// existing success path

default:
	reason := failureReason(resp, err) // small helper: returns "5xx", "timeout", "dns", etc.
	newState, justDisabled, rfErr := s.configs.RecordDeliveryFailure(ctx, cfg.ID, reason)
	if rfErr != nil {
		s.log.Warn("webhook_record_failure_failed", slog.Any("error", rfErr), slog.String("config_id", cfg.ID))
	}
	if justDisabled {
		s.log.Warn("webhook_auto_disabled",
			slog.String("config_id", cfg.ID),
			slog.String("user_id", cfg.UserID),
			slog.String("reason", reason))
		// Fire-and-forget email notification (see Task 13).
		go s.notifyWebhookDisabled(context.Background(), cfg, reason)
	}
	_ = newState
	// existing failure path (handleRetry / markFailed)
}
```

- [ ] **Step 3: Add tests**

In `web/services/webhook_delivery_test.go` (create or extend) add a table-driven test that:
1. Stubs the receiver to return 500.
2. Calls `Deliver(ctx, job)` 10 times.
3. Asserts the 11th call short-circuits (no HTTP request made — verifiable via a counter on the stubbed transport).

- [ ] **Step 4: Run + commit**

```bash
go test ./web/services/ -count=1
git add web/services/webhook_delivery.go web/services/webhook_delivery_test.go
git commit -m "webhook: circuit-breaker pre-check + post-attempt state update"
```

### Task 13: Auto-disable email notification

**Files:**
- Modify: `web/services/webhook_delivery.go` (add `notifyWebhookDisabled`)
- Modify: `pkg/notify/` or wherever the existing email sender lives

- [ ] **Step 1: Add a single-use helper**

```go
// notifyWebhookDisabled emails the webhook's owning user that we have
// auto-disabled their endpoint. Best-effort: failures are logged but don't
// surface — the user can also see the disabled state in the dashboard.
func (s *Service) notifyWebhookDisabled(ctx context.Context, cfg *models.WebhookConfig, reason string) {
	user, err := s.users.GetByID(ctx, cfg.UserID)
	if err != nil {
		s.log.Warn("notify_disabled_user_lookup_failed", slog.Any("error", err), slog.String("user_id", cfg.UserID))
		return
	}
	subject := "Your BrezelScraper webhook has been auto-disabled"
	body := fmt.Sprintf(`Hi,

We auto-disabled your webhook "%s" (%s) because it has failed %d consecutive deliveries (most recent reason: %s).

Re-enable it once your endpoint is fixed:
  https://brezelscraper.com/dashboard/integrations

— BrezelScraper`, cfg.Name, cfg.URL, models.AutoDisableThreshold, reason)

	if err := s.mailer.Send(ctx, user.Email, subject, body); err != nil {
		s.log.Warn("notify_disabled_email_failed", slog.Any("error", err), slog.String("user_id", cfg.UserID))
	}
}
```

If no mailer exists yet, **skip Task 13 entirely** and open a follow-up ticket. The disabled state still surfaces in the dashboard (next task), which is the critical signal.

- [ ] **Step 2: Commit**

```bash
git add web/services/webhook_delivery.go
git commit -m "webhook: email user when their endpoint is auto-disabled"
```

### Task 14: Frontend dashboard surfaces health state + re-enable

**Files (paired frontend PR):**
- `src/components/integrations/WebhookRow.tsx` — render a "Disabled" badge when `health_state === 'disabled'`
- `src/hooks/useWebhooks.ts` — add `reenableWebhook(id)` action that PATCHes `{reenable: true}`

Backend side:
- Modify: `web/handlers/webhook.go` — accept `{"reenable": true}` in the existing PATCH handler

- [ ] **Step 1: Extend the PATCH handler**

In `web/handlers/webhook.go`, the existing `UpdateWebhook` decodes a body like `{"name": "..."}`. Add an optional `reenable` flag:

```go
type updateWebhookRequest struct {
	Name     *string `json:"name,omitempty"`
	Reenable *bool   `json:"reenable,omitempty"`
}

// inside handler:
if req.Reenable != nil && *req.Reenable {
	if err := h.repo.Reenable(ctx, id, userID); err != nil {
		// 404 if not found, 500 otherwise
		if errors.Is(err, models.ErrWebhookConfigNotFound) {
			renderJSON(w, http.StatusNotFound, models.APIError{Code: 404, Message: "webhook not found"})
			return
		}
		internalError(w, h.log, err, "failed to re-enable webhook",
			slog.String("user_id", userID), slog.String("webhook_id", id))
		return
	}
}
```

- [ ] **Step 2: Test**

```go
func TestUpdateWebhook_ReenableClearsDisabledState(t *testing.T) {
	// Seed a webhook in disabled state. PATCH with {"reenable": true}.
	// Re-fetch and assert health_state == "healthy", consecutive_failures == 0.
}
```

- [ ] **Step 3: Commit (backend)**

```bash
git add web/handlers/webhook.go web/handlers/webhook_test.go
git commit -m "webhook: allow PATCH {reenable: true} to clear circuit-breaker state"
```

Frontend changes get their own PR — out of scope for this plan but tracked in the file structure above.

### Phase 4 acceptance gate

```bash
# Migration applied:
psql $DSN -c "\d webhook_configs" | grep -E "consecutive_failures|health_state"

# Repository methods green:
go test ./postgres/ -run TestWebhookConfig -count=1

# Delivery service green:
go test ./web/services/ -run TestWebhookDelivery -count=1

# Manual: configure a webhook pointing at a server that always 500s, run jobs,
# observe consecutive_failures climbing in the DB. After 10 failures, the
# webhook should auto-disable and stop receiving delivery attempts.
```

---

## Chunk 5: Phase 5 — Documentation

### Task 15: Update the docs repo

**Files (`brezel-ai/docs`):**
- Modify: `api-reference/authentication.mdx` — describe tier behaviour
- Modify: `api-reference/jobs.mdx` (or a new `rate-limits.mdx`) — document `RateLimit-*` headers
- Modify: `api-reference/webhooks.mdx` — document `health_state`, auto-disable, re-enable flow

- [ ] **Step 1: Rate-limit headers section**

Add to `authentication.mdx` (or a new `rate-limits.mdx`):

```markdown
## Rate-limit response headers

Every authenticated response includes the following headers so your client can self-throttle without polling:

| Header | Example | Meaning |
|---|---|---|
| `RateLimit-Policy` | `"api";q=5;w=2` | The active policy: `q` is the burst capacity, `w` is the window (seconds) to a full refill. Per IETF `draft-ietf-httpapi-ratelimit-headers-10`. |
| `RateLimit` | `"api";r=4;t=1` | Current state: `r` is the remaining tokens, `t` is the seconds to next refill. |
| `X-RateLimit-Limit` | `5` | Legacy alias for the burst capacity. |
| `X-RateLimit-Remaining` | `4` | Legacy alias for remaining tokens. |
| `X-RateLimit-Reset` | `1748000000` | Unix epoch (seconds) when the bucket is fully refilled. |

When a request is rejected, you'll still receive these headers alongside `Retry-After: <seconds>`.

## Tier behaviour

| Tier | Definition | Rate | Burst |
|---|---|---|---|
| Free | No successful Stripe payment yet. Signup bonus alone does NOT count. | 2 req/s | 5 |
| Paid | At least one successful Stripe payment (refunds do not demote). | 10 req/s | 30 |
| Session (browser) | Clerk session cookie auth (dashboard). | 5 req/s | 20 |

Tier is computed from your account in real-time. Make your first credit purchase and your next API call is on the paid bucket.
```

- [ ] **Step 2: Webhook health-state section**

Add to `webhooks.mdx`:

```markdown
## Health state and auto-disable

Each webhook has a `health_state` returned by `GET /api/v1/webhooks`:

| State | Meaning |
|---|---|
| `healthy` | Last delivery succeeded (or no deliveries yet). |
| `degraded` | 5 or more consecutive failures. Still receiving deliveries — fix your endpoint to clear. |
| `disabled` | 10 consecutive failures. We have stopped delivering to this endpoint. You must re-enable to resume. |

### Re-enabling a disabled webhook

```bash
curl -X PATCH https://api.brezelscraper.com/api/v1/webhooks/{id} \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"reenable": true}'
```

This resets the consecutive-failure counter and sets `health_state` to `healthy`. Your next job completion will resume normal delivery.
```

- [ ] **Step 3: Open the docs PR**

Single commit, single PR. Title: `docs: document rate-limit headers, tier behaviour, and webhook health states`.

---

## Rollout sequence

1. **Phase 1** (tier resolution plumbing) — merge first. Zero behavioural change.
2. **Phase 2** (wire tier to limiter) — merge after Phase 1 deploys cleanly. Paid users start seeing their higher quota immediately on next request. Verify with the smoke test in Task 4.
3. **Phase 3** (response headers) — independent of Phase 2 but easier to verify after Phase 2 is live. Merge once Phase 2 has soaked for ~24h.
4. **Phase 4** (webhook circuit breaker) — independent of Phases 1–3. Can ship in parallel. Migration is additive and non-blocking; existing webhooks remain healthy by default.
5. **Phase 5** (docs) — merge last, after all backend changes are deployed and verified.

## Rollback story per phase

| Phase | Forward-only? | Rollback |
|---|---|---|
| 1 | Yes (additive) | None needed — `User.Tier` is unused outside the auth layer. |
| 2 | Yes | Revert PR. Limiter falls back to always-free. |
| 3 | Yes | Revert PR. Headers disappear; clients tolerate their absence. |
| 4 | **Migration**, but down migration exists. | Apply 000037.down.sql, revert code PRs. Existing rows already had health_state default of 'healthy' so no data loss. |
| 5 | Docs | Revert PR. |

## Out of scope (follow-ups)

- **Automatic health-probe** that re-enables a disabled webhook if a synthetic ping succeeds. v1 requires manual re-enable.
- **Per-receiver outbound rate limit beyond the existing 100/hour per user**. The circuit breaker handles the bigger risk (DDoSing a sick endpoint); a true per-host limiter is a future enhancement.
- **Tier promotion to enterprise / higher-than-paid** quotas. Single boolean today.
- **Headers on the public router** (`/api/v1/version`, `/api/v1/health`). The PerIPRateLimit there doesn't expose snapshots; could be added in a follow-up for symmetry.
- **`Stripe-Rate-Limited-Reason`-style "which limit did I trip" header**. We currently have multiple stacked limiters; today they all share the `"api"` policy name. Differentiating them in the modern header value (e.g. `"api";...,"job_create";...`) is a future enhancement.

---

## Final acceptance criteria

After Phases 1–4 are merged and deployed:

- [ ] `curl -i ... /api/v1/credits/balance` returns 5 `RateLimit*` / `X-RateLimit*` headers
- [ ] A user with `total_credits_purchased > 0` can sustain 10 req/s for 30+ seconds without 429
- [ ] A user with `total_credits_purchased = 0` is rate-limited at 2 req/s as before
- [ ] A webhook pointed at a 500-returning endpoint auto-disables after exactly 10 consecutive deliveries
- [ ] PATCH `{"reenable": true}` clears the disabled state and resets the counter
- [ ] All existing tests stay green
- [ ] No new dependencies introduced
