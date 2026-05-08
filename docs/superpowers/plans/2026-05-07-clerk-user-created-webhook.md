# Clerk `user.created` Webhook — Eager User Provisioning Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the race-on-first-API-call user-provisioning bug surfaced as a brief "Failed to load dashboard" toast on first sign-up. Add a Clerk `user.created` webhook that eagerly inserts the `users` row before the frontend ever fetches; keep the existing lazy auth-middleware path as a concurrency-safe fallback for webhook delays/outages.

**Architecture:** Receive Svix-signed `user.created` POSTs at `/webhooks/clerk`. Verify with `github.com/svix/svix-webhooks/go`. Inside one Postgres transaction: claim the `svix-id` in the existing `processed_webhook_events` dedupe table; insert the user row with `ON CONFLICT DO NOTHING`; commit. After commit (idempotently): `EnsureStripeCustomer` and `grantSignupBonus`. The same provisioning chain — extracted into `web/services/user_provisioning.go` — is invoked by both the webhook handler and the existing auth middleware. Both surfaces converge on the same idempotent Postgres state regardless of which arrives first.

**Tech Stack:** Go (existing toolchain), `github.com/svix/svix-webhooks/go` (new dep), `gorilla/mux` (existing), `database/sql` + `pgx` (existing), `slog` (existing), `samber/oops` (existing). No frontend changes.

---

## Go-skill specialist review fixes (2026-05-08)

After PR #52 was opened, five parallel Go-skill specialist reviews ran (security, concurrency, database, error-handling, testing). They surfaced 2 Critical + 8 High + 18 Medium + 10 Low findings. All Critical, High, and selected Medium items were implemented across 7 fix-up commits; M9 (testify migration, repo-wide refactor) and M12 (build-tag convention, repo-wide decision) deferred as out-of-scope.

| Wave | Commit | Findings closed |
|---|---|---|
| 1 | `29e78f2` | C1 + M5 + M6 — `ErrUserNotFound` + `ErrStripeCustomerIDConflict` sentinels |
| 2 | `85df82c` | H6 + M2 + M7 — `uuid.NewV7` error-checked, `BeginTx` doc + wrap. H2 + H5 confirmed false-positive after re-verification (FOR UPDATE serialises; untargeted ON CONFLICT documented) |
| 3 | `81405c6` | C2 + H1 + H7 + M3 + M4 + M17 — 16-byte key floor, dedupe-row release on Provision failure (503), `defer recover()`, single body cap, log truncation, `auth.ClientIP` for source IP. M1 confirmed false-positive (already targeted). |
| 4 | `28877d0` | H3 + H4 — per-IP rate limit on webhook chain, signing-secret rotation slice |
| 5 | `e25be63` | M15 + M16 — `time.NewTimer` in `jitterSleep`, no-op down for migration 000035 |
| 6 | `d77eca4` | M18 — `singleflight.Group` coalesces concurrent first-request `Provision` |
| 7 | `3d6823e` | H8 + M8 + M10 + M11 + M13 + M14 — fallback-email tests, `t.Parallel`, `goleak.VerifyTestMain`, cleanup ordering, concurrent dedupe test, balance assertion |

**Master reviewer (Opus) verdict on the wave fix-ups:** ready to merge. Every finding either closed or re-confirmed false-positive after deeper inspection. No regressions, no over-engineering, no new bugs introduced. 10/10 stress runs of `TestProvision_Concurrent_DoesNotErrorOrDuplicate` stable. Original-bug coverage preserved.

---

## Review changelog (2026-05-07, post-Clerk-docs verification)

Two **blocking** issues and three over-engineering tightens were applied after re-checking the plan against current `pkg.go.dev/github.com/svix/svix-webhooks/go` and the actual `processed_webhook_events` schema:

1. **BLOCKER → fixed:** Migration `000018` adds `CHECK (event_id ~ '^evt_[a-zA-Z0-9]+$')` on `processed_webhook_events.event_id`. Clerk's Svix `msg_*` IDs would have failed the check on every insert. Added Task 1 (1-line migration to drop the constraint) and updated D5.
2. **BLOCKER (initial thinko, then re-corrected):** A first verification agent claimed Svix's Go API was `NewWebhook(secret) *Webhook` / `Verify(payload string, headers map[string]string)` / `Sign(...) string`. After installing svix-webhooks v1.93.0 in Task 5, **the actual signatures match the original plan, not the agent's claims**: `NewWebhook(secret) (*Webhook, error)`, `Verify(payload []byte, headers http.Header) error`, `Sign(msgId string, timestamp time.Time, payload []byte) (string, error)`. The plan was re-corrected accordingly: `flattenHeaders` helper deleted (no flattening needed), constructor restored to error-returning form, test `signedRequest` reverted to take `time.Time` + `[]byte` and check both errors. Lesson: when a research agent contradicts the original plan on a library's API, install the library and read the source before believing the agent.
3. **YAGNI:** Dropped `Object` and `Timestamp` from the `clerkEvent` JSON struct — neither is read anywhere. Svix already validates the timestamp via signature.
4. **YAGNI:** `claimEvent` SQL no longer sets `processed_at` explicitly — the column has `DEFAULT NOW()`.
5. **Naming clarity:** Dedupe row's `event_type` is now `'clerk.user.created'` (matches the Clerk event-type taxonomy) instead of the generic `'clerk.webhook'`. Future provider-neutral queries on this table are easier to read.

---

## Decisions (and YAGNI cuts)

| # | Decision | Rationale |
|---|---|---|
| D1 | Subscribe to `user.created` **only**. | Only `user.created` fixes the reported bug. `user.updated` (email staleness) and `user.deleted` (account deletion) are real but separate concerns — ship when there is a concrete need. |
| D2 | Keep the lazy-provisioning path in auth middleware as a fallback. | Webhooks can fail silently for >24h before Svix gives up. Without a fallback, any Clerk/Svix outage locks newly-signed-up users out of the API entirely — strictly worse than the bug we're fixing. With idempotency on both surfaces (D3, D4) the fallback is free. |
| D3 | Make `userRepo.Create` idempotent via `ON CONFLICT (id) DO NOTHING`. | Defense in depth. Even without the webhook, this single-line change kills the race in the auth middleware. The webhook is *belt*; this is *suspenders*. |
| D4 | Extract provisioning into `services.UserProvisioning.Provision(ctx, id, email)` called from both the webhook and the auth middleware. | DRY. Eliminates the duplicate Clerk-fetch / Create / EnsureStripeCustomer / grantSignupBonus chain. |
| D5 | Reuse the existing `processed_webhook_events` table; **drop the over-defensive `chk_event_id_format` regex CHECK** in a 1-line migration so non-`evt_*` IDs (Svix `msg_*`) are accepted. | **DRY on the table** — Svix `msg_*` (Clerk) and Stripe `evt_*` (Stripe) IDs cannot collide. **Verification surfaced** that migration `000018` line 60-61 enforces `event_id ~ '^evt_[a-zA-Z0-9]+$'`, which would reject every Clerk insert. The CHECK never carried real safety value (the column is text either way) and dropping it is smaller than adding a `provider` column or maintaining a parallel `processed_clerk_events` table. |
| D6 | Use `github.com/svix/svix-webhooks/go` for signature verification. Do not hand-roll HMAC. | Both research streams independently recommended it. Constant-time compare, ±5min replay window, multi-key rotation are built in. |
| D7 | Synchronous handler with a 10s `context.WithTimeout`. No async queue. | Per-signup volume is trivial, post-commit Stripe call is ~500ms. Adding a queue is YAGNI. |
| D8 | Mount `POST /webhooks/clerk` on the root router (next to `/webhooks/stripe`), outside the auth middleware. | Mirrors the Stripe precedent in `web/web.go:372`. Cleaner than per-route auth opt-outs. |
| D9 | Status codes: **401** bad/missing signature; **400** malformed JSON post-verify; **200** for everything else (dedupe hit, unknown event type, processed, post-commit Stripe failure). | Aligns with Svix retry semantics — 2xx means "stop retrying." Returning 4xx on dedupe makes Svix retry forever. Post-commit Stripe failure returns 200 because the user is already created and `EnsureStripeCustomer` is idempotent on Stripe's side via the per-user idempotency key — Svix re-delivery would re-enter the dedupe path anyway. |
| D10 | No frontend changes. | The bug is server-side; once eager provisioning lands, the symptom disappears with zero client work. |

**Explicitly NOT in this plan (YAGNI):**
- ❌ `user.updated` / `user.deleted` event handling
- ❌ A `provider` column on `processed_webhook_events`
- ❌ Async/queued processing of side effects
- ❌ Prometheus metrics for the new endpoint (existing observability is sufficient until we have an SLO)
- ❌ Frontend hook gating on a "user provisioned" flag
- ❌ Granular per-route auth-middleware opt-outs

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `scripts/migrations/000035_relax_processed_webhook_events_event_id_check.up.sql` | **Create** | `ALTER TABLE processed_webhook_events DROP CONSTRAINT chk_event_id_format;` (down: re-create the constraint). Required so Clerk `msg_*` IDs can be inserted into the existing dedupe table. |
| `postgres/user.go` | Modify (~3 LOC) | Make `Create` idempotent with `ON CONFLICT (id) DO NOTHING`. |
| `postgres/user_test.go` | Modify (+1 test) | Cover concurrent `Create` calls for the same ID. |
| `web/services/user_provisioning.go` | **Create** | `UserProvisioning` service: single source of truth for the provisioning chain. Owns `grantSignupBonus` (moved here from `auth.go`). |
| `web/services/user_provisioning_test.go` | **Create** | Concurrency + idempotency tests for `Provision`. |
| `web/auth/auth.go` | Modify (~40 LOC removed, ~5 added) | Replace inlined chain (lines 126–185) with `provisioning.Provision`. Delete `grantSignupBonus` (moved). |
| `web/auth/auth_test.go` (or existing) | Modify | Adjust if any assertion targeted the inlined chain shape. |
| `web/handlers/clerk_webhook.go` | **Create** | HTTP handler: read raw body → verify Svix → dedupe in tx → call provisioning service → 200/401/400. |
| `web/handlers/clerk_webhook_test.go` | **Create** | Verification, dedupe, malformed body, unknown event type, happy path. |
| `web/web.go` | Modify (~10 LOC) | Add `ClerkWebhookSigningSecret` to `Config`; wire the handler; mount `POST /webhooks/clerk`. |
| `go.mod` / `go.sum` | Modify | Add `github.com/svix/svix-webhooks/go`. |
| `.env.example` (or equivalent) | Modify (1 line) | Document `CLERK_WEBHOOK_SIGNING_SECRET=`. |

**One small migration.** No frontend changes.

---

## ~~Task 1: Migration — relax `processed_webhook_events.event_id` CHECK constraint~~ ✅ DONE

**Commits:** `e1a6ef6` (initial), `fcbb157` (review fixes — added provider-neutral COMMENTs, clarified down-migration warning, updated stale `chk_event_id_format` references in `billing/service_test.go`)

**Review notes:**
- Code-quality reviewer flagged stale comments in `billing/service_test.go` referencing the dropped constraint as the *reason* for `evt_*` fixture prefixes — fixed in `fcbb157` (now phrased as Stripe convention).
- Reviewer suggested `ADD CONSTRAINT IF NOT EXISTS` for idempotent down — **rejected:** PostgreSQL does not support that clause for CHECK constraints; `golang-migrate` runs each down at most once, so non-idempotent down is acceptable.
- Reviewer suggested noting the `ACCESS EXCLUSIVE` lock — applied to the down warning where it matters (the full-table re-scan); skipped on the up because the up is metadata-only.


**Files:**
- Create: `scripts/migrations/000035_relax_processed_webhook_events_event_id_check.up.sql`
- Create: `scripts/migrations/000035_relax_processed_webhook_events_event_id_check.down.sql`

The existing constraint at migration `000018` line 60-61 (`CHECK (event_id ~ '^evt_[a-zA-Z0-9]+$')`) was a Stripe-specific defensive check; it would reject every Clerk `svix-id` (`msg_*`). Since the column is already free-text and provider mixing is safe (Svix `msg_*` and Stripe `evt_*` cannot collide), drop the constraint. No data migration needed.

- [ ] **Step 1: Write the up migration**

```sql
-- 000035_relax_processed_webhook_events_event_id_check.up.sql
-- Drop the Stripe-specific CHECK constraint so Clerk's Svix msg_* IDs can be
-- inserted into the same dedupe table. The constraint added no real safety
-- (the column is text either way); the trade is zero — we gain table reuse
-- across providers.
BEGIN;

ALTER TABLE processed_webhook_events
    DROP CONSTRAINT IF EXISTS chk_event_id_format;

COMMIT;
```

- [ ] **Step 2: Write the down migration**

```sql
-- 000035_relax_processed_webhook_events_event_id_check.down.sql
-- Restore the Stripe-only CHECK constraint. Note: this will fail if any
-- non-evt_* rows exist (e.g., Clerk msg_* rows from after the up migration).
BEGIN;

ALTER TABLE processed_webhook_events
    ADD CONSTRAINT chk_event_id_format
    CHECK (event_id ~ '^evt_[a-zA-Z0-9]+$');

COMMIT;
```

- [ ] **Step 3: Apply locally and verify**

```bash
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" \
  go run . -web   # migrations apply on startup; check logs for 000035
```

Then sanity-check:

```bash
psql -d google_maps_scraper -c "
SELECT conname FROM pg_constraint
WHERE conrelid = 'processed_webhook_events'::regclass
  AND conname = 'chk_event_id_format';
"
```
Expected: zero rows.

- [ ] **Step 4: Commit**

```bash
git add scripts/migrations/000035_*.sql
git commit -m "chore(db): drop Stripe-specific event_id format CHECK on processed_webhook_events"
```

---

## ~~Task 2: Make `userRepo.Create` idempotent (the smallest fix on its own)~~ ✅ DONE

**Commit:** `95d9485` — added `ON CONFLICT (id) DO NOTHING` to `Create` in `postgres/user.go`; added `TestCreate_IsIdempotent_OnDuplicateID` to `postgres/user_test.go`. Test verified passing against local Postgres.

**Review notes:**
- Reviewer: approved. Production fix is minimal and correct; test pins `DO NOTHING` semantics (original row preserved, NOT overwritten).
- **Carry into Task 3:** the auth middleware's lazy path currently uses the in-memory `newUser` struct after `Create`. With `DO NOTHING`, the *loser* of a race gets `nil` error but no insert; in-memory `newUser` happens to match the canonical DB row only because both callers build it from the same Clerk user object. Task 3's `Provision` must `GetByID` after `Create` to fetch the canonical row regardless of who won — this closes the latent divergence.
- **Known limitation (acceptable):** `ON CONFLICT (id)` does not catch a hypothetical race where two callers insert different IDs with the same email (`users_email_key` would still violate). Both surfaces use the Clerk user ID, so id and email are correlated — not a real-world path.
- Pre-existing local test failure on `TestPostgresRepository/*` (jobs CHECK constraint, unrelated to this change) noted but not blocking this PR.


**Files:**
- Modify: `postgres/user.go:68-82`
- Test: `postgres/user_test.go` (add one test)

- [ ] **Step 1: Write the failing test** in `postgres/user_test.go`

```go
// TestCreate_IsIdempotent_OnDuplicateID verifies that calling Create twice
// with the same user ID does NOT return an error. This is required because
// the Clerk webhook and the lazy auth-middleware provisioning path can both
// race to insert the same user; both must succeed silently.
func TestCreate_IsIdempotent_OnDuplicateID(t *testing.T) {
    t.Parallel()
    ctx, db := newTestDB(t) // existing helper in this file
    repo := NewUserRepository(db)

    user := User{ID: "user_idempotency_test", Email: "idem@example.com"}
    if err := repo.Create(ctx, &user); err != nil {
        t.Fatalf("first Create: %v", err)
    }
    // Second call with same ID must succeed (no error, no panic).
    user2 := User{ID: user.ID, Email: "different@example.com"}
    if err := repo.Create(ctx, &user2); err != nil {
        t.Fatalf("second Create (idempotent): %v", err)
    }

    // The original row must be preserved (DO NOTHING semantics — not DO UPDATE).
    got, err := repo.GetByID(ctx, user.ID)
    if err != nil {
        t.Fatalf("GetByID: %v", err)
    }
    if got.Email != "idem@example.com" {
        t.Errorf("email: want %q, got %q (DO NOTHING must not overwrite)", "idem@example.com", got.Email)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./postgres/ -run TestCreate_IsIdempotent_OnDuplicateID -count=1 -v
```
Expected: FAIL with `pq: duplicate key value violates unique constraint "users_pkey"`.

- [ ] **Step 3: Implement** — change the SQL in `postgres/user.go:68-82`

```go
func (repo *userRepository) Create(ctx context.Context, user *User) error {
    const q = `INSERT INTO users (id, email, created_at, updated_at)
               VALUES ($1, $2, $3, $4)
               ON CONFLICT (id) DO NOTHING`

    now := time.Now().UTC()
    if user.CreatedAt.IsZero() {
        user.CreatedAt = now
    }
    if user.UpdatedAt.IsZero() {
        user.UpdatedAt = now
    }

    _, err := repo.db.ExecContext(ctx, q, user.ID, user.Email, user.CreatedAt, user.UpdatedAt)
    return err
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./postgres/ -count=1
```
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add postgres/user.go postgres/user_test.go
git commit -m "fix(postgres): make user Create idempotent via ON CONFLICT DO NOTHING"
```

---

## ~~Task 3: Extract `UserProvisioning` service~~ ✅ DONE

**Commits:** `bc454ab` (initial), `b3aca4f` (review fixes), `d7504c7` (Task 2 amendment surfaced by Task 3's stress test)

**Review notes:**
- Code-quality reviewer flagged 3 Important issues; all fixed in `b3aca4f`:
  1. **Lock-order inversion vs `billing/service.go`** — swapped to `users FOR UPDATE` first, dropped `FOR UPDATE` from EXISTS check (relies on `idx_unique_signup_bonus` partial unique index from migration `000022`).
  2. **`grantSignupBonus` returning `nil` on no-op caused `signup_bonus_granted` log spam** on every steady-state request — changed signature to `(granted bool, err error)`; caller's `switch` only logs the success case when actually granted.
  3. **Bare error returns** — wrapped every DB call site with `fmt.Errorf("user_provisioning: <op>: %w", err)` to match `dashboard.go` / `credit.go` conventions.
- Stress test ran 8 goroutines and intermittently failed with `users_email_key` violation. Root cause: Task 2's `ON CONFLICT (id) DO NOTHING` only suppresses `users_pkey`. Fixed in `d7504c7` by dropping the conflict target — both call sites derive id+email from the same Clerk user object, so `ON CONFLICT DO NOTHING` (no target) is safe and complete. **10/10 stress runs pass** post-fix.
- One minor observation deferred to Task 4: `BeginTx` error path in `grantSignupBonus` doesn't wrap (pre-existing behavior; will surface as a follow-up if it ever fires).
- Reviewer noted that `Provision` returns `dbUser` without re-reading after `EnsureStripeCustomer` writes `stripe_customer_id`. Acceptable today (no caller needs it); not fixing.


**Files:**
- Create: `web/services/user_provisioning.go`
- Create: `web/services/user_provisioning_test.go`

- [ ] **Step 1: Write the failing test** in `web/services/user_provisioning_test.go`

```go
package services

import (
    "context"
    "sync"
    "testing"
    // (test helpers and fakes already present in services/ for other tests)
)

// TestProvision_Concurrent_DoesNotErrorOrDuplicate spawns N goroutines that
// all call Provision for the same Clerk user ID at the same time. Reproduces
// the original race that caused "Failed to load dashboard" toasts on first
// sign-up: three parallel /api/v1/* requests all hitting auth-middleware lazy
// provisioning concurrently. Post-fix, all goroutines must succeed and the
// users table must contain exactly one row.
func TestProvision_Concurrent_DoesNotErrorOrDuplicate(t *testing.T) {
    t.Parallel()
    svc, db := newTestProvisioningService(t)

    const N = 8
    var wg sync.WaitGroup
    errs := make(chan error, N)
    for i := 0; i < N; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, err := svc.Provision(context.Background(), "user_concurrent_test", "race@example.com")
            errs <- err
        }()
    }
    wg.Wait()
    close(errs)
    for err := range errs {
        if err != nil {
            t.Fatalf("Provision returned error: %v", err)
        }
    }

    var count int
    if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id=$1`, "user_concurrent_test").Scan(&count); err != nil {
        t.Fatalf("count: %v", err)
    }
    if count != 1 {
        t.Errorf("users row count: want 1, got %d", count)
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./web/services/ -run TestProvision_Concurrent -count=1 -v
```
Expected: FAIL — `Provision` does not exist yet.

- [ ] **Step 3: Implement** `web/services/user_provisioning.go`

```go
// Package services hosts cross-cutting orchestration that is reused by
// multiple HTTP handlers. UserProvisioning is the single source of truth
// for "make sure a Postgres users row exists for this Clerk user, with
// any one-time signup side effects applied," called by both the
// /webhooks/clerk handler and the auth middleware's lazy-provisioning
// fallback.
package services

import (
    "context"
    "database/sql"
    "errors"
    "log/slog"

    "github.com/google/uuid"
    "github.com/gosom/google-maps-scraper/billing"
    "github.com/gosom/google-maps-scraper/models"
    "github.com/gosom/google-maps-scraper/postgres"
)

// SignupBonusAmount is the credit amount granted to new users on signup ($2.00).
// Sourced previously from web/auth/auth.go; centralized here because this is
// now the only place that grants it.
const SignupBonusAmount = 2.0

type UserProvisioning struct {
    db         *sql.DB
    userRepo   postgres.UserRepository
    billingSvc *billing.Service // nil-safe (test/no-Stripe builds)
    logger     *slog.Logger
}

func NewUserProvisioning(
    db *sql.DB,
    userRepo postgres.UserRepository,
    billingSvc *billing.Service,
    logger *slog.Logger,
) *UserProvisioning {
    return &UserProvisioning{db: db, userRepo: userRepo, billingSvc: billingSvc, logger: logger}
}

// Provision ensures a users row exists for the given Clerk user ID and email,
// and grants one-time signup side effects (signup bonus, lazy Stripe customer
// creation). Safe to call concurrently and repeatedly: every step is
// idempotent. Returns the canonical user row.
//
// Order is intentional: insert user row first (atomic, fast); then non-fatal
// best-effort calls to Stripe + bonus grant. A failure in Stripe/bonus does
// NOT roll back the user row — same contract as the prior auth.go logic.
func (s *UserProvisioning) Provision(ctx context.Context, userID, email string) (postgres.User, error) {
    if userID == "" || email == "" {
        return postgres.User{}, errors.New("user_provisioning: userID and email are required")
    }

    // Step 1: idempotent insert. ON CONFLICT DO NOTHING means concurrent
    // callers all succeed; the loser simply does not insert.
    newUser := postgres.User{ID: userID, Email: email}
    if err := s.userRepo.Create(ctx, &newUser); err != nil {
        return postgres.User{}, err
    }

    // Step 2: re-read to get the canonical row regardless of which caller
    // actually inserted it. Cheap (PK lookup) and avoids subtle "did I or
    // did the other goroutine win?" branching.
    dbUser, err := s.userRepo.GetByID(ctx, userID)
    if err != nil {
        return postgres.User{}, err
    }

    // Step 3: lazy Stripe customer creation (idempotent — guarded by
    // existing stripe_customer_id check in EnsureStripeCustomer + Stripe
    // idempotency key). Non-fatal: log and continue.
    if s.billingSvc != nil {
        if _, err := s.billingSvc.EnsureStripeCustomer(ctx, dbUser.ID, dbUser.Email, dbUser.StripeCustomerID, s.userRepo); err != nil {
            s.logger.Error("stripe_customer_ensure_failed_on_provision",
                slog.String("user_id", dbUser.ID), slog.Any("error", err))
        }
    }

    // Step 4: signup bonus (idempotent — guarded by reference_id='signup_bonus'
    // unique check inside the function). Non-fatal: log and continue.
    if err := s.grantSignupBonus(ctx, dbUser.ID); err != nil {
        s.logger.Error("failed_to_grant_signup_bonus",
            slog.String("user_id", dbUser.ID), slog.Any("error", err))
    } else {
        s.logger.Info("signup_bonus_granted",
            slog.Float64("amount", SignupBonusAmount), slog.String("user_id", dbUser.ID))
    }

    // Newly created users get the default role; align in-memory struct with
    // the DB default so callers don't need a second GetByID.
    if dbUser.Role == "" {
        dbUser.Role = models.RoleUser
    }
    return dbUser, nil
}

// grantSignupBonus is moved verbatim from web/auth/auth.go:287-336 (no logic
// change). It uses an idempotency check on credit_transactions so concurrent
// callers see at most one bonus credit.
func (s *UserProvisioning) grantSignupBonus(ctx context.Context, userID string) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer func() {
        if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
            s.logger.Error("rollback_failed", slog.Any("error", rbErr))
        }
    }()

    var alreadyGranted bool
    err = tx.QueryRowContext(ctx,
        "SELECT EXISTS(SELECT 1 FROM credit_transactions WHERE user_id = $1 AND reference_id = 'signup_bonus' AND reference_type = 'system' FOR UPDATE)",
        userID).Scan(&alreadyGranted)
    if err != nil {
        return err
    }
    if alreadyGranted {
        s.logger.Info("signup_bonus_already_granted", slog.String("user_id", userID))
        return nil
    }

    var currentBalance float64
    if err = tx.QueryRowContext(ctx, "SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&currentBalance); err != nil {
        return err
    }
    newBalance := currentBalance + SignupBonusAmount

    if _, err = tx.ExecContext(ctx, `
        UPDATE users
        SET credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
            updated_at = NOW()
        WHERE id = $2`, SignupBonusAmount, userID); err != nil {
        return err
    }

    if _, err = tx.ExecContext(ctx, `
        INSERT INTO credit_transactions (id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
        VALUES ($1, $2, 'bonus', $3, $4, $5, 'Signup bonus', 'signup_bonus', 'system')`,
        uuid.Must(uuid.NewV7()).String(), userID, SignupBonusAmount, currentBalance, newBalance); err != nil {
        return err
    }

    return tx.Commit()
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./web/services/ -run TestProvision_Concurrent -count=1 -v
```
Expected: PASS, exactly 1 row.

- [ ] **Step 5: Commit**

```bash
git add web/services/user_provisioning.go web/services/user_provisioning_test.go
git commit -m "feat(services): extract UserProvisioning as single source of truth"
```

---

## ~~Task 4: Refactor auth middleware to use the service (DRY cleanup)~~ ✅ DONE

**Commits:** `b27b62f` (refactor), `79b1fe9` (stale-comment cleanup)

**LOC delta in `auth.go`:** +37 / -115 (net -78 lines).

**Review notes:**
- Reviewer: approved. All HTTP error semantics preserved (Clerk fetch fail → 500, no email → 400, Provision fail → 500). Logging fields preserved (`user_id`, `path`, `method`, `error`). `dbUser` re-assignment correct (no shadowing). `*clerk.User` used for the email helper because `user.Client.Get()` returns that type.
- Two stale comments cleaned up in follow-up `79b1fe9`: removed reference to "Task 4 will delete the duplicate" in `user_provisioning.go`; updated `web/web.go` comment to mention `UserProvisioning` rather than `NewAuthMiddleware` as the consumer of `billingSvc`.
- Reviewer flagged a **pre-existing** issue (out of scope): `userRepo.GetByID` returns `errors.New("user not found")` for not-found AND wraps transient DB errors via the same path — both old and new code treat any error as "user not found." Worth a follow-up to introduce a `models.ErrNotFound` sentinel, but not blocking this PR.


**Files:**
- Modify: `web/auth/auth.go:118-185` (the inlined chain) and `:287-336` (delete `grantSignupBonus`)
- Modify: `web/auth/auth.go:64-80` (constructor — accept `*services.UserProvisioning` instead of `db`+`billingSvc` for the provisioning concern)
- Modify: `web/web.go` constructor wiring
- Modify: `web/auth/auth.go` — delete unused imports (`uuid`, `billing`)

- [ ] **Step 1: Verify existing auth middleware tests still cover the lazy path** (read-only check)

```bash
go test ./web/auth/ -run "Provision|Lazy|FirstRequest" -count=1 -v
```
If no tests target the lazy provisioning path, that is fine — the new `web/services` test covers it. Proceed.

- [ ] **Step 2: Modify the constructor signature** — `AuthMiddleware` should take a `*services.UserProvisioning` instead of (or in addition to) `db`+`billingSvc`. Rationale: middleware no longer needs a DB connection of its own for provisioning; the service owns that.

In `web/auth/auth.go`:

```go
type AuthMiddleware struct {
    userAPI      *user.Client
    userRepo     postgres.UserRepository
    apiKeyRepo   models.APIKeyRepository
    serverSecret []byte
    provisioning *services.UserProvisioning  // NEW — replaces db + billingSvc
    logger       *slog.Logger
}

func NewAuthMiddleware(
    clerkAPIKey string,
    userRepo postgres.UserRepository,
    apiKeyRepo models.APIKeyRepository,
    serverSecret []byte,
    provisioning *services.UserProvisioning,
    logger *slog.Logger,
) (*AuthMiddleware, error) {
    clerk.SetKey(clerkAPIKey)
    return &AuthMiddleware{
        userAPI: user.NewClient(&clerk.ClientConfig{
            BackendConfig: clerk.BackendConfig{Key: clerk.String(clerkAPIKey)},
        }),
        userRepo:     userRepo,
        apiKeyRepo:   apiKeyRepo,
        serverSecret: serverSecret,
        provisioning: provisioning,
        logger:       logger,
    }, nil
}
```

- [ ] **Step 3: Replace the inlined chain** in `Authenticate`'s "user not found" branch (the body of the `if err != nil` at line 127) with:

```go
// User not found — auto-provision from Clerk. Defense-in-depth fallback
// for the Clerk user.created webhook (handlers/clerk_webhook.go); both
// paths converge on the same idempotent UserProvisioning.Provision call.
clerkUser, err := m.userAPI.Get(r.Context(), userID)
if err != nil {
    m.logger.Error("failed_to_retrieve_user_from_clerk", slog.String("user_id", userID), slog.Any("error", err))
    http.Error(w, "Failed to retrieve user information", http.StatusInternalServerError)
    return
}

email := primaryEmailFromClerkUser(clerkUser) // small helper extracted from old lines 136-147
if email == "" {
    m.logger.Error("user_has_no_email", slog.String("user_id", userID))
    http.Error(w, "User has no email address", http.StatusBadRequest)
    return
}

dbUser, err = m.provisioning.Provision(r.Context(), userID, email)
if err != nil {
    m.logger.Error("user_provisioning_failed",
        slog.String("user_id", userID),
        slog.String("path", r.URL.Path),
        slog.String("method", r.Method),
        slog.Any("error", err))
    http.Error(w, "Failed to create user record", http.StatusInternalServerError)
    return
}
```

Then **delete** the entire `grantSignupBonus` function from `auth.go` (lines 287-336). It now lives in the service.

- [ ] **Step 4: Add the small helper** at the bottom of `auth.go` (or factored to a separate file if `auth.go` is too long):

```go
// primaryEmailFromClerkUser returns the primary email address from a Clerk
// user record, falling back to the first email if no primary is set.
// Returns "" if the user has no email addresses at all.
func primaryEmailFromClerkUser(u *clerkuser.User) string {
    if u.PrimaryEmailAddressID != nil {
        primaryID := *u.PrimaryEmailAddressID
        for _, ea := range u.EmailAddresses {
            if ea.ID == primaryID {
                return ea.EmailAddress
            }
        }
    }
    if len(u.EmailAddresses) > 0 {
        return u.EmailAddresses[0].EmailAddress
    }
    return ""
}
```

- [ ] **Step 5: Update the wire-up** in `web/web.go` where `NewAuthMiddleware` is called. Construct `services.UserProvisioning` first (it depends on `db`, `userRepo`, `billingSvc`, `logger`), pass it in.

```go
provisioningSvc := services.NewUserProvisioning(cfg.PgDB, cfg.UserRepo, ans.billingSvc, ans.logger)
authMW, err := auth.NewAuthMiddleware(
    cfg.ClerkAPIKey,
    cfg.UserRepo,
    cfg.APIKeyRepo,
    cfg.ServerSecret,
    provisioningSvc,
    ans.logger,
)
```

- [ ] **Step 6: Verify the build and the existing test suite**

```bash
go build ./...
go test ./web/... ./postgres/... -count=1
```
Expected: green.

- [ ] **Step 7: Commit**

```bash
git add web/auth/auth.go web/web.go
git commit -m "refactor(auth): delegate user provisioning to services.UserProvisioning"
```

---

## ~~Task 5: Add Svix dependency~~ ✅ DONE

**Commit:** `6e3042f` — `go get github.com/svix/svix-webhooks/go@latest` brought in v1.93.0. Skipped `go mod tidy` (it would strip the `// indirect` dep until Task 7 actually imports it).

**Discovery:** verified the actual library signatures by reading source at `~/go/pkg/mod/github.com/svix/svix-webhooks@v1.93.0/go/webhook.go`. Real signatures are `NewWebhook(secret) (*Webhook, error)`, `Verify(payload []byte, headers http.Header) error`, `Sign(msgId, timestamp time.Time, payload []byte) (string, error)`. Plan code blocks for Task 7 corrected accordingly (the prior verification agent had been wrong). See updated Review changelog at the top of this plan.


**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/svix/svix-webhooks/go@latest
go mod tidy
```

- [ ] **Step 2: Verify it builds**

```bash
go build ./...
```
Expected: green.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): add svix-webhooks/go for Clerk webhook signature verification"
```

---

## ~~Task 6: Add `ClerkWebhookSigningSecret` to config~~ ✅ DONE

**Commits:** `8fec8f8` (config plumbing), `50ed9a7` (restore svix dep dropped by go-tooling during build/test).

**Files touched:** `pkg/config/config.go` (env-tagged struct field), `web/web.go` (`ServerConfig.ClerkWebhookSigningSecret`), `runner/webrunner/webrunner.go` (literal init), `.env.example` (doc).

**Review notes:**
- Reviewer caught that `go build`/`go test` invocations during the task run silently stripped the `svix-webhooks` dep from `go.mod`/`go.sum` (it was added as `// indirect` in Task 5 because no source file imports it yet, so any module-graph tidy treats it as removable). Restored in `50ed9a7`. Task 7's import will pin it as a direct dep.
- Field `ClerkWebhookSigningSecret` is **not required** in config — empty string disables `/webhooks/clerk` route mounting (per D9 / D2). The auth-middleware lazy-provisioning fallback handles signups when the route is disabled.


**Files:**
- Modify: `web/web.go` — add to `Config` struct (next to `StripeWebhookSecrets` at line 59)
- Modify: `runner/` config loader where `StripeWebhookSecrets` is loaded — load `CLERK_WEBHOOK_SIGNING_SECRET` the same way
- Modify: `.env.example` (or whatever the project uses for documenting env vars)

- [ ] **Step 1: Add field to `Config`** in `web/web.go`:

```go
// ClerkWebhookSigningSecret is the Svix signing secret for the Clerk
// webhook endpoint configured in the Clerk Dashboard. Empty string disables
// the /webhooks/clerk endpoint (route still mounts but returns 503).
// Required in production; optional locally.
ClerkWebhookSigningSecret string
```

- [ ] **Step 2: Plumb the env var** in the runner/config loader. Find the line that reads `STRIPE_WEBHOOK_SECRET` (or similar) and add a sibling read for `CLERK_WEBHOOK_SIGNING_SECRET`.

- [ ] **Step 3: Document** in `.env.example` (or equivalent):

```
# Clerk webhook signing secret (Svix-format). Get from the Clerk Dashboard
# → Webhooks → your endpoint → Signing Secret. Per-environment (test vs prod
# Clerk instances each have their own).
CLERK_WEBHOOK_SIGNING_SECRET=
```

- [ ] **Step 4: Build verification**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add web/web.go runner/ .env.example
git commit -m "config: plumb CLERK_WEBHOOK_SIGNING_SECRET through Config"
```

---

## ~~Task 7: Implement the Clerk webhook handler~~ ✅ DONE

**Commits:** `c57e174` (initial — 596 lines across handler + test), `251933a` (review fixes).

**Tests:** 7/7 pass, race-clean. 2 pure-unit (sig + headers); 5 integration (gated on `PG_TEST_DSN`, all use `t.Cleanup` to drop dedupe rows).

**Review notes:**
- Reviewer flagged 3 Important issues; all fixed in `251933a`:
  1. **413 vs 400 on body-too-large** — `MaxBytesReader` now distinguished via `errors.As(err, &http.MaxBytesError)`. Diagnostic value for ops dashboards; both still trigger Svix retry, so no correctness delta.
  2. **`claimEvent` hardcodes `event_type='clerk.user.created'`** even for unknown event types. Fixed by adding a comment block explaining the trade-off (we claim before parse, the Dashboard only sends user.created, so unknown events are at most a labeling imprecision).
  3. **`fakeProvisioner` data race** between `atomic.AddInt32` callCount and unprotected string fields — replaced with mutex-guarded struct + `snapshot()` method; race detector clean.
- Reviewer noted minor concerns (fragility of `Verify` vs `VerifyIgnoringTimestamp` in tests if a debugger pauses >5min; missing comment on silent-return contract in `handleUserCreated`) — accepted as noise, not addressed.
- Verified API surface against `~/go/pkg/mod/github.com/svix/svix-webhooks@v1.93.0/go/webhook.go`: `NewWebhook(secret) (*Webhook, error)`, `Verify(payload []byte, headers http.Header) error`, `Sign(msgId, time.Time, []byte) (string, error)` — all consumed correctly.


**Files:**
- Create: `web/handlers/clerk_webhook.go`
- Create: `web/handlers/clerk_webhook_test.go`

- [ ] **Step 1: Write the failing tests** in `web/handlers/clerk_webhook_test.go`. Use a fake `UserProvisioning` (interface, see step 2 below) to keep tests hermetic.

```go
package handlers_test

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync/atomic"
    "testing"
    "time"

    svix "github.com/svix/svix-webhooks/go"
    // package under test
)

// shared helpers
const testSigningSecret = "whsec_dGVzdHNlY3JldGZvcnVuaXR0ZXN0cw=="

// signedRequest forges a Svix-signed POST against the handler under test.
// Uses the actual svix-webhooks v1.93.0 API: NewWebhook(secret) returns
// (*Webhook, error); Sign(msgId, timestamp time.Time, payload []byte) returns
// (string, error). Both errors are checked for hygiene.
func signedRequest(t *testing.T, body []byte, secret string) *http.Request {
    t.Helper()
    msgID := "msg_" + uuid.NewString()
    ts := time.Now()

    wh, err := svix.NewWebhook(secret)
    if err != nil {
        t.Fatalf("svix.NewWebhook: %v", err)
    }
    sig, err := wh.Sign(msgID, ts, body)
    if err != nil {
        t.Fatalf("svix.Sign: %v", err)
    }

    req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", bytes.NewReader(body))
    req.Header.Set("svix-id", msgID)
    req.Header.Set("svix-timestamp", strconv.FormatInt(ts.Unix(), 10))
    req.Header.Set("svix-signature", sig)
    return req
}

// 1) Happy path: valid user.created → 200, provisioning called once.
func TestClerkWebhook_UserCreated_Provisions(t *testing.T) { /* ... */ }

// 2) Bad signature → 401, provisioning NOT called.
func TestClerkWebhook_BadSignature_401(t *testing.T) { /* ... */ }

// 3) Missing svix-* headers → 401.
func TestClerkWebhook_MissingHeaders_401(t *testing.T) { /* ... */ }

// 4) Malformed JSON post-verify → 400.
func TestClerkWebhook_MalformedJSON_400(t *testing.T) { /* ... */ }

// 5) Dedup: same svix-id delivered twice → first 200 + provisioning called;
//    second 200 + provisioning NOT called again.
func TestClerkWebhook_DedupesByMessageID(t *testing.T) {
    // Use atomic counter on the fake provisioner to assert exactly one call.
}

// 6) Unknown event type (e.g., "session.created") → 200, provisioning NOT called.
func TestClerkWebhook_UnknownEventType_200_NoOp(t *testing.T) { /* ... */ }

// 7) user.created with no email addresses → 200 (acknowledge), log + skip.
//    Returning 4xx would make Svix retry forever.
func TestClerkWebhook_UserCreatedNoEmail_200_NoOp(t *testing.T) { /* ... */ }
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./web/handlers/ -run TestClerkWebhook -count=1 -v
```
Expected: all FAIL — handler does not exist.

- [ ] **Step 3: Implement** `web/handlers/clerk_webhook.go`

```go
package handlers

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "io"
    "log/slog"
    "net/http"
    "time"

    svix "github.com/svix/svix-webhooks/go"

    "github.com/gosom/google-maps-scraper/postgres"
    "github.com/gosom/google-maps-scraper/web/services"
)

// Provisioner is the narrow interface the webhook handler depends on. Lets
// tests inject a fake without spinning up Postgres.
type Provisioner interface {
    Provision(ctx context.Context, userID, email string) (postgres.User, error)
}

// ClerkWebhookHandler verifies and dispatches Svix-signed Clerk webhooks.
// Routed at POST /webhooks/clerk in web/web.go (outside the auth middleware).
type ClerkWebhookHandler struct {
    db           *sql.DB
    verifier     *svix.Webhook
    provisioning Provisioner
    logger       *slog.Logger
}

// Svix Go API (verified against svix-webhooks v1.93.0 source — the actual
// installed version):
//
//   func NewWebhook(secret string) (*Webhook, error)
//   func (*Webhook) Verify(payload []byte, headers http.Header) error
//   func (*Webhook) Sign(msgId string, timestamp time.Time, payload []byte) (string, error)
//
// Pass the raw body bytes and the http.Header directly — no flattening
// required.

func NewClerkWebhookHandler(db *sql.DB, signingSecret string, provisioning Provisioner, logger *slog.Logger) (*ClerkWebhookHandler, error) {
    if signingSecret == "" {
        return nil, errors.New("clerk_webhook: signing secret is empty")
    }
    wh, err := svix.NewWebhook(signingSecret)
    if err != nil {
        return nil, fmt.Errorf("clerk_webhook: %w", err)
    }
    return &ClerkWebhookHandler{
        db:           db,
        verifier:     wh,
        provisioning: provisioning,
        logger:       logger,
    }, nil
}

// Maximum body size for an inbound Clerk webhook (defensive). Clerk payloads
// for user.created are ~2KB; 32KB is generous.
const clerkWebhookMaxBody = 32 * 1024

// Maximum total time we allow the handler to spend; well under Svix's 15s
// timeout. Stripe customer create is ~500ms p99, so 10s is comfortable.
const clerkWebhookTimeout = 10 * time.Second

// clerkEvent is the minimal envelope we read from the verified body. Other
// envelope fields (object, timestamp, instance_id) exist but we don't read
// them — Svix has already enforced the timestamp via signature verification,
// and we rely on Type for dispatch and Data for the typed payload.
type clerkEvent struct {
    Type string          `json:"type"`
    Data json.RawMessage `json:"data"`
}

type clerkUserCreatedData struct {
    ID                     string `json:"id"`
    PrimaryEmailAddressID  string `json:"primary_email_address_id"`
    EmailAddresses         []struct {
        ID           string `json:"id"`
        EmailAddress string `json:"email_address"`
    } `json:"email_addresses"`
}

func (h *ClerkWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), clerkWebhookTimeout)
    defer cancel()

    // 1. Read raw body BEFORE any parsing — signature is computed over bytes.
    body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, clerkWebhookMaxBody))
    if err != nil {
        h.logger.Warn("clerk_webhook_body_read_failed", slog.Any("error", err))
        http.Error(w, "body read", http.StatusBadRequest)
        return
    }

    // 2. Verify signature. svix-webhooks v1.93.0 takes raw payload bytes and
    // the original http.Header — no flattening needed.
    if err := h.verifier.Verify(body, r.Header); err != nil {
        h.logger.Warn("clerk_webhook_signature_invalid",
            slog.String("source_ip", r.RemoteAddr),
            slog.Any("error", err))
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    msgID := r.Header.Get("svix-id")
    if msgID == "" {
        // Svix verify already requires this header; defensive.
        http.Error(w, "missing svix-id", http.StatusUnauthorized)
        return
    }

    // 3. Claim the event in the dedupe table. If the row already exists,
    // this is a redelivery — return 200 without re-processing.
    claimed, err := h.claimEvent(ctx, msgID)
    if err != nil {
        h.logger.Error("clerk_webhook_dedupe_failed", slog.String("svix_id", msgID), slog.Any("error", err))
        http.Error(w, "transient", http.StatusServiceUnavailable)
        return
    }
    if !claimed {
        h.logger.Info("clerk_webhook_duplicate_ignored", slog.String("svix_id", msgID))
        w.WriteHeader(http.StatusOK)
        return
    }

    // 4. Parse the event envelope.
    var evt clerkEvent
    if err := json.Unmarshal(body, &evt); err != nil {
        h.logger.Warn("clerk_webhook_malformed_json", slog.String("svix_id", msgID), slog.Any("error", err))
        http.Error(w, "malformed", http.StatusBadRequest)
        return
    }

    // 5. Dispatch on event type. We only handle user.created today.
    switch evt.Type {
    case "user.created":
        h.handleUserCreated(ctx, msgID, evt.Data)
    default:
        // Acknowledge and ignore — returning 4xx would make Svix retry
        // forever for events we don't subscribe to.
        h.logger.Info("clerk_webhook_event_ignored",
            slog.String("svix_id", msgID), slog.String("type", evt.Type))
    }

    w.WriteHeader(http.StatusOK)
}

func (h *ClerkWebhookHandler) handleUserCreated(ctx context.Context, msgID string, raw json.RawMessage) {
    var data clerkUserCreatedData
    if err := json.Unmarshal(raw, &data); err != nil {
        h.logger.Warn("clerk_webhook_user_created_data_invalid",
            slog.String("svix_id", msgID), slog.Any("error", err))
        return
    }

    email := primaryEmailFromClerkPayload(data)
    if data.ID == "" || email == "" {
        h.logger.Warn("clerk_webhook_user_created_missing_fields",
            slog.String("svix_id", msgID),
            slog.String("user_id", data.ID),
            slog.Bool("has_email", email != ""))
        return
    }

    if _, err := h.provisioning.Provision(ctx, data.ID, email); err != nil {
        // Provisioning failed AFTER we claimed the event — log and let the
        // next user request hit the auth-middleware fallback. Returning 5xx
        // here would make Svix redeliver, but the dedupe row is already
        // present so the redelivery would also no-op. Returning 200 +
        // logging is the right call.
        h.logger.Error("clerk_webhook_provisioning_failed",
            slog.String("svix_id", msgID),
            slog.String("user_id", data.ID),
            slog.Any("error", err))
        return
    }

    h.logger.Info("clerk_webhook_user_provisioned",
        slog.String("svix_id", msgID), slog.String("user_id", data.ID))
}

// claimEvent inserts a row into processed_webhook_events and returns true if
// this caller is the first to see the message ID. Reuses the existing dedupe
// table from migration 000018; the table's processed_at, processing_result,
// and metadata columns all have DB defaults so we only set the two columns
// that are provider-specific.
func (h *ClerkWebhookHandler) claimEvent(ctx context.Context, msgID string) (bool, error) {
    const q = `
        INSERT INTO processed_webhook_events (event_id, event_type)
        VALUES ($1, 'clerk.user.created')
        ON CONFLICT (event_id) DO NOTHING
    `
    res, err := h.db.ExecContext(ctx, q, msgID)
    if err != nil {
        return false, err
    }
    n, err := res.RowsAffected()
    if err != nil {
        return false, err
    }
    return n == 1, nil
}

func primaryEmailFromClerkPayload(d clerkUserCreatedData) string {
    if d.PrimaryEmailAddressID != "" {
        for _, ea := range d.EmailAddresses {
            if ea.ID == d.PrimaryEmailAddressID {
                return ea.EmailAddress
            }
        }
    }
    if len(d.EmailAddresses) > 0 {
        return d.EmailAddresses[0].EmailAddress
    }
    return ""
}

```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./web/handlers/ -run TestClerkWebhook -count=1 -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/handlers/clerk_webhook.go web/handlers/clerk_webhook_test.go
git commit -m "feat(handlers): add Clerk user.created webhook with Svix verification + dedupe"
```

---

## ~~Task 8: Mount the route in `web/web.go`~~ ✅ DONE

**Commits:** `09bd2c7` (initial mount), `bc7c080` (review fixes — explicit slice copy + Warn on missed-mount).

**Changes:**
- Hoisted `provisioningSvc` from inside the auth-middleware `if` to function scope so both surfaces share one instance.
- Refactored `webhookMws` into `baseWebhookMws` (4 shared middlewares: RequestID / InjectLogger / RequestLogger / MaxBodySize 64KB) + `stripeWebhookMws` (base + optional CIDR). Clerk handler uses base only — no CIDR allowlist (Clerk uses Cloudflare, not fixed CIDRs).
- Mounted `POST /webhooks/clerk` on the root router, gated on `ClerkWebhookSigningSecret != "" && provisioningSvc != nil`. NewClerkWebhookHandler error aborts startup.
- When secret is set but provisioning is nil (DB/repo missing), `slog.Warn("clerk_webhook_route_not_mounted")` fires at startup so the gap is operationally visible.

**Review notes:**
- Reviewer caught a latent slice-aliasing hazard (`stripeWebhookMws := baseWebhookMws` is a slice-header copy; safe at cap 4 today but would silently bleed CIDR onto `baseWebhookMws` if `base` ever grows past cap). Fixed by `append([]func(http.Handler) http.Handler{}, baseWebhookMws...)` for an explicit copy.
- Reviewer recommended a startup `Warn` for the silent-non-mount case; applied.
- All package tests green; no regressions on the existing Stripe webhook path.


**Files:**
- Modify: `web/web.go` (mirror the Stripe webhook wiring at line 363/372)

- [ ] **Step 1: Construct the handler and mount it**, immediately after the Stripe webhook lines (~web/web.go:372). Use the same `webhookMws` middleware chain (request ID, logger injection, body size — but DO NOT apply the auth middleware).

```go
// Clerk user.created webhook — eager user provisioning. Mounted on the root
// router (not under /api/v1) and exempt from auth middleware; authentication
// is via Svix signature.
if cfg.ClerkWebhookSigningSecret != "" {
    clerkHandler, err := handlers.NewClerkWebhookHandler(cfg.PgDB, cfg.ClerkWebhookSigningSecret, provisioningSvc, ans.logger)
    if err != nil {
        return nil, fmt.Errorf("clerk webhook handler: %w", err)
    }
    clerkWebhookHandler := webmiddleware.Chain(clerkHandler, webhookMws...)
    router.Handle("/webhooks/clerk", clerkWebhookHandler).Methods(http.MethodPost)
}
```

- [ ] **Step 2: Build + run unit tests**

```bash
go build ./...
go test ./... -count=1
```
Expected: green.

- [ ] **Step 3: Commit**

```bash
git add web/web.go
git commit -m "feat(web): mount POST /webhooks/clerk route"
```

---

## ⚠️ Task 9: Smoke test against a running server — **DEFERRED, requires manual user action**

This task verifies the route end-to-end against live Clerk infrastructure. It cannot be executed by an autonomous agent because it requires:
1. A public tunnel (cloudflared / ngrok / etc.) to expose the local server.
2. Access to the Clerk Dashboard for the test instance to register the webhook URL + retrieve the per-environment signing secret.
3. A real test user signup or "Send test event" click in the Dashboard.

**Coverage already in place via automated tests** (commit `c57e174` / `251933a`):
- Signature verification (good, bad, missing headers).
- Dedupe by `svix-id` (redelivery returns 200 without re-processing).
- Malformed-JSON handling.
- Unknown event types acknowledged with 200.
- Missing-email user.created acknowledged with 200.
- Happy-path provisioning end-to-end through the real `UserProvisioning` service.

These cover the critical correctness paths. The remaining manual smoke test verifies: (a) Clerk header casing matches what `svix-webhooks/go` expects; (b) the production signing secret is parseable; (c) the configured Dashboard URL resolves through any reverse proxy / CDN in front of the backend. None of these are software-correctness issues and all surface immediately on the first real delivery.

**Manual instructions** (when ready):

```bash
# 1. Start backend with test-instance Clerk secret + a real signing secret.
CLERK_SECRET_KEY="sk_test_..." \
CLERK_WEBHOOK_SIGNING_SECRET="whsec_..." \
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" \
  go run . -web

# 2. Expose with cloudflared / ngrok:
cloudflared tunnel --url http://localhost:8080
# → https://<random>.trycloudflare.com

# 3. In Clerk Dashboard (test instance):
#    Webhooks → Add Endpoint → URL: https://<tunnel>/webhooks/clerk
#    Events: select user.created (only)
#    Save → copy "Signing Secret" → paste into CLERK_WEBHOOK_SIGNING_SECRET above and restart.

# 4. Trigger via Dashboard → "Send test event" → user.created.

# 5. Verify in Postgres:
psql -d google_maps_scraper -c "SELECT id, email FROM users ORDER BY created_at DESC LIMIT 3;"
psql -d google_maps_scraper -c "SELECT event_id, event_type, processed_at FROM processed_webhook_events WHERE event_type='clerk.user.created' ORDER BY processed_at DESC LIMIT 3;"
psql -d google_maps_scraper -c "SELECT user_id, type, amount FROM credit_transactions WHERE reference_id='signup_bonus' ORDER BY created_at DESC LIMIT 3;"

# 6. Re-send the same test event → verify redelivery returns 200 quickly + no new rows.

# 7. UX-level test: sign up a fresh user via the staging frontend → land on /dashboard
#    → confirm there is NO flash of "Failed to load dashboard."
```

This task remains **open** until the user executes it. Implementation is complete; this is verification only.


**No code change.** Use Clerk's webhook test feature.

- [ ] **Step 1: Start the backend locally** with `CLERK_WEBHOOK_SIGNING_SECRET` set to a Clerk *test instance* signing secret.

```bash
CLERK_WEBHOOK_SIGNING_SECRET="whsec_..." \
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" \
  go run . -web
```

- [ ] **Step 2: Expose the local server** (e.g., via `cloudflared` or `ngrok`) and configure the test-instance Clerk Dashboard webhook to point at `<tunnel>/webhooks/clerk` with `user.created` selected.

- [ ] **Step 3: Trigger a `user.created` event** from the Clerk Dashboard's "Send test event" button OR by signing up a fresh test user.

- [ ] **Step 4: Verify in Postgres**

```bash
psql -d google_maps_scraper -c "SELECT id, email FROM users WHERE id = 'user_<test_id>';"
psql -d google_maps_scraper -c "SELECT event_id, event_type FROM processed_webhook_events WHERE event_type = 'clerk.webhook' ORDER BY processed_at DESC LIMIT 5;"
psql -d google_maps_scraper -c "SELECT user_id, type, amount FROM credit_transactions WHERE reference_id = 'signup_bonus' ORDER BY created_at DESC LIMIT 5;"
```

Expected: user row exists, dedupe row exists, signup bonus transaction exists.

- [ ] **Step 5: Re-send the same event** from the Clerk Dashboard. Verify it returns 200 quickly and does NOT create a second user row or a second bonus transaction (counts unchanged).

- [ ] **Step 6: End-to-end UX test** — sign up a fresh user via the staging frontend → land on `/dashboard` → confirm there is **no flash** of "Failed to load dashboard."

- [ ] **Step 7: No commit** — this task is verification only.

---

## ~~Task 10: Open the PR + master code review~~ ✅ DONE

**PR:** [#52 — fix(auth): eager Clerk user provisioning + race-safe lazy fallback](https://github.com/brezel-ai/brezelscraper-backend/pull/52)

**Final commit on branch:** `1056622` (alignment of `Provision` doc-comment with the actual `ON CONFLICT DO NOTHING` clause — caught by the master reviewer).

**Master review verdict (Opus):** **ready to merge**. No critical or important issues. Five strengths called out (defense-in-depth design, webhook hygiene, no PII in logs, correct route-mount placement outside auth middleware, safe migration). Seven minor follow-ups identified — all post-merge:
1. ✅ FIXED: doc-comment drift in `Provision` (commit `1056622`).
2. Pre-existing: `userRepo.GetByID` flattens DB errors into "user not found" — switch to `errors.Is(err, sql.ErrNoRows)` sentinel in a follow-up.
3. Inconsistency: webhook handler accepts a narrow `Provisioner` interface; auth middleware accepts the concrete `*services.UserProvisioning`. Hoist the interface in a follow-up if reusable.
4. `CLERK_WEBHOOK_SIGNING_SECRET` is silently optional even in prod — consider making it required when `AppEnv.IsProduction()`.
5. No automated test of the auth-middleware lazy fallback end-to-end (covered transitively by the services concurrency test).
6. No test asserting empty signing secret leaves the route unmounted.
7. No test for "webhook + auth fallback both fire concurrently for the same user" cross-surface race (concurrency test already covers in-process contention on `Provision`, which is the actual contention point).

**Plan executed in this branch:**
- 9 implementation tasks (1 deferred — Task 9 manual smoke).
- 26 commits total (10 feature/fix commits, 7 doc/plan commits, 1 dep, 1 master-review fix, 6 plan-marker commits).
- Per-task ping-pong: spec compliance reviewer → code-quality reviewer → fix-implementer cycles. Every flagged Critical/Important issue addressed before moving on.
- Master reviewer dispatched against the full branch diff.


- [ ] **Step 1: Push and open PR against `develop`**

```bash
git push -u origin fix/clerk-webhook-eager-provisioning
gh pr create --repo brezel-ai/brezelscraper-backend --base develop \
  --title "fix(auth): add Clerk user.created webhook + idempotent provisioning to fix dashboard race" \
  --body "..."
```

PR body checklist:
- Root cause summary (link to debugging analysis)
- Decision matrix (D1–D10) with one-sentence rationale each
- Test plan: concurrent-Provision unit test, Clerk Dashboard "Send test event" smoke test, fresh-signup UX test on staging
- Rollout: deploy backend → configure Clerk Dashboard webhook for **production** Clerk instance with prod `CLERK_WEBHOOK_SIGNING_SECRET` → monitor logs for `clerk_webhook_user_provisioned` and `clerk_webhook_provisioning_failed`
- Rollback: leaving `CLERK_WEBHOOK_SIGNING_SECRET` unset disables the route; the existing lazy-provisioning fallback (now race-safe via Task 1's `ON CONFLICT`) continues to work standalone

---

## Risk register

| Risk | Mitigation |
|---|---|
| Webhook misconfigured in Clerk Dashboard (wrong secret, wrong URL) | Lazy-provisioning fallback (D2) keeps users functional. Logs `clerk_webhook_signature_invalid` will spike — alert on this. |
| Postgres `processed_webhook_events` table grows unbounded | Existing concern shared with Stripe webhooks. Out of scope for this PR. File a follow-up issue for retention. |
| Clerk renames event type or payload schema | Today: `user.created` is stable per the Clerk research brief. We dispatch on `evt.Type` so unknown types are silently 200-ed; a schema change would surface as `clerk_webhook_user_created_missing_fields` log alarms, not user-visible breakage (lazy fallback covers). |
| Stripe customer create fails inside `Provision` | Already non-fatal (logs + continues). User row exists; `CreateCheckoutSession` lazy-creates on first checkout. Same behavior as today. |
| Two parallel Clerk webhook deliveries for the same `svix-id` | Dedupe row's PK conflict makes only one of them claim → other returns 200 immediately. |

---

## Definition of Done

- [ ] All unit tests green (`go test ./... -count=1`).
- [ ] Smoke test on staging Clerk instance: fresh signup → no toast flash on `/dashboard`.
- [ ] PR merged to `develop`.
- [ ] Production Clerk Dashboard webhook configured with prod signing secret.
- [ ] One follow-up issue filed: "Add retention sweep for `processed_webhook_events`" (out of scope here).
