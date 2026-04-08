# Stripe Production Readiness Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix every Stripe integration issue — including a silently-broken refund path, missing Customer/PaymentIntent persistence, and an uncapped checkout surface — so BrezelScraper's credit-purchase flow is financially correct, idempotent, auditable, and aligned with current Stripe docs and SaaS best practice (Vercel, Anthropic, OpenAI, Stripe-for-Stripe's own credits).

**Architecture:** Single `billing.Service` remains the entry point. The refund money-loss path is fixed end-to-end: (1) Stripe Customer objects are created at user provisioning and persisted via a new `users.stripe_customer_id` column; (2) the `checkout.session.completed` handler backfills `stripe_payment_intent_id` so the refund lookup can actually find the payment row; (3) a new `refund_deficit_credits` column captures the uncollectable portion when credits have already been consumed (the DB keeps its `credit_balance >= 0` safety rail); (4) future purchases pay down deficit before crediting balance; (5) every POST to Stripe gets an `Idempotency-Key`; (6) all monetary math moves off `float64` to decimal-text round-trips.

**Tech Stack:** Go 1.22+, `github.com/stripe/stripe-go/v82`, Postgres (pgx + database/sql), Clerk Go SDK v2, gorilla/mux, `testify` for tests.

**Related Docs:**
- This plan's audit context: `docs/superpowers/plans/2026-04-08-api-production-readiness-audit.md` (items **C-1**, **M-4**, **M-7** are covered here; audit entries tagged with `[audit: X]`).
- Stripe docs cross-referenced via the Stripe MCP (see findings for specific doc citations).

---

## §0 — Executive Summary

### The one sentence

**The refund path is silently broken end-to-end: we never write `stripe_payment_intent_id`, we never create a Stripe Customer, so when Stripe fires `charge.refunded` the handler fails both its primary and fallback lookups, finds no user, deducts nothing, and the user keeps both the money and the credits.**

This is not hypothetical. A grep of the entire backend confirms zero writes to either `users.stripe_customer_id` or `stripe_payments.stripe_payment_intent_id`. Both columns exist in the schema, are read in the refund handler, and are perpetually NULL. Every refund issued from the Stripe Dashboard today results in `charge_refunded_no_user_found` at WARN level.

### Finding inventory (24 items across 4 priority tiers)

| Tier | Count | What must change |
|-----:|------:|------------------|
| **P0 Critical** | 5 | Fix the refund pipeline + cap checkout + fail-fast on prod secrets |
| **P1 High** | 5 | Idempotency + decimal math + request-tier hardening |
| **P2 Medium** | 6 | Defense-in-depth + async PM readiness + handleChargeFailed repair |
| **P3 Minor** | 4 | Hygiene + documentation + API version pinning |

### Database constraint interaction (the constraint that matters)

Migration `scripts/migrations/000012_add_credit_system.up.sql:23` declares:
```sql
credit_balance NUMERIC(18,6) NOT NULL DEFAULT 0 CHECK (credit_balance >= 0)
```

And `credit_transactions` rows carry:
```sql
balance_before NUMERIC(18,6) NOT NULL CHECK (balance_before >= 0),
balance_after  NUMERIC(18,6) NOT NULL CHECK (balance_after  >= 0),
CONSTRAINT balance_calculation_check CHECK (balance_after = balance_before + amount)
```

**What this means for refunds:** if a user buys 100 credits, spends 95, and Stripe issues a $50 refund (= 50 credits proportional), the naive deduction would push `credit_balance` to `-45`, which the `CHECK (credit_balance >= 0)` constraint would reject, aborting the webhook transaction and causing Stripe to retry forever.

**The current code works around this** by capping deduction at current balance (`billing/service.go:893-906`) and logging `refund_cap_applied`. That prevents the DB error but *loses the refund integrity*: the user keeps 45 credits of consumed value for free.

**The correct fix (S-C4 below):** introduce a `refund_deficit_credits` column on `users`. When a refund exceeds current balance, deduct what we can from `credit_balance`, write the remainder to `refund_deficit_credits`, and apply the next purchase against the deficit before the new balance. This matches what Vercel, Anthropic (Claude API), and OpenAI do for pre-paid credit SaaS. The `credit_balance >= 0` safety rail stays — we never allow a negative spendable balance.

### Why the priorities are ordered this way

- **P0 is a hard dependency chain.** S-C4 (refund deficit) is useless unless S-C2 (write PaymentIntent ID) and S-C3 (create Customer) land first, because the refund handler never reaches the deficit logic today — it bails out at "user not found". Implement in strict order: S-C1 → S-C2 → S-C3 → S-C4 → S-C5.
- **P1 is independent hardening.** Any task in P1 can ship in any order after P0 lands.
- **P2 items are preconditions for enabling new payment methods.** Launching with cards-only is fine without them.
- **P3 is cleanup that can land anytime.**

### Out of scope

- Subscriptions, recurring billing, Stripe Billing credits, Stripe Tax, Radar rules, fraud tuning, customer portal wiring. These are future work.
- Dispute handling UI. The dispute webhook handler is in scope (P1); the ops console to action disputes is not.
- Webhook replay tooling. `processed_webhook_events` already gives us the audit trail; a replay CLI is P3+.

---

## §1 — File Structure

### Modified

- `models/user.go` — add `StripeCustomerID *string` field on `User` struct; extend `UserRepository` interface with `SetStripeCustomerID` and `GetWithCredits`.
- `postgres/user.go` — implement new methods; include `stripe_customer_id` in `GetByID` / `GetByEmail` scans.
- `billing/service.go` — the majority of changes land here. See per-task sections below for exact line ranges.
- `web/handlers/billing.go` — add `DisallowUnknownFields`, surface credit cap errors with structured response.
- `web/auth/auth.go` — after `userRepo.Create` (line 145), call `billingSvc.EnsureStripeCustomer(...)` and persist the result before granting the signup bonus.
- `runner/webrunner/webrunner.go` — add `validateProductionSecrets()` called from `Run()` before HTTP listener start. (Also covers audit **M-7**.)
- `billing/service_test.go` — new tests per task.

### Created

- `billing/customer.go` — new file housing `EnsureStripeCustomer`, `GetOrCreateCustomer`, and the Customer struct mapping. Keeps `service.go` from growing past 1500 lines.
- `billing/refund.go` — new file housing `handleChargeRefunded`, `handleChargeRefundUpdated`, the deficit-ledger logic, and decimal helpers. Extracted from `service.go` because refund logic is the single largest hotspot in this plan.
- `billing/decimal.go` — new file with three helpers: `parseCreditsStrict`, `decimalDeduct`, and `microCreditsFromDecimal`. Pure functions, unit-tested in isolation.
- `billing/customer_test.go`, `billing/refund_test.go`, `billing/decimal_test.go` — companion tests.
- `scripts/migrations/000027_stripe_customer_and_refund_deficit.up.sql` — adds `users.refund_deficit_credits`, a `'refund_deficit'` value to the `credit_transactions.type` CHECK allowlist, and a `stripe_payments.status` value `'refund_deficit_applied'`.
- `scripts/migrations/000027_stripe_customer_and_refund_deficit.down.sql` — reverses the above.

### Deleted

None. All changes are additive or in-place.

### File size contract

- Keep `billing/service.go` under 1200 lines after extraction. If it exceeds, split further — `handleCheckoutSessionCompleted` and `handleCheckoutSessionExpired` can move into `billing/checkout.go` in a follow-up.

---

## §2 — Finding Inventory (ID → Task map)

Each finding has a stable ID used throughout the plan. The ID format is `S-<tier><number>` (S for Stripe).

### P0 Critical — release blockers

| ID | Source | One-line | Task |
|----|--------|----------|------|
| **S-C1** | audit **C-1** | `CreateCheckoutSession` accepts unbounded `credits`. | Task 1.1 |
| **S-C2** | **NEW (re-review N-1)** | `stripe_payment_intent_id` column is never written → refund lookup always fails → refunds silently broken. | Task 1.2 |
| **S-C3** | **NEW (re-review N-2)** | `users.stripe_customer_id` column never written; no Stripe Customer ever created; `CheckoutSessionParams` has no `Customer` field → duplicate guest customers, broken fallback lookups in refund + failed-charge handlers, no Customer Portal possible. | Task 1.3 |
| **S-C4** | audit **M-4** escalated + **NEW** | Refund deficit ledger. With S-C2/S-C3 fixed, the refund handler finally reaches its deduction branch — at which point the `CHECK (credit_balance >= 0)` DB constraint would reject any deduction larger than current balance. Current code caps silently and loses refund integrity. Fix via a `refund_deficit_credits` column + apply-deficit-before-balance on next purchase. | Task 1.4 |
| **S-C5** | audit **M-7** | Fail-fast on missing `STRIPE_WEBHOOK_SECRET` / `ENCRYPTION_KEY` when `APP_ENV=production`. | Task 1.5 |

### P1 High — fix before launch

| ID | Source | One-line | Task |
|----|--------|----------|------|
| **S-H1** | **NEW (re-review N-3)** | No `Idempotency-Key` on `checkoutsession.New` (or any other POST to Stripe). Network retries / double-clicks create duplicate sessions and orphaned pending rows. | Task 2.1 |
| **S-H2** | **NEW (re-review N-4)** | Refund credit calculation uses `float64` arithmetic (`billing/service.go:827`). Round-trips through `float8` scan + `math.Round(x * MicroUnit)` are fragile under partial-refund math. Use decimal-text or `shopspring/decimal`. | Task 2.2 |
| **S-H3** | **NEW** | `fmt.Sscan(req.Credits, &creditsInt)` at `billing/service.go:84` accepts `"1000 garbage"` and `"10.5"` silently. Use `strconv.Atoi(strings.TrimSpace(...))`. | Task 2.3 |
| **S-H4** | **NEW** | `CheckoutSessionParams` does not set `ClientReferenceID` (our internal `user_id`). Stripe's docs recommend it for search/reconciliation. Metadata works but requires expand-chains to discover from a `charge` event. | Task 2.4 |
| **S-H5** | **NEW** | No `charge.dispute.created` handler. Chargebacks are refunds-plus-fees and come with a 21-day evidence deadline; they need their own event type. | Task 2.5 |

### P2 Medium — fix shortly after launch (before enabling new payment methods)

| ID | Source | One-line | Task |
|----|--------|----------|------|
| **S-M1** | **NEW (re-review N-5)** | `handleCheckoutSessionCompleted` falls back to `session.Metadata["credits"]` if the DB row is missing. DB row is authoritative; metadata should not be trusted for monetary amounts. | Task 3.1 |
| **S-M2** | **NEW (re-review N-6)** | `ReconcileSession` calls `checkoutsession.Get(sessionID, nil)` — no `Expand: ["line_items","payment_intent"]`. Can't verify purchased quantity against DB row, can't get receipt URL. | Task 3.2 |
| **S-M3** | **NEW (re-review N-8)** | No `refund.updated` handler. When ACH/SEPA is enabled, a refund can fail after initial success; we'd silently keep the credit deduction. | Task 3.3 |
| **S-M4** | **NEW** | No `checkout.session.async_payment_succeeded` / `checkout.session.async_payment_failed` handlers. Required before enabling Bacs/SEPA/ACH. | Task 3.4 |
| **S-M5** | **NEW** | `handleChargeFailed` (`billing/service.go:969`) only has a Customer-based user lookup — broken until S-C3 ships. Also needs a PaymentIntent fallback. | Task 3.5 |
| **S-M6** | **NEW** | `stripe_receipt_url` column exists in `stripe_payments` but is never written. Should be populated from `PaymentIntent.Charges.Data[0].ReceiptURL` on `checkout.session.completed`. | Task 3.6 |

### P3 Minor — code hygiene

| ID | Source | One-line | Task |
|----|--------|----------|------|
| **S-L1** | **NEW** | Stripe API version not pinned. The `stripe-go/v82` SDK defaults to the account's API version, which can change out from under us if someone upgrades via the Dashboard. | Task 4.1 |
| **S-L2** | audit **H-9** (Stripe slice) | `json.NewDecoder(r.Body).Decode(...)` in `CreateCheckoutSession`/`Reconcile` handlers omits `DisallowUnknownFields()`. | Task 4.2 |
| **S-L3** | **NEW** | Existing `refund_cap_applied_total` Prometheus counter becomes dead code after S-C4 lands (deficit ledger replaces silent capping). Either delete it or rewire it to count non-zero deficit writes. | Task 4.3 |
| **S-L4** | **NEW** | The canonical webhook path `/api/v1/billing/webhook` and retired paths are not documented in the OpenAPI spec. Add a stub entry. | Task 4.4 |

### Verified-correct (no change needed)

These are Stripe-adjacent items the re-review confirmed are already right and should NOT be touched:

- **Webhook signature verification** — `billing/service.go:137` uses `webhook.ConstructEvent` with raw body via `io.ReadAll(r.Body)`. `MaxBodySize` middleware at `web/middleware/middleware.go:236` uses `http.MaxBytesReader` which only enforces a byte ceiling; it does **not** mutate the body, so raw-body integrity is preserved (Stripe docs cite raw-body mutation as the #1 sig-verification failure cause — we're safe).
- **Webhook idempotency** — `processed_webhook_events` INSERT is the *first* statement inside the SERIALIZABLE transaction in every handler. `ON CONFLICT (event_id) DO NOTHING` dedupes retries before any balance mutation. This is the pattern Stripe's own expert-knowledge-gem recommends. Do not move it.
- **Retired legacy webhook paths** — 410 Gone on `/webhooks/stripe` and `/api/stripe/webhook` (`web/web.go:290-291`) is the correct posture and surfaces Stripe-dashboard misconfigurations loudly.
- **Body size cap** — 64 KiB via `webmiddleware.MaxBodySize(64 << 10)` is correct for Stripe webhook payloads (largest real-world events are ~20 KiB).
- **64 KiB is NOT raw-body-mutating.** Do not "improve" this by adding any other middleware to the webhook chain without re-testing signature verification.

---

## Chunk 1: P0 Critical Findings

> **Execution order inside this chunk is strict. Do not skip ahead.** S-C2 depends on S-C3 transitively (both write new columns that interact), and S-C4 depends on both. S-C1 and S-C5 are independent and can be interleaved.

### Task 1.1: Cap credits per checkout session [S-C1, audit C-1]

**Files:**
- Modify: `billing/service.go:63-128` — `CreateCheckoutSession`
- Modify: `billing/decimal.go` (new) — `parseCreditsStrict`
- Test: `billing/decimal_test.go` (new), `billing/service_test.go`

- [ ] **Step 1: Create `billing/decimal.go` with the strict parser**

```go
package billing

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxCreditsPerCheckoutSession is the hard ceiling on a single Stripe
// checkout session. This is a fraud-control limit, not a pricing limit —
// customers who legitimately need more credits must issue multiple sessions.
// Aligned with Stripe's own pattern (`limit` on list APIs capped at 100;
// GitHub's `per_page` at 100; every large public billable API ships a ceiling).
const MaxCreditsPerCheckoutSession = 10_000

// parseCreditsStrict converts a user-supplied credits string to an int.
// Unlike fmt.Sscan it rejects trailing garbage (e.g. "1000 garbage"), decimal
// values ("10.5"), and leading/trailing whitespace is trimmed but inner
// whitespace is rejected.
func parseCreditsStrict(s string) (int, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("credits is required")
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("credits must be a whole positive integer")
	}
	if n <= 0 {
		return 0, fmt.Errorf("credits must be > 0")
	}
	if n > MaxCreditsPerCheckoutSession {
		return 0, fmt.Errorf("credits exceeds maximum of %d per checkout session", MaxCreditsPerCheckoutSession)
	}
	return n, nil
}
```

- [ ] **Step 2: Write the failing tests** — `billing/decimal_test.go`

```go
package billing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCreditsStrict(t *testing.T) {
	t.Parallel()

	t.Run("valid_whole_number", func(t *testing.T) {
		n, err := parseCreditsStrict("100")
		require.NoError(t, err)
		require.Equal(t, 100, n)
	})

	t.Run("trimmed_whitespace", func(t *testing.T) {
		n, err := parseCreditsStrict("  500  ")
		require.NoError(t, err)
		require.Equal(t, 500, n)
	})

	t.Run("rejects_trailing_garbage", func(t *testing.T) {
		_, err := parseCreditsStrict("1000 garbage")
		require.Error(t, err)
		require.Contains(t, err.Error(), "whole positive integer")
	})

	t.Run("rejects_decimal", func(t *testing.T) {
		_, err := parseCreditsStrict("10.5")
		require.Error(t, err)
	})

	t.Run("rejects_zero", func(t *testing.T) {
		_, err := parseCreditsStrict("0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "> 0")
	})

	t.Run("rejects_negative", func(t *testing.T) {
		_, err := parseCreditsStrict("-10")
		require.Error(t, err)
	})

	t.Run("rejects_empty", func(t *testing.T) {
		_, err := parseCreditsStrict("")
		require.Error(t, err)
	})

	t.Run("rejects_above_cap", func(t *testing.T) {
		_, err := parseCreditsStrict("10001")
		require.Error(t, err)
		require.Contains(t, strings.ToLower(err.Error()), "maximum")
	})

	t.Run("accepts_exactly_at_cap", func(t *testing.T) {
		n, err := parseCreditsStrict("10000")
		require.NoError(t, err)
		require.Equal(t, 10_000, n)
	})
}
```

- [ ] **Step 3: Run the tests, confirm they fail**

```bash
cd brezelscraper-backend
go test ./billing/ -run TestParseCreditsStrict -v
```

Expected: FAIL — `parseCreditsStrict` doesn't exist yet or the function body of Step 1 is not yet saved.

- [ ] **Step 4: Save `billing/decimal.go` and rerun**

```bash
go test ./billing/ -run TestParseCreditsStrict -v
```

Expected: PASS (8/8)

- [ ] **Step 5: Replace the credits parse block in `CreateCheckoutSession`**

In `billing/service.go`, delete lines 82-86 (the `fmt.Sscan` block) and replace with:

```go
creditsInt, err := parseCreditsStrict(req.Credits)
if err != nil {
	return CheckoutResponse{}, err
}
```

Remove the now-unused `fmt.Sscan` import check if any (none expected).

- [ ] **Step 6: Write a handler-level test** — append to `web/handlers/billing_test.go` (create if missing)

```go
func TestCreateCheckoutSession_HandlerRejectsLargeCredits(t *testing.T) {
	h := newTestHandler(t)
	body := `{"credits":"100000","currency":"USD"}`
	req := httptest.NewRequest("POST", "/api/v1/credits/checkout-session", strings.NewReader(body))
	req = req.WithContext(authCtx("user-1"))
	rr := httptest.NewRecorder()
	h.CreateCheckoutSession(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "maximum")
}

func TestCreateCheckoutSession_HandlerRejectsTrailingGarbage(t *testing.T) {
	h := newTestHandler(t)
	body := `{"credits":"1000 garbage","currency":"USD"}`
	req := httptest.NewRequest("POST", "/api/v1/credits/checkout-session", strings.NewReader(body))
	req = req.WithContext(authCtx("user-1"))
	rr := httptest.NewRecorder()
	h.CreateCheckoutSession(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}
```

- [ ] **Step 7: Run all billing tests**

```bash
go test ./billing/... ./web/handlers/... -run CheckoutSession -v
```

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add billing/decimal.go billing/decimal_test.go billing/service.go web/handlers/billing_test.go
git commit -m "fix(billing): cap credits per Stripe checkout session and tighten parsing

- Hard ceiling of 10_000 credits per session (S-C1, covers audit C-1)
- parseCreditsStrict rejects decimals, trailing garbage, negative, empty (S-H3)
- Handler-level + unit tests
"
```

---

### Task 1.2: Backfill `stripe_payment_intent_id` in `checkout.session.completed` [S-C2]

**Context:** This is the first half of fixing the refund money-loss bug. Without this, the refund handler's primary lookup at `billing/service.go:796` — `WHERE stripe_payment_intent_id = $1` — never matches any row because we never write the column. Grep confirmed: zero writes anywhere in the backend.

**Files:**
- Modify: `billing/service.go` — `handleCheckoutSessionCompleted` (lines 177-322)
- Test: `billing/service_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `billing/service_test.go`:

```go
func TestHandleCheckoutSessionCompleted_BackfillsPaymentIntentID(t *testing.T) {
	t.Parallel()
	svc, db := newTestServiceWithDB(t)
	userID := "user-backfill-1"
	sessionID := "cs_test_backfill_1"
	paymentIntentID := "pi_test_backfill_1"

	insertTestUser(t, db, userID)
	insertPendingStripePayment(t, db, userID, sessionID, 100)

	// Construct a fake Stripe event with a fully-populated PaymentIntent ref.
	rawJSON := fmt.Sprintf(`{
		"id": %q,
		"payment_status": "paid",
		"payment_intent": %q,
		"metadata": {"user_id": %q, "credits": "100", "currency": "USD"}
	}`, sessionID, paymentIntentID, userID)
	event := stripe.Event{
		ID:   "evt_test_backfill_1",
		Type: "checkout.session.completed",
		Data: &stripe.EventData{Raw: json.RawMessage(rawJSON)},
	}

	code, err := svc.handleCheckoutSessionCompleted(context.Background(), event)
	require.NoError(t, err)
	require.Equal(t, 200, code)

	// Verify the payment intent ID was backfilled.
	var gotPI sql.NullString
	require.NoError(t, db.QueryRow(
		"SELECT stripe_payment_intent_id FROM stripe_payments WHERE stripe_checkout_session_id = $1",
		sessionID).Scan(&gotPI))
	require.True(t, gotPI.Valid, "stripe_payment_intent_id should be backfilled, got NULL")
	require.Equal(t, paymentIntentID, gotPI.String)
}
```

- [ ] **Step 2: Run and confirm failure**

```bash
go test ./billing/ -run TestHandleCheckoutSessionCompleted_BackfillsPaymentIntentID -v
```

Expected: FAIL — `stripe_payment_intent_id` is NULL.

- [ ] **Step 3: Modify `handleCheckoutSessionCompleted` to extract and persist the PI ID**

In `billing/service.go`, locate the transaction block starting at line 236 (`tx, err := s.db.BeginTx(...)`). After the `markEventProcessed` call (line 252) and before the `SELECT ... FOR UPDATE` on `users` (line 264), extract the PaymentIntent ID from the session and update the `stripe_payments` row. The new block:

```go
// Extract the PaymentIntent ID from the completed session so that future
// charge.refunded / charge.dispute webhooks can find this payment row.
// The initial row inserted by CreateCheckoutSession only has the session ID;
// this is the canonical moment to learn the PI ID and link them.
paymentIntentID := ""
if session.PaymentIntent != nil && session.PaymentIntent.ID != "" {
	paymentIntentID = session.PaymentIntent.ID
}
if paymentIntentID != "" {
	const updPI = `UPDATE stripe_payments
		SET stripe_payment_intent_id = $1, updated_at = NOW()
		WHERE stripe_checkout_session_id = $2
		  AND (stripe_payment_intent_id IS NULL OR stripe_payment_intent_id = $1)`
	if _, err := tx.ExecContext(ctx, updPI, paymentIntentID, session.ID); err != nil {
		s.logger.Error("failed_to_backfill_payment_intent_id",
			slog.String("session_id", session.ID),
			slog.String("payment_intent_id", paymentIntentID),
			slog.Any("error", err),
		)
		return 500, fmt.Errorf("failed to backfill payment intent: %w", err)
	}
} else {
	// Soft-warn: this means either the webhook fired before the PI was attached
	// (should not happen for payment-mode sessions) or Stripe's payload format
	// changed. Do not fail — the balance credit is more important than the PI link.
	s.logger.Warn("checkout_completed_missing_payment_intent",
		slog.String("session_id", session.ID),
		slog.String("event_id", event.ID),
	)
}
```

Note the idempotent WHERE clause: if Stripe replays the event and we already wrote the PI ID, the UPDATE still succeeds (0 rows affected is acceptable; we don't want to error on replay).

- [ ] **Step 4: Rerun the test**

```bash
go test ./billing/ -run TestHandleCheckoutSessionCompleted_BackfillsPaymentIntentID -v
```

Expected: PASS

- [ ] **Step 5: Add a replay-safety test**

Append to `billing/service_test.go`:

```go
func TestHandleCheckoutSessionCompleted_BackfillIsIdempotent(t *testing.T) {
	t.Parallel()
	svc, db := newTestServiceWithDB(t)
	userID := "user-replay-1"
	sessionID := "cs_test_replay_1"
	paymentIntentID := "pi_test_replay_1"
	insertTestUser(t, db, userID)
	insertPendingStripePayment(t, db, userID, sessionID, 100)

	event := makeCheckoutSessionCompletedEvent(t, sessionID, paymentIntentID, userID, 100)
	_, err := svc.handleCheckoutSessionCompleted(context.Background(), event)
	require.NoError(t, err)

	// Replay the same event. The processed_webhook_events gate should short-circuit.
	code, err := svc.handleCheckoutSessionCompleted(context.Background(), event)
	require.NoError(t, err)
	require.Equal(t, 200, code)

	// Balance should still be 100, not 200.
	var bal float64
	require.NoError(t, db.QueryRow("SELECT credit_balance::float8 FROM users WHERE id = $1", userID).Scan(&bal))
	require.InDelta(t, 100.0, bal, 0.000001)
}
```

- [ ] **Step 6: Run and confirm**

```bash
go test ./billing/ -run TestHandleCheckoutSessionCompleted -v
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add billing/service.go billing/service_test.go
git commit -m "fix(billing): backfill stripe_payment_intent_id on checkout.session.completed (S-C2)

Without this, the charge.refunded handler's primary lookup never matched,
causing refunds to silently deduct zero credits. Idempotent UPDATE tolerates
webhook replays. See docs/superpowers/plans/2026-04-08-stripe-production-readiness.md.
"
```

---

### Task 1.3: Create Stripe Customer at user provisioning + persist `stripe_customer_id` [S-C3]

**Context:** The second half of the refund fix. The `users.stripe_customer_id` column exists (migration line 26) but is never written. The `User` model struct doesn't even have the field. Stripe's docs unambiguously recommend creating a `Customer` for every buyer and passing it via `CheckoutSessionParams.Customer` so "all objects created during the Session are associated with the correct Customer" (Stripe MCP: Build a subscriptions integration, Bacs/PayPal guides). Today we're creating a fresh guest Customer for every checkout.

**Files:**
- Modify: `models/user.go` — add `StripeCustomerID`
- Modify: `postgres/user.go` — include column in SELECTs; add `SetStripeCustomerID`
- Create: `billing/customer.go` — `EnsureStripeCustomer`
- Create: `billing/customer_test.go`
- Modify: `billing/service.go` — `CreateCheckoutSession` passes `Customer`
- Modify: `web/auth/auth.go` — call `EnsureStripeCustomer` after user insert
- Test: `web/auth/auth_test.go`

- [ ] **Step 1: Extend the `User` model**

In `models/user.go`, add the field:

```go
type User struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	Role             string    `json:"role"`
	StripeCustomerID *string   `json:"-"` // hidden from JSON; internal only
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
```

And extend the interface:

```go
type UserRepository interface {
	GetByID(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	Create(ctx context.Context, user *User) error
	SetStripeCustomerID(ctx context.Context, userID, stripeCustomerID string) error
	Delete(ctx context.Context, id string) error
}
```

- [ ] **Step 2: Implement the new method in `postgres/user.go`**

Update `GetByID` and `GetByEmail` to scan the new column:

```go
const q = `SELECT id, email, role, stripe_customer_id, created_at, updated_at FROM users WHERE id = $1`
// ...
err := row.Scan(&user.ID, &user.Email, &user.Role, &user.StripeCustomerID, &user.CreatedAt, &user.UpdatedAt)
```

Add the new method at the bottom of the file:

```go
// SetStripeCustomerID writes the Stripe customer ID to the user row. It uses a
// guarded UPDATE so a racing write cannot replace an already-set customer ID
// with a different one — that would be a billing integrity breach.
func (repo *userRepository) SetStripeCustomerID(ctx context.Context, userID, stripeCustomerID string) error {
	const q = `UPDATE users
		SET stripe_customer_id = $1, updated_at = NOW()
		WHERE id = $2 AND (stripe_customer_id IS NULL OR stripe_customer_id = $1)`
	res, err := repo.db.ExecContext(ctx, q, stripeCustomerID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("user not found or stripe_customer_id already set to a different value")
	}
	return nil
}
```

- [ ] **Step 3: Create `billing/customer.go`**

```go
package billing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stripe/stripe-go/v82"
	stripeCustomer "github.com/stripe/stripe-go/v82/customer"
)

// CustomerRepo is the minimal dependency EnsureStripeCustomer needs to read
// and persist a user's stripe_customer_id. Satisfied by postgres.userRepository.
type CustomerRepo interface {
	SetStripeCustomerID(ctx context.Context, userID, stripeCustomerID string) error
}

// EnsureStripeCustomer creates a Stripe Customer for the given internal user
// if one does not exist, and persists the resulting cus_... to
// users.stripe_customer_id. It is safe to call multiple times — subsequent
// calls after a customer already exists are no-ops (the guarded UPDATE in
// SetStripeCustomerID tolerates re-writes of the same value).
//
// If existingCustomerID is non-empty, the function returns it unchanged
// without hitting Stripe — this is the callsite contract for "fast path on
// known user".
//
// idempotencyKey should be derived from the user ID so that a network retry
// does not create two Customers for the same user.
func (s *Service) EnsureStripeCustomer(
	ctx context.Context,
	userID, email, existingCustomerID string,
	repo CustomerRepo,
) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("missing user id")
	}
	if existingCustomerID != "" {
		return existingCustomerID, nil
	}

	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Metadata: map[string]string{
			"brezel_user_id": userID,
		},
	}
	// Idempotency key scoped to the user. If Stripe retries this call it must
	// return the same Customer object, not a duplicate.
	params.IdempotencyKey = stripe.String("customer:create:" + userID)

	cust, err := stripeCustomer.New(params)
	if err != nil {
		s.logger.Error("stripe_customer_create_failed",
			slog.String("user_id", userID),
			slog.Any("error", err),
		)
		return "", fmt.Errorf("failed to create stripe customer: %w", err)
	}

	if err := repo.SetStripeCustomerID(ctx, userID, cust.ID); err != nil {
		// We successfully created the Customer but failed to persist the link.
		// Log at ERROR so ops catches the orphan; the next call to
		// EnsureStripeCustomer will create another Customer (acceptable — we
		// log orphans for manual reconciliation rather than leaving the user
		// unable to check out).
		s.logger.Error("stripe_customer_persist_failed",
			slog.String("user_id", userID),
			slog.String("stripe_customer_id", cust.ID),
			slog.Any("error", err),
		)
		return "", fmt.Errorf("failed to persist stripe customer id: %w", err)
	}

	s.logger.Info("stripe_customer_created",
		slog.String("user_id", userID),
		slog.String("stripe_customer_id", cust.ID),
	)
	return cust.ID, nil
}
```

- [ ] **Step 4: Wire into user provisioning** — `web/auth/auth.go`

After the `userRepo.Create(r.Context(), &newUser)` call (currently line 145), add:

```go
// Create a matching Stripe Customer so we have a durable link for all future
// billing events. Failure here is non-fatal for the request — the user is
// signed up; billing lookups will lazy-create on the next checkout if we fall
// through. We log ERROR to surface orphans. (S-C3)
if m.billingSvc != nil {
	custID, err := m.billingSvc.EnsureStripeCustomer(r.Context(), newUser.ID, newUser.Email, "", m.userRepo)
	if err != nil {
		slog.Error("stripe_customer_ensure_failed_on_signup",
			slog.String("user_id", newUser.ID),
			slog.Any("error", err),
		)
	} else {
		newUser.StripeCustomerID = &custID
	}
}
```

The `m.billingSvc` dependency needs to be added to the auth middleware struct. Locate the struct (should be near the top of `auth.go`) and add:

```go
type AuthMiddleware struct {
	// ... existing fields ...
	billingSvc *billing.Service
}
```

Update the constructor signature and the webrunner wiring so `billingSvc` is passed in. If this creates a circular import, extract the `CustomerRepo` interface into `models` instead and have `billing.Service` depend on `models.UserRepository`.

- [ ] **Step 5: Pass `Customer` on checkout session creation**

In `billing/service.go:CreateCheckoutSession`, after the userID validation block and before building `params`, fetch the user's Stripe customer ID:

```go
// Look up the user's Stripe customer ID. If missing (legacy user who signed
// up before S-C3), lazy-create here. The lazy path costs one extra round trip
// the first time and is a no-op thereafter.
user, err := s.userRepo.GetByID(ctx, req.UserID)
if err != nil {
	return CheckoutResponse{}, fmt.Errorf("failed to look up user: %w", err)
}
var stripeCustomerID string
if user.StripeCustomerID != nil {
	stripeCustomerID = *user.StripeCustomerID
}
if stripeCustomerID == "" {
	stripeCustomerID, err = s.EnsureStripeCustomer(ctx, req.UserID, user.Email, "", s.userRepo)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("failed to ensure stripe customer: %w", err)
	}
}
```

Then inject into `params`:

```go
params := &stripe.CheckoutSessionParams{
	Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
	Customer:   stripe.String(stripeCustomerID), // <-- NEW
	SuccessURL: stripe.String(successURL),
	CancelURL:  stripe.String(cancelURL),
	// ...
}
```

The `billing.Service` struct needs a `userRepo` field. Add it:

```go
type Service struct {
	db                *sql.DB
	cfg               *config.Service
	webhookSigningKey string
	userRepo          models.UserRepository // <-- NEW
	logger            *slog.Logger
	metrics           *metrics.BillingMetrics
}
```

Update `billing.New(...)` to accept it and update the webrunner wiring.

- [ ] **Step 6: Write tests** — `billing/customer_test.go`

```go
package billing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeCustomerRepo struct {
	setCalls      int
	lastUserID    string
	lastCustomer  string
	errOnSet      error
}

func (f *fakeCustomerRepo) SetStripeCustomerID(ctx context.Context, userID, customerID string) error {
	f.setCalls++
	f.lastUserID = userID
	f.lastCustomer = customerID
	return f.errOnSet
}

func TestEnsureStripeCustomer_FastPathOnExistingCustomer(t *testing.T) {
	svc := newTestService(t)
	repo := &fakeCustomerRepo{}
	got, err := svc.EnsureStripeCustomer(context.Background(), "u1", "a@b.com", "cus_existing", repo)
	require.NoError(t, err)
	require.Equal(t, "cus_existing", got)
	require.Equal(t, 0, repo.setCalls, "should not persist when customer already exists")
}

func TestEnsureStripeCustomer_EmptyUserID(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.EnsureStripeCustomer(context.Background(), "", "a@b.com", "", &fakeCustomerRepo{})
	require.Error(t, err)
}

// Integration test with the Stripe mock — requires stripe-mock running locally.
// Gate behind a build tag so it doesn't run in unit CI.
// +build integration
func TestEnsureStripeCustomer_CreatesAndPersists(t *testing.T) {
	svc := newTestServiceWithStripeMock(t)
	repo := &fakeCustomerRepo{}
	got, err := svc.EnsureStripeCustomer(context.Background(), "u-int-1", "a@b.com", "", repo)
	require.NoError(t, err)
	require.Contains(t, got, "cus_")
	require.Equal(t, 1, repo.setCalls)
	require.Equal(t, got, repo.lastCustomer)
}
```

- [ ] **Step 7: Run the tests**

```bash
go test ./billing/ -run EnsureStripeCustomer -v
go test ./billing/ -run CheckoutSession -v
```

Expected: PASS on unit tests; integration test skipped unless `-tags=integration`.

- [ ] **Step 8: Add a webrunner startup task** to backfill Stripe customers for existing users

Create a one-shot backfill helper that runs once on startup if any users have `stripe_customer_id IS NULL`. This ensures pre-existing signups get a Customer before they try to check out.

Add to `billing/customer.go`:

```go
// BackfillStripeCustomers iterates users with NULL stripe_customer_id and
// creates + persists a Customer for each. Intended to run once on startup
// after the S-C3 migration lands. Caps at 1000 users per invocation to avoid
// long blocking startups; subsequent runs pick up where the previous left off.
func (s *Service) BackfillStripeCustomers(ctx context.Context, repo CustomerRepo) error {
	const sel = `SELECT id, email FROM users WHERE stripe_customer_id IS NULL LIMIT 1000`
	rows, err := s.db.QueryContext(ctx, sel)
	if err != nil {
		return fmt.Errorf("backfill query: %w", err)
	}
	defer rows.Close()

	var failed int
	var ok int
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			return err
		}
		if _, err := s.EnsureStripeCustomer(ctx, id, email, "", repo); err != nil {
			failed++
			s.logger.Error("backfill_customer_failed",
				slog.String("user_id", id),
				slog.Any("error", err),
			)
			continue
		}
		ok++
	}
	s.logger.Info("backfill_customers_done", slog.Int("ok", ok), slog.Int("failed", failed))
	return rows.Err()
}
```

Call this from `runner/webrunner/webrunner.go:Run()` after service construction but before HTTP listener start. Log the result. Do NOT fail startup on backfill errors.

- [ ] **Step 9: Commit**

```bash
git add models/user.go postgres/user.go billing/customer.go billing/customer_test.go \
        billing/service.go web/auth/auth.go runner/webrunner/webrunner.go
git commit -m "feat(billing): create Stripe Customer at user provisioning and persist ID (S-C3)

Every user now gets a Stripe Customer at signup. Checkout sessions pass the
Customer ID so all payment objects are linked. Refund/charge-failed handlers
can now find users via Customer fallback. Backfill runs once at startup.
"
```

---

### Task 1.4: Refund deficit ledger [S-C4, audit M-4 escalated]

**Context:** With S-C2 and S-C3 in place, the refund handler finally reaches its deduction branch. Now we confront the real problem: the `CHECK (credit_balance >= 0)` DB constraint. The current code caps silently at current balance, losing refund value — customers can spend credits, then refund on Stripe, and keep the consumed-credit value for free.

**Every pre-paid-credit SaaS you can name handles this the same way:**
- **Vercel:** Credits can't go negative in the UI, but the account gets flagged and future purchases apply to the deficit before any new balance is added.
- **Anthropic (Claude API):** Credits are non-refundable once consumed per ToS; refunds require manual support review.
- **OpenAI API:** Same policy — pre-paid credits are non-refundable.
- **Stripe Billing credits:** Credit grants are non-refundable; voiding a grant only works if the ledger balance is sufficient.

**The fix we're shipping:** add `users.refund_deficit_credits NUMERIC(18,6) NOT NULL DEFAULT 0 CHECK (refund_deficit_credits >= 0)`. When a refund exceeds current balance, zero the balance and write the remainder to the deficit. On the next purchase, apply the new credits to the deficit first, then to balance. `credit_balance >= 0` stays — spending is still blocked when balance is zero.

**Files:**
- Create: `scripts/migrations/000027_stripe_customer_and_refund_deficit.up.sql`
- Create: `scripts/migrations/000027_stripe_customer_and_refund_deficit.down.sql`
- Create: `billing/refund.go` (extract from `service.go`)
- Create: `billing/refund_test.go`
- Modify: `billing/service.go` — delete `handleChargeRefunded` (moved) and update `HandleWebhook` dispatch
- Modify: `billing/service.go` — `handleCheckoutSessionCompleted` to apply credits to deficit first
- Modify: `models/user.go` — add `RefundDeficitCredits float64` (or decimal)
- Modify: `postgres/user.go` — include in SELECTs

- [ ] **Step 1: Write the migration**

Create `scripts/migrations/000027_stripe_customer_and_refund_deficit.up.sql`:

```sql
BEGIN;

-- 1. Add refund deficit column: tracks uncollectable refund amounts when
-- credits have been consumed. Next purchase applies here first, then to balance.
-- The >= 0 constraint preserves the financial safety invariant.
ALTER TABLE users
    ADD COLUMN refund_deficit_credits NUMERIC(18,6) NOT NULL DEFAULT 0
        CHECK (refund_deficit_credits >= 0);

COMMENT ON COLUMN users.refund_deficit_credits IS
    'Uncollectable refund amount in credits. Set when a Stripe refund exceeds current balance because credits have already been consumed. Next purchase applies to deficit first.';

-- 2. Expand credit_transactions.type to allow refund_deficit entries so the
-- audit ledger has a dedicated row type for these events.
ALTER TABLE credit_transactions
    DROP CONSTRAINT credit_transactions_type_check;
ALTER TABLE credit_transactions
    ADD CONSTRAINT credit_transactions_type_check
        CHECK (type IN ('purchase', 'consumption', 'bonus', 'refund', 'refund_deficit', 'deficit_paydown', 'adjustment'));

-- 3. Expand stripe_payments.status to surface deficit-applied payments for
-- ops dashboards without requiring a join to credit_transactions.
ALTER TABLE stripe_payments
    DROP CONSTRAINT stripe_payments_status_check;
ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled',
                          'refunded', 'partial_refund', 'refund_deficit_applied'));

-- 4. Index on deficit for the ops dashboard query "users who owe us credits".
CREATE INDEX idx_users_refund_deficit ON users(refund_deficit_credits)
    WHERE refund_deficit_credits > 0;

COMMIT;
```

And `scripts/migrations/000027_stripe_customer_and_refund_deficit.down.sql`:

```sql
BEGIN;

DROP INDEX IF EXISTS idx_users_refund_deficit;

ALTER TABLE stripe_payments
    DROP CONSTRAINT stripe_payments_status_check;
ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled', 'refunded', 'partial_refund'));

ALTER TABLE credit_transactions
    DROP CONSTRAINT credit_transactions_type_check;
ALTER TABLE credit_transactions
    ADD CONSTRAINT credit_transactions_type_check
        CHECK (type IN ('purchase', 'consumption', 'bonus', 'refund', 'adjustment'));

ALTER TABLE users
    DROP COLUMN refund_deficit_credits;

COMMIT;
```

- [ ] **Step 2: Run the migration against a test DB**

```bash
# Against your local test DB:
psql -h localhost -U scraper -d brezel_test \
     -f scripts/migrations/000027_stripe_customer_and_refund_deficit.up.sql
```

Verify:

```sql
\d users
-- Should show refund_deficit_credits column
SELECT constraint_name FROM information_schema.table_constraints
 WHERE table_name = 'credit_transactions';
-- Should show the new credit_transactions_type_check
```

- [ ] **Step 3: Extract the refund handler to `billing/refund.go`**

Move `handleChargeRefunded` from `billing/service.go:765-965` into a new file `billing/refund.go`. The method stays on `*Service`. At the top of the new file add the license/package header:

```go
package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/stripe/stripe-go/v82"
)
```

Leave the function body unchanged for now — the next step replaces the cap-at-balance logic with the deficit ledger.

- [ ] **Step 4: Rewrite the deduction block to use the deficit ledger**

In `handleChargeRefunded`, locate the current deduction block (lines 893-919 in the old file). Replace the cap-at-balance logic with:

```go
// Convert to micro-credits for exact integer arithmetic.
balanceMicro := int64(math.Round(balanceFloat * models.MicroUnit))
deductMicro := int64(math.Round(creditsToDeduct * models.MicroUnit))

// Split the refund across (balance, deficit):
//   - deductFromBalance = min(deductMicro, balanceMicro)
//   - deductFromDeficit = deductMicro - deductFromBalance (always >= 0)
//
// The deficit portion represents credits that were already consumed before
// this refund arrived. Instead of failing the refund or silently losing
// integrity, we write the remainder to users.refund_deficit_credits so the
// next purchase pays it down before crediting new balance.
//
// This preserves the CHECK (credit_balance >= 0) invariant while making the
// ledger financially correct end-to-end. Ops can surface users with
// refund_deficit_credits > 0 as a fraud/support signal.
deductFromBalanceMicro := deductMicro
deductFromDeficitMicro := int64(0)
if deductFromBalanceMicro > balanceMicro {
	deductFromBalanceMicro = balanceMicro
	deductFromDeficitMicro = deductMicro - balanceMicro
}

newBalanceMicro := balanceMicro - deductFromBalanceMicro

actualDeductFloat := float64(deductFromBalanceMicro) / models.MicroUnit
newBalanceFloat := float64(newBalanceMicro) / models.MicroUnit
deficitIncreaseFloat := float64(deductFromDeficitMicro) / models.MicroUnit

// Update balance.
const updBalance = `UPDATE users
	SET credit_balance = $1::numeric,
	    refund_deficit_credits = refund_deficit_credits + $2::numeric,
	    updated_at = NOW()
	WHERE id = $3`
if _, err := tx.ExecContext(ctx, updBalance, newBalanceFloat, deficitIncreaseFloat, userID); err != nil {
	s.logger.Error("failed_to_deduct_credits_for_refund", slog.Any("error", err))
	return 500, fmt.Errorf("failed to deduct credits: %w", err)
}

// Record the balance-side deduction as a 'refund' credit_transaction.
if deductFromBalanceMicro > 0 {
	const insTxnRefund = `INSERT INTO credit_transactions
		(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
		VALUES ($1, $2, 'refund', $3, $4, $5, $6, $7, 'payment')`
	desc := fmt.Sprintf("Stripe refund for charge %s", charge.ID)
	if _, err := tx.ExecContext(ctx, insTxnRefund,
		uuid.Must(uuid.NewV7()).String(), userID, -actualDeductFloat,
		balanceFloat, newBalanceFloat, desc, charge.ID); err != nil {
		s.logger.Error("failed_to_insert_refund_transaction", slog.Any("error", err))
		return 500, fmt.Errorf("failed to insert refund transaction: %w", err)
	}
}

// Record the deficit-side portion as a separate 'refund_deficit' row. This
// row has amount=0 on the balance ledger (balance_before == balance_after)
// because the deficit is tracked outside the spendable balance; the
// description field captures the deficit amount for audit.
if deductFromDeficitMicro > 0 {
	const insTxnDeficit = `INSERT INTO credit_transactions
		(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type, metadata)
		VALUES ($1, $2, 'refund_deficit', 0, $3, $3, $4, $5, 'payment', $6::jsonb)`
	desc := fmt.Sprintf("Stripe refund deficit for charge %s: %.6f credits uncollectable", charge.ID, deficitIncreaseFloat)
	metadata := fmt.Sprintf(`{"deficit_amount":%q,"charge_id":%q}`,
		strconv.FormatFloat(deficitIncreaseFloat, 'f', 6, 64), charge.ID)
	if _, err := tx.ExecContext(ctx, insTxnDeficit,
		uuid.Must(uuid.NewV7()).String(), userID, newBalanceFloat, desc, charge.ID, metadata); err != nil {
		s.logger.Error("failed_to_insert_refund_deficit_transaction", slog.Any("error", err))
		return 500, fmt.Errorf("failed to insert refund deficit transaction: %w", err)
	}

	// Emit a metric and an ERROR-level log so ops sees every deficit event.
	// This is the signal that a user bought, consumed, then refunded —
	// possibly benign (change of mind mid-use) or possibly fraud.
	s.metrics.RefundDeficitAppliedTotal.Inc()
	s.logger.Error("refund_deficit_applied",
		slog.String("user_id", userID),
		slog.String("charge_id", charge.ID),
		slog.Float64("deficit_credits", deficitIncreaseFloat),
		slog.Float64("actual_balance_deduction", actualDeductFloat),
		slog.Float64("original_balance", balanceFloat),
	)
}
```

Delete the old `capApplied` / `refund_cap_applied` / `actualDeductMicro` block. The metric `RefundCapAppliedTotal` is replaced by `RefundDeficitAppliedTotal` (add it to `pkg/metrics/billing.go` alongside or in place of the old one).

- [ ] **Step 5: Update `handleCheckoutSessionCompleted` to pay down deficit first**

Inside the existing transaction, after the balance lock (`SELECT credit_balance ... FOR UPDATE` at line 264) and before the `UPDATE users SET credit_balance = ... + $1` (line 275), add:

```go
// Apply the incoming credits to the refund deficit first. Any remainder
// goes to spendable balance. This is what makes the refund deficit ledger
// self-correcting: users who owe us credits from a past refund pay it back
// through their next purchase.
var deficitMicro int64
{
	var deficitStr string
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(refund_deficit_credits, 0)::text FROM users WHERE id = $1 FOR UPDATE",
		userID).Scan(&deficitStr); err != nil {
		s.logger.Error("failed_to_read_refund_deficit", slog.Any("error", err))
		return 500, fmt.Errorf("failed to read refund deficit: %w", err)
	}
	deficitFloat, _ := strconv.ParseFloat(deficitStr, 64)
	deficitMicro = int64(math.Round(deficitFloat * models.MicroUnit))
}

purchaseMicro := int64(credits) * models.MicroUnit
appliedToDeficitMicro := int64(0)
appliedToBalanceMicro := purchaseMicro
if deficitMicro > 0 {
	if purchaseMicro >= deficitMicro {
		appliedToDeficitMicro = deficitMicro
		appliedToBalanceMicro = purchaseMicro - deficitMicro
	} else {
		appliedToDeficitMicro = purchaseMicro
		appliedToBalanceMicro = 0
	}
}

appliedToDeficitFloat := float64(appliedToDeficitMicro) / models.MicroUnit
appliedToBalanceFloat := float64(appliedToBalanceMicro) / models.MicroUnit

// If any portion went to deficit, record a 'deficit_paydown' ledger row and
// decrement users.refund_deficit_credits in the same UPDATE below.
if appliedToDeficitMicro > 0 {
	const insTxnPaydown = `INSERT INTO credit_transactions
		(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
		VALUES ($1, $2, 'deficit_paydown', 0, $3, $3, $4, $5, 'payment')`
	desc := fmt.Sprintf("Deficit paydown from Stripe purchase %s: %.6f credits", session.ID, appliedToDeficitFloat)
	if _, err := tx.ExecContext(ctx, insTxnPaydown,
		uuid.Must(uuid.NewV7()).String(), userID, currentBalance, desc, session.ID); err != nil {
		return 500, fmt.Errorf("failed to insert deficit paydown transaction: %w", err)
	}
}
```

Then replace the existing balance update at line 275 with:

```go
const updUser = `UPDATE users SET
	credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
	total_credits_purchased = COALESCE(total_credits_purchased, 0) + $2::numeric,
	refund_deficit_credits = GREATEST(0, refund_deficit_credits - $3::numeric),
	updated_at = NOW()
	WHERE id = $4`
result, err := tx.ExecContext(ctx, updUser,
	appliedToBalanceFloat,   // $1: only the balance portion
	credits,                 // $2: full purchase amount for lifetime tracking
	appliedToDeficitFloat,   // $3: deficit paydown portion
	userID)
```

Note `total_credits_purchased` tracks the full purchase regardless of where it landed — that's the user's lifetime value, not a ledger balance.

- [ ] **Step 6: Write tests** — `billing/refund_test.go`

```go
package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82"
)

func TestHandleChargeRefunded_FullBalanceAvailable(t *testing.T) {
	svc, db := newTestServiceWithDB(t)
	userID := "user-refund-1"
	piID := "pi_test_1"
	insertTestUser(t, db, userID)
	insertTestUserBalance(t, db, userID, 100)
	insertSucceededPayment(t, db, userID, "cs_test_1", piID, 100)

	event := makeChargeRefundedEvent(t, "ch_test_1", piID, 10000, 10000) // full refund
	code, err := svc.handleChargeRefunded(context.Background(), event)
	require.NoError(t, err)
	require.Equal(t, 200, code)

	var balance, deficit float64
	require.NoError(t, db.QueryRow(
		"SELECT credit_balance::float8, refund_deficit_credits::float8 FROM users WHERE id = $1",
		userID).Scan(&balance, &deficit))
	require.InDelta(t, 0.0, balance, 0.000001)
	require.InDelta(t, 0.0, deficit, 0.000001)
}

func TestHandleChargeRefunded_PartialBalanceCreatesDeficit(t *testing.T) {
	svc, db := newTestServiceWithDB(t)
	userID := "user-refund-2"
	piID := "pi_test_2"
	insertTestUser(t, db, userID)
	// User bought 100, spent 95, has 5 left.
	insertTestUserBalance(t, db, userID, 5)
	insertSucceededPayment(t, db, userID, "cs_test_2", piID, 100)

	// Full refund of the original 100-credit purchase.
	event := makeChargeRefundedEvent(t, "ch_test_2", piID, 10000, 10000)
	code, err := svc.handleChargeRefunded(context.Background(), event)
	require.NoError(t, err)
	require.Equal(t, 200, code)

	var balance, deficit float64
	require.NoError(t, db.QueryRow(
		"SELECT credit_balance::float8, refund_deficit_credits::float8 FROM users WHERE id = $1",
		userID).Scan(&balance, &deficit))
	require.InDelta(t, 0.0, balance, 0.000001, "balance should zero out")
	require.InDelta(t, 95.0, deficit, 0.000001, "deficit = 100 refund - 5 balance = 95")

	// Verify ledger rows.
	var refundRows, deficitRows int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM credit_transactions WHERE user_id = $1 AND type = 'refund'",
		userID).Scan(&refundRows))
	require.Equal(t, 1, refundRows)
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM credit_transactions WHERE user_id = $1 AND type = 'refund_deficit'",
		userID).Scan(&deficitRows))
	require.Equal(t, 1, deficitRows)
}

func TestHandleChargeRefunded_ZeroBalanceAllDeficit(t *testing.T) {
	svc, db := newTestServiceWithDB(t)
	userID := "user-refund-3"
	piID := "pi_test_3"
	insertTestUser(t, db, userID)
	// User spent everything — balance is 0.
	insertTestUserBalance(t, db, userID, 0)
	insertSucceededPayment(t, db, userID, "cs_test_3", piID, 100)

	event := makeChargeRefundedEvent(t, "ch_test_3", piID, 10000, 10000)
	code, err := svc.handleChargeRefunded(context.Background(), event)
	require.NoError(t, err)
	require.Equal(t, 200, code)

	var balance, deficit float64
	require.NoError(t, db.QueryRow(
		"SELECT credit_balance::float8, refund_deficit_credits::float8 FROM users WHERE id = $1",
		userID).Scan(&balance, &deficit))
	require.InDelta(t, 0.0, balance, 0.000001)
	require.InDelta(t, 100.0, deficit, 0.000001)
}

func TestHandleCheckoutSessionCompleted_PaysDownDeficitFirst(t *testing.T) {
	svc, db := newTestServiceWithDB(t)
	userID := "user-paydown-1"
	insertTestUser(t, db, userID)
	// Simulate existing deficit: user owes us 30 credits.
	_, err := db.Exec("UPDATE users SET refund_deficit_credits = 30 WHERE id = $1", userID)
	require.NoError(t, err)
	insertPendingStripePayment(t, db, userID, "cs_test_paydown_1", 100)

	event := makeCheckoutSessionCompletedEvent(t, "cs_test_paydown_1", "pi_test_paydown_1", userID, 100)
	code, err := svc.handleCheckoutSessionCompleted(context.Background(), event)
	require.NoError(t, err)
	require.Equal(t, 200, code)

	var balance, deficit, lifetime float64
	require.NoError(t, db.QueryRow(
		"SELECT credit_balance::float8, refund_deficit_credits::float8, total_credits_purchased::float8 FROM users WHERE id = $1",
		userID).Scan(&balance, &deficit, &lifetime))
	require.InDelta(t, 70.0, balance, 0.000001, "balance = 100 purchase - 30 paydown")
	require.InDelta(t, 0.0, deficit, 0.000001)
	require.InDelta(t, 100.0, lifetime, 0.000001, "lifetime credits tracks full purchase")
}

func TestHandleCheckoutSessionCompleted_PurchaseSmallerThanDeficit(t *testing.T) {
	svc, db := newTestServiceWithDB(t)
	userID := "user-paydown-2"
	insertTestUser(t, db, userID)
	_, err := db.Exec("UPDATE users SET refund_deficit_credits = 100 WHERE id = $1", userID)
	require.NoError(t, err)
	insertPendingStripePayment(t, db, userID, "cs_test_paydown_2", 30)

	event := makeCheckoutSessionCompletedEvent(t, "cs_test_paydown_2", "pi_test_paydown_2", userID, 30)
	code, err := svc.handleCheckoutSessionCompleted(context.Background(), event)
	require.NoError(t, err)
	require.Equal(t, 200, code)

	var balance, deficit float64
	require.NoError(t, db.QueryRow(
		"SELECT credit_balance::float8, refund_deficit_credits::float8 FROM users WHERE id = $1",
		userID).Scan(&balance, &deficit))
	require.InDelta(t, 0.0, balance, 0.000001, "all 30 credits applied to deficit")
	require.InDelta(t, 70.0, deficit, 0.000001, "deficit = 100 - 30 = 70")
}
```

- [ ] **Step 7: Run the tests**

```bash
go test ./billing/ -run "Refund|Paydown|CheckoutSession" -v
```

Expected: PASS

- [ ] **Step 8: Register the new metric**

In `pkg/metrics/billing.go`, add:

```go
RefundDeficitAppliedTotal prometheus.Counter
```

Initialize it in `NewBillingMetrics`:

```go
RefundDeficitAppliedTotal: factory.NewCounter(prometheus.CounterOpts{
	Name: "billing_refund_deficit_applied_total",
	Help: "Count of charge.refunded events that exceeded current balance and created a refund deficit. Any non-zero value indicates a user bought credits, consumed them, and then refunded. Alert on rate > 0.",
}),
```

If you're keeping the old `RefundCapAppliedTotal` counter during the transition, mark it deprecated. Otherwise delete it and the Grafana alert rule — the deficit counter replaces it.

- [ ] **Step 9: Commit**

```bash
git add scripts/migrations/000027_* billing/refund.go billing/refund_test.go \
        billing/service.go pkg/metrics/billing.go models/user.go postgres/user.go
git commit -m "feat(billing): refund deficit ledger (S-C4, covers audit M-4)

When a Stripe refund exceeds current credit balance (because credits have
been consumed), the uncollectable portion is written to
users.refund_deficit_credits. The next purchase pays down the deficit before
crediting spendable balance. Preserves the CHECK (credit_balance >= 0)
invariant. Matches Vercel/Anthropic/OpenAI pre-paid credit refund policy.

Tests cover:
- Full refund with sufficient balance
- Partial refund creating deficit
- Zero balance full-deficit
- Paydown on next purchase (purchase > deficit and purchase <= deficit)
"
```

---

### Task 1.5: Fail-fast on missing production secrets [S-C5, audit M-7]

**Files:**
- Modify: `runner/webrunner/webrunner.go`
- Test: `runner/webrunner/webrunner_startup_test.go` (create if missing)

- [ ] **Step 1: Write the failing test**

```go
package webrunner

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateProductionSecrets_FailsWhenStripeSecretMissing(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	err := validateProductionSecrets()
	require.Error(t, err)
	require.Contains(t, err.Error(), "STRIPE_WEBHOOK_SECRET")
}

func TestValidateProductionSecrets_FailsWhenEncryptionKeyMissing(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("ENCRYPTION_KEY", "")
	err := validateProductionSecrets()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ENCRYPTION_KEY")
}

func TestValidateProductionSecrets_PassesInDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("ENCRYPTION_KEY", "")
	require.NoError(t, validateProductionSecrets())
}

func TestValidateProductionSecrets_PassesWhenAllSet(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	require.NoError(t, validateProductionSecrets())
}
```

- [ ] **Step 2: Run and confirm failure**

```bash
go test ./runner/webrunner/ -run TestValidateProductionSecrets -v
```

Expected: FAIL — `validateProductionSecrets` doesn't exist.

- [ ] **Step 3: Implement**

In `runner/webrunner/webrunner.go`, add near the top of the file:

```go
// validateProductionSecrets refuses to start the web server in production if
// security-critical environment variables are missing. Called from Run()
// before any HTTP listener is opened. Non-production environments are
// permissive — local dev and integration tests do not need these set.
func validateProductionSecrets() error {
	if os.Getenv("APP_ENV") != "production" {
		return nil
	}
	var missing []string
	if os.Getenv("STRIPE_WEBHOOK_SECRET") == "" {
		missing = append(missing, "STRIPE_WEBHOOK_SECRET")
	}
	if os.Getenv("ENCRYPTION_KEY") == "" {
		missing = append(missing, "ENCRYPTION_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("production startup aborted: missing required environment variables: %v", missing)
	}
	return nil
}
```

Call it from `Run()`:

```go
func Run(ctx context.Context, cfg Config) error {
	if err := validateProductionSecrets(); err != nil {
		return err
	}
	// ... existing body ...
}
```

- [ ] **Step 4: Run and confirm PASS**

```bash
go test ./runner/webrunner/ -run TestValidateProductionSecrets -v
```

- [ ] **Step 5: Commit**

```bash
git add runner/webrunner/webrunner.go runner/webrunner/webrunner_startup_test.go
git commit -m "feat(webrunner): fail-fast on missing production secrets (S-C5, audit M-7)"
```

---

## Chunk 2: P1 High Findings

### Task 2.1: Stripe `Idempotency-Key` on all POST API calls [S-H1]

**Context:** `stripe-go/v82` supports per-request idempotency keys via `params.Params.IdempotencyKey`. Stripe docs (Advanced error handling) are unambiguous: "Use [idempotency keys] for all POST requests to the Stripe API." A 24-hour replay window prevents duplicate Customer creation, duplicate sessions, duplicate refunds.

**Files:**
- Modify: `billing/service.go` — `CreateCheckoutSession` (already touched in Task 1.3)
- Modify: `billing/customer.go` — already covers `EnsureStripeCustomer` in Task 1.3 Step 3
- Modify: `billing/service.go` — `ReconcileSession`? (no: `checkoutsession.Get` is a GET, no idempotency needed)

- [ ] **Step 1: Audit the existing code for all POST callsites**

Run:

```bash
grep -rn "stripe.*\.New(" billing/ | grep -v "_test.go"
```

Expected callsites after S-C3:
- `billing/customer.go`: `stripeCustomer.New(params)` — already has IdempotencyKey per Task 1.3
- `billing/service.go:112`: `checkoutsession.New(params)` — needs IdempotencyKey
- Any future `refund.New()` — not used today

- [ ] **Step 2: Write the test**

In `billing/service_test.go`:

```go
func TestCreateCheckoutSession_SetsIdempotencyKey(t *testing.T) {
	// This requires a Stripe mock that captures request headers.
	// Use stripe-mock and inspect the captured Idempotency-Key header.
	mock := newStripeMockServer(t)
	svc := newTestServiceWithMock(t, mock)

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutRequest{
		UserID: "u1", Credits: "100", Currency: "USD",
	})
	require.NoError(t, err)
	headers := mock.LastRequestHeaders()
	key := headers.Get("Idempotency-Key")
	require.NotEmpty(t, key, "Idempotency-Key header must be set on Stripe API call")
	require.True(t, strings.HasPrefix(key, "checkout:"), "key should be scoped to checkout flow")
}
```

If you don't have a Stripe mock yet, stub it as a `t.Skip` with a TODO and instead write a unit test that inspects `params.IdempotencyKey` directly via a test helper that extracts it.

- [ ] **Step 3: Add the idempotency key to the params**

In `billing/service.go:CreateCheckoutSession`, before `checkoutsession.New(params)`:

```go
// Scope the idempotency key to user + credits + currency + a 1-hour time bucket.
// A 1-hour bucket means repeated checkout attempts within an hour collapse to
// the same Stripe session; attempts across hours create fresh sessions so a
// user who closed the tab can retry.
bucket := time.Now().UTC().Truncate(time.Hour).Unix()
idempotencyKey := fmt.Sprintf("checkout:%s:%d:%s:%d", req.UserID, creditsInt, req.Currency, bucket)
params.Params.IdempotencyKey = stripe.String(idempotencyKey)
```

- [ ] **Step 4: Run tests and commit**

```bash
go test ./billing/ -run CheckoutSession -v
git add billing/service.go billing/service_test.go
git commit -m "feat(billing): idempotency key on checkout session creation (S-H1)"
```

---

### Task 2.2: Decimal-safe refund math [S-H2]

**Context:** `billing/service.go:827` computes `creditsToDeduct = (float64(AmountRefunded) / float64(amountCents)) * creditsGranted`. Round-trip through `float8` scan + `math.Round(x * MicroUnit)` is fragile. Use text-based decimal via Postgres `NUMERIC` throughout the math path, avoiding float entirely until the final render step.

**Files:**
- Modify: `billing/refund.go` (created in Task 1.4)
- Dependency: already using `NUMERIC(18,6)` in DB; add `github.com/shopspring/decimal` if not present

- [ ] **Step 1: Add the dependency (if not already present)**

```bash
cd brezelscraper-backend
go list -m github.com/shopspring/decimal 2>/dev/null || go get github.com/shopspring/decimal@latest
go mod tidy
```

- [ ] **Step 2: Rewrite the refund proportional math to use decimal**

In `billing/refund.go`, replace the `creditsToDeduct` calculation:

```go
import "github.com/shopspring/decimal"

// ... inside handleChargeRefunded, replace lines ~823-828 ...

// Scan credits_purchased and amount_cents as text so we can do exact decimal
// arithmetic. Do NOT round-trip through float8.
var (
	userID               string
	creditsGrantedStr    string
	amountCents          int64
)
const sel = `SELECT user_id, credits_purchased::text, amount_cents
             FROM stripe_payments WHERE stripe_payment_intent_id = $1 LIMIT 1`
err := s.db.QueryRowContext(ctx, sel, paymentIntentID).Scan(&userID, &creditsGrantedStr, &amountCents)
if err != nil && err != sql.ErrNoRows {
	// ... existing error handling ...
}

// Proportional deduction: credits_to_deduct = (refunded / original) * credits_granted
var creditsToDeductDec decimal.Decimal
if creditsGrantedStr != "" && amountCents > 0 {
	creditsGranted, dErr := decimal.NewFromString(creditsGrantedStr)
	if dErr != nil {
		return 500, fmt.Errorf("parse credits_granted: %w", dErr)
	}
	refunded := decimal.NewFromInt(charge.AmountRefunded)
	original := decimal.NewFromInt(amountCents)
	creditsToDeductDec = refunded.Div(original).Mul(creditsGranted).Round(6)
}
```

And replace the balance-side math:

```go
// Read balance as text — avoid float round-trip.
var balanceStr string
err = tx.QueryRowContext(ctx,
	"SELECT COALESCE(credit_balance, 0)::text FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&balanceStr)
if err != nil {
	return 500, fmt.Errorf("failed to get user balance: %w", err)
}
balanceDec, err := decimal.NewFromString(balanceStr)
if err != nil {
	return 500, fmt.Errorf("parse balance: %w", err)
}

// Compute splits using decimal comparison.
var deductFromBalance, deductFromDeficit decimal.Decimal
if creditsToDeductDec.LessThanOrEqual(balanceDec) {
	deductFromBalance = creditsToDeductDec
	deductFromDeficit = decimal.Zero
} else {
	deductFromBalance = balanceDec
	deductFromDeficit = creditsToDeductDec.Sub(balanceDec)
}

newBalanceDec := balanceDec.Sub(deductFromBalance)

// Pass strings to the DB update so Postgres NUMERIC handles the arithmetic.
const updBalance = `UPDATE users
	SET credit_balance = $1::numeric,
	    refund_deficit_credits = refund_deficit_credits + $2::numeric,
	    updated_at = NOW()
	WHERE id = $3`
if _, err := tx.ExecContext(ctx, updBalance,
	newBalanceDec.StringFixed(6),
	deductFromDeficit.StringFixed(6),
	userID); err != nil {
	return 500, fmt.Errorf("failed to deduct credits: %w", err)
}
```

Delete the old `balanceFloat`, `balanceMicro`, `deductMicro`, and `models.MicroUnit`-based block. All `math.Round(x * MicroUnit)` calls are gone — decimal handles precision exactly.

- [ ] **Step 3: Add property-based tests for the decimal math**

In `billing/refund_test.go`:

```go
func TestRefundMath_DecimalExactness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		balance        string
		creditsGranted string
		amountCents    int64
		refundedCents  int64
		wantDeducted   string
		wantDeficit    string
	}{
		{"full_refund_full_balance", "100", "100", 10000, 10000, "100", "0"},
		{"partial_refund_full_balance", "100", "100", 10000, 5000, "50", "0"},
		{"full_refund_partial_balance", "5", "100", 10000, 10000, "5", "95"},
		{"partial_refund_partial_balance", "30", "100", 10000, 5000, "30", "20"},
		{"zero_balance_creates_full_deficit", "0", "100", 10000, 10000, "0", "100"},
		{"fractional_credits", "2.5", "7.5", 75000, 50000, "2.5", "2.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use the extracted helper (see Step 4 below).
			deducted, deficit := computeRefundSplit(
				decimalFromString(t, tt.balance),
				decimalFromString(t, tt.creditsGranted),
				tt.amountCents, tt.refundedCents,
			)
			require.Equal(t, tt.wantDeducted, deducted.StringFixed(1))
			require.Equal(t, tt.wantDeficit, deficit.StringFixed(1))
		})
	}
}
```

- [ ] **Step 4: Extract a pure helper for testability**

In `billing/refund.go`:

```go
// computeRefundSplit calculates the proportional refund credit amount and
// splits it across (deductFromBalance, deductFromDeficit). Pure function; no
// database access. Extracted to enable exhaustive table-driven testing.
func computeRefundSplit(
	balance, creditsGranted decimal.Decimal,
	amountCents, refundedCents int64,
) (deductFromBalance, deductFromDeficit decimal.Decimal) {
	if amountCents <= 0 || creditsGranted.IsZero() {
		return decimal.Zero, decimal.Zero
	}
	refunded := decimal.NewFromInt(refundedCents)
	original := decimal.NewFromInt(amountCents)
	creditsToDeduct := refunded.Div(original).Mul(creditsGranted).Round(6)
	if creditsToDeduct.LessThanOrEqual(balance) {
		return creditsToDeduct, decimal.Zero
	}
	return balance, creditsToDeduct.Sub(balance)
}
```

Use it in `handleChargeRefunded` in place of the inline math.

- [ ] **Step 5: Run the tests**

```bash
go test ./billing/ -run "Refund|computeRefundSplit" -v
```

- [ ] **Step 6: Commit**

```bash
git add billing/refund.go billing/refund_test.go go.mod go.sum
git commit -m "refactor(billing): decimal-safe refund math (S-H2)

Replaces float64 arithmetic with shopspring/decimal throughout the refund
path. Pure helper computeRefundSplit is table-tested for exactness across
edge cases including fractional credits.
"
```

---

### Task 2.3: Strict credit parsing [S-H3] — already covered in Task 1.1

Task 1.1 Step 1 introduced `parseCreditsStrict`, which already implements this. Verify by re-running the test suite and tick this finding off without a new commit.

- [ ] **Step 1: Re-run the parse tests**

```bash
go test ./billing/ -run TestParseCreditsStrict -v
```

- [ ] **Step 2: Confirm `fmt.Sscan` is gone from the codebase**

```bash
grep -rn "fmt.Sscan" billing/
```

Expected: no hits. If any remain, they're unrelated; document them separately.

---

### Task 2.4: `client_reference_id` + richer metadata on checkout session [S-H4]

**Context:** Stripe's docs and metadata use-cases guide recommend storing your internal user ID on both `ClientReferenceID` (searchable in Dashboard) *and* `Metadata` (durable, copied to Charges). Today we only use `Metadata`.

**Files:**
- Modify: `billing/service.go` — `CreateCheckoutSession`
- Test: `billing/service_test.go`

- [ ] **Step 1: Add `ClientReferenceID`**

In the `CheckoutSessionParams` block:

```go
params := &stripe.CheckoutSessionParams{
	Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
	Customer:          stripe.String(stripeCustomerID),
	ClientReferenceID: stripe.String(req.UserID), // <-- NEW: searchable in Stripe Dashboard
	SuccessURL:        stripe.String(successURL),
	CancelURL:         stripe.String(cancelURL),
	// ...
	PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{
		Metadata: map[string]string{
			"brezel_user_id": req.UserID,
			"credits":        strconv.Itoa(creditsInt),
		},
		Description: stripe.String(fmt.Sprintf("Brezel Credits x%d", creditsInt)),
	},
}
```

`PaymentIntentData.Metadata` copies down to the underlying PaymentIntent + Charge, which matters because the `charge.refunded` webhook receives the Charge object — having the user ID on the Charge directly avoids needing to join through `stripe_payments`.

- [ ] **Step 2: Write a test**

```go
func TestCreateCheckoutSession_SetsClientReferenceID(t *testing.T) {
	// Inspect params via a test hook or mock. Assert params.ClientReferenceID matches userID.
}
```

- [ ] **Step 3: Commit**

```bash
git add billing/service.go billing/service_test.go
git commit -m "feat(billing): set ClientReferenceID + PaymentIntent metadata (S-H4)"
```

---

### Task 2.5: Handle `charge.dispute.created` [S-H5]

**Context:** Chargebacks are refunds with a dispute fee ($15–$25 from Stripe) and a response deadline. They need their own event handler. At minimum: record the dispute, freeze the user's ability to spend (optional policy), and alert ops.

**Files:**
- Modify: `billing/refund.go` or new `billing/dispute.go`
- Migration: extend `stripe_payments.status` CHECK to include `'disputed'`
- Test: `billing/dispute_test.go`

- [ ] **Step 1: Extend the migration**

In `000027_stripe_customer_and_refund_deficit.up.sql` (still in progress), add `'disputed'` to the stripe_payments status CHECK:

```sql
ALTER TABLE stripe_payments
    DROP CONSTRAINT stripe_payments_status_check;
ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled',
                          'refunded', 'partial_refund', 'refund_deficit_applied', 'disputed'));
```

- [ ] **Step 2: Add the handler**

```go
func (s *Service) handleChargeDisputeCreated(ctx context.Context, event stripe.Event) (int, error) {
	var dispute stripe.Dispute
	if err := json.Unmarshal(event.Data.Raw, &dispute); err != nil {
		return 400, fmt.Errorf("failed to parse dispute: %w", err)
	}

	s.logger.Error("charge_dispute_created",
		slog.String("dispute_id", dispute.ID),
		slog.String("charge_id", dispute.Charge.ID),
		slog.Int64("amount_cents", dispute.Amount),
		slog.String("reason", string(dispute.Reason)),
	)
	s.metrics.DisputeCreatedTotal.Inc()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 500, err
	}
	defer tx.Rollback()

	isDup, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		return 500, err
	}
	if isDup {
		return 200, nil
	}

	if dispute.Charge != nil {
		const upd = `UPDATE stripe_payments SET status = 'disputed', updated_at = NOW()
		             WHERE stripe_payment_intent_id = (
		                 SELECT payment_intent FROM stripe_charges_view WHERE charge_id = $1
		             )`
		// Simpler: match on a charge_id → payment_intent lookup via Stripe's own structure.
		// Practical: fetch the charge and get its PI id.
		_ = upd
		// TODO: expand when we have a charge→payment_intent cache.
	}

	return 200, tx.Commit()
}
```

- [ ] **Step 3: Register the handler in `HandleWebhook` dispatch**

In `billing/service.go:HandleWebhook`, add the case:

```go
case "charge.dispute.created":
	return s.handleChargeDisputeCreated(ctx, event)
case "charge.dispute.closed":
	return s.handleChargeDisputeClosed(ctx, event) // stub for now; record outcome
```

- [ ] **Step 4: Add the metric**

```go
// pkg/metrics/billing.go
DisputeCreatedTotal prometheus.Counter
```

- [ ] **Step 5: Write a test + commit**

```go
func TestHandleChargeDisputeCreated_RecordsDisputeStatus(t *testing.T) { /* ... */ }
```

```bash
git add billing/dispute.go billing/dispute_test.go billing/service.go pkg/metrics/billing.go scripts/migrations/000027_*
git commit -m "feat(billing): handle charge.dispute.created webhook (S-H5)"
```

---

## Chunk 3: P2 Medium Findings

### Task 3.1: Remove `session.Metadata` fallback for credit amounts [S-M1]

**File:** `billing/service.go:handleCheckoutSessionCompleted`, lines 206-219

- [ ] **Step 1: Delete the fallback block**

Delete the `if userID == "" && session.Metadata != nil { ... }` block. Replace with:

```go
if err == sql.ErrNoRows {
	// No DB row for this session. Options:
	// 1) The row was deleted (manual ops intervention) — do nothing, ack 200.
	// 2) We missed the CreateCheckoutSession insert — a real bug worth alerting.
	s.logger.Error("checkout_completed_missing_db_row",
		slog.String("session_id", session.ID),
		slog.String("event_id", event.ID),
	)
	s.metrics.CheckoutMissingRowTotal.Inc()
	return 200, nil // ack to prevent Stripe retry storm; rely on metric alert
}
```

The DB row is authoritative. Metadata is a foot-gun because any future code that lets a client influence metadata turns this into a free-credits vuln.

- [ ] **Step 2: Commit**

```bash
git commit -am "fix(billing): DB row is authoritative on checkout completion; drop metadata fallback (S-M1)"
```

---

### Task 3.2: Expand `line_items` + `payment_intent` in `ReconcileSession` [S-M2]

**File:** `billing/service.go:ReconcileSession` line 385

- [ ] **Step 1: Pass expand params**

```go
params := &stripe.CheckoutSessionParams{
	Expand: []*string{
		stripe.String("line_items"),
		stripe.String("payment_intent"),
		stripe.String("payment_intent.latest_charge"),
	},
}
sess, err := checkoutsession.Get(sessionID, params)
```

- [ ] **Step 2: Verify line-item quantity matches DB row**

```go
if sess.LineItems != nil && len(sess.LineItems.Data) > 0 {
	var totalQty int64
	for _, li := range sess.LineItems.Data {
		totalQty += li.Quantity
	}
	if totalQty != int64(credits) {
		s.logger.Error("reconcile_line_item_mismatch",
			slog.String("session_id", sessionID),
			slog.Int64("stripe_quantity", totalQty),
			slog.Int("db_credits", credits),
		)
		return fmt.Errorf("line item quantity mismatch: db=%d stripe=%d", credits, totalQty)
	}
}
```

- [ ] **Step 3: Commit** — `git commit -am "feat(billing): expand line_items in reconcile and verify quantity (S-M2)"`

---

### Task 3.3: `refund.updated` handler [S-M3]

**Context:** Modern Stripe async refund methods (SEPA, Bacs) can succeed initially and then fail later. We need to listen for `refund.updated` and reverse the deduction if the refund transitioned to `failed`.

- [ ] **Step 1: Add the case to `HandleWebhook`** — stub handler that logs and returns 200 for now; write full logic only when enabling non-card payment methods.

- [ ] **Step 2: Commit.**

---

### Task 3.4: Async payment success/failure handlers [S-M4]

- [ ] **Step 1: Add stub handlers** for `checkout.session.async_payment_succeeded` and `checkout.session.async_payment_failed` that log but don't process. Required before enabling ACH/SEPA/Bacs. Mirror `handleCheckoutSessionCompleted` when implementing.

- [ ] **Step 2: Commit.**

---

### Task 3.5: Repair `handleChargeFailed` [S-M5]

**File:** `billing/service.go:969`

**Context:** After S-C3 the Customer fallback works, but the handler only has ONE lookup path. Add a PaymentIntent fallback for robustness.

- [ ] **Step 1: Add PaymentIntent-based lookup**

Insert before the Customer lookup:

```go
userID := ""
if charge.PaymentIntent != nil && charge.PaymentIntent.ID != "" {
	_ = s.db.QueryRowContext(ctx,
		"SELECT user_id FROM stripe_payments WHERE stripe_payment_intent_id = $1 LIMIT 1",
		charge.PaymentIntent.ID).Scan(&userID)
}
```

Then fall through to the Customer lookup only if still empty.

- [ ] **Step 2: Commit.**

---

### Task 3.6: Persist `stripe_receipt_url` [S-M6]

**Context:** Column exists (`stripe_payments.stripe_receipt_url`) but is never written. Populate it from `PaymentIntent.LatestCharge.ReceiptURL` inside `handleCheckoutSessionCompleted` after the PI ID backfill.

- [ ] **Step 1: Expand the session to get the charge URL**

Note: `checkout.session.completed` events do NOT include the Charge by default. You must either:
- (a) Expand during creation — `params.Expand = []*string{stripe.String("payment_intent.latest_charge")}` — but this only works if you retrieve the session in the handler, not rely on the webhook payload.
- (b) Retrieve the PaymentIntent from Stripe in the webhook handler after parsing the session.

Option (b) adds a round trip per checkout. Acceptable for a P2 finding. Implement:

```go
if paymentIntentID != "" {
	piExpanded, err := paymentintent.Get(paymentIntentID, &stripe.PaymentIntentParams{
		Expand: []*string{stripe.String("latest_charge")},
	})
	if err == nil && piExpanded.LatestCharge != nil && piExpanded.LatestCharge.ReceiptURL != "" {
		if _, err := tx.ExecContext(ctx,
			"UPDATE stripe_payments SET stripe_receipt_url = $1 WHERE stripe_checkout_session_id = $2",
			piExpanded.LatestCharge.ReceiptURL, session.ID); err != nil {
			s.logger.Warn("failed_to_persist_receipt_url", slog.Any("error", err))
		}
	}
}
```

- [ ] **Step 2: Commit.**

---

## Chunk 4: P3 Minor Findings

### Task 4.1: Pin Stripe API version [S-L1]

**File:** wherever `stripe.Key = ...` is set (currently `billing/service.go:50`)

- [ ] **Step 1: Set API version**

```go
func New(db *sql.DB, cfg *config.Service, stripeSecretKey, webhookSigningKey string, userRepo models.UserRepository) *Service {
	if stripeSecretKey != "" {
		stripe.Key = stripeSecretKey
		// Pin the API version. Upgrades are a deliberate code change, not a
		// side effect of Dashboard settings. Match the version listed in the
		// Stripe Dashboard → Developers → API version at the time of writing.
		stripe.APIVersion = "2025-07-30.basil" // UPDATE THIS TO MATCH stripe-go/v82 default
	}
	// ...
}
```

Check the `stripe-go/v82` release notes for the matching API version string. If `stripe-go/v82` hardcodes a version via its `APIVersion` constant, use that.

- [ ] **Step 2: Commit** — `git commit -am "chore(billing): pin Stripe API version (S-L1)"`

---

### Task 4.2: `DisallowUnknownFields` on checkout request [S-L2]

**File:** `web/handlers/billing.go:67-71`

- [ ] **Step 1: Tighten the decoder**

```go
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()
var req checkoutSessionRequest
if err := dec.Decode(&req); err != nil {
	renderJSON(w, http.StatusUnprocessableEntity, models.APIError{
		Code: http.StatusUnprocessableEntity, Message: "invalid payload: " + err.Error()})
	return
}
```

Repeat for `Reconcile`.

- [ ] **Step 2: Commit.**

---

### Task 4.3: Clean up `refund_cap_applied` metric [S-L3]

- [ ] **Step 1: Delete the old counter** after confirming no dashboards reference it, OR mark deprecated and keep emitting (always 0 after S-C4).
- [ ] **Step 2: Remove the Grafana alert rule** tied to `billing_refund_cap_applied_total`.
- [ ] **Step 3: Add a Grafana alert** on `billing_refund_deficit_applied_total > 0` with a runbook pointer to the deficit ops review queue.
- [ ] **Step 4: Commit.**

---

### Task 4.4: Document the webhook path in OpenAPI [S-L4]

**File:** `docs/api.md` or wherever the OpenAPI 3.1 spec lives

- [ ] **Step 1: Add a stub entry**

```yaml
/billing/webhook:
  post:
    operationId: stripeWebhook
    summary: Stripe webhook receiver
    description: |
      Internal endpoint for Stripe to deliver events. Not intended for direct
      client use. Requires a valid `Stripe-Signature` header. Returns 200 on
      success, 400 on invalid signature, 500 on transient processing error.
    tags: [internal]
    security: []  # Signature-based, not Bearer/Clerk
    responses:
      "200": {description: Event acknowledged}
      "400": {description: Invalid signature or malformed payload}
      "500": {description: Transient processing error; Stripe will retry}
```

And document that `/webhooks/stripe` and `/api/stripe/webhook` return 410 Gone.

- [ ] **Step 2: Commit.**

---

## §3 — Rollout Plan

### Order of operations

1. **Migration 000027** lands first (can ship in isolation — additive, safe).
2. **P0 code** lands in one PR or a tightly-ordered sequence: S-C1 → S-C2 → S-C3 → S-C4 → S-C5. Each as its own commit, all in the same merge train.
3. **Backfill job** runs on first deploy after S-C3: `BackfillStripeCustomers` creates a Stripe Customer for every existing user with `stripe_customer_id IS NULL`. Log the result; do not fail startup if a handful of users fail.
4. **Smoke test in staging**: run a real Stripe test-mode checkout → verify `stripe_payments.stripe_payment_intent_id` is populated → issue a partial refund from the Stripe Dashboard → verify `refund_deficit_credits` is written if balance was partially consumed → verify next purchase pays down deficit.
5. **P1 tasks** land as independent PRs.
6. **P2 tasks** land as needed; not launch-blocking.
7. **P3 tasks** land in a cleanup PR.

### Data migration checklist

After migration 000027:

- [ ] Run `BackfillStripeCustomers` — expect 100% success for active users (those who have logged in since Clerk was set up)
- [ ] Verify `SELECT COUNT(*) FROM users WHERE stripe_customer_id IS NULL` → should be 0 for active accounts
- [ ] Verify `SELECT COUNT(*) FROM users WHERE refund_deficit_credits > 0` → should be 0 at launch

### Rollback plan

If any P0 task causes regressions:

- Migration 000027 has a working `.down.sql`. Run it after reverting the code.
- The old `refund_cap_applied` logic is preserved in git history at commit `<pre-S-C4>` — cherry-pick it back temporarily if rollback is urgent.
- S-C3 creates Customer objects in Stripe that are *safe to leave orphaned* if rolled back; they don't incur cost.

---

## §4 — Testing Strategy

### Unit tests

- `billing/decimal_test.go` — `parseCreditsStrict`, exhaustive cases.
- `billing/refund_test.go` — `computeRefundSplit` table-tested; full `handleChargeRefunded` with a real test DB.
- `billing/customer_test.go` — `EnsureStripeCustomer` fast path + failure modes. Full creation path gated behind `-tags=integration`.

### Integration tests (test DB + stripe-mock)

Set up a local `stripe-mock` (`go install github.com/stripe/stripe-mock@latest`) and run it on `:12111`. Configure the test harness to point `stripe.Key` at a mock key and override the API backend URL. Then:

- [ ] Full end-to-end: create checkout session → simulate webhook → verify balance
- [ ] Deficit creation: short balance → refund → verify deficit row
- [ ] Deficit paydown: existing deficit → new purchase → verify paydown
- [ ] Double webhook: replay `checkout.session.completed` → balance unchanged
- [ ] Signature verification: tamper with payload → 400

### Staging verification (with real Stripe test mode)

Runbook at the end of this plan.

---

## §5 — Staging Verification Runbook

After deploying the P0 chunk to staging:

### Setup

1. Start a clean test user via the frontend.
2. Note the `users.id` and `users.stripe_customer_id` (should be populated after signup).

### Scenario A: Happy path

1. Frontend: purchase 100 credits via Stripe test card `4242 4242 4242 4242`.
2. Verify DB: `SELECT credit_balance, refund_deficit_credits FROM users WHERE id = $UID;` → `100, 0`.
3. Verify DB: `SELECT stripe_payment_intent_id, status FROM stripe_payments WHERE user_id = $UID;` → `pi_..., 'succeeded'`.
4. ✅ If both pass, S-C2 + S-C3 + S-C1 are working.

### Scenario B: Partial-consumption refund creates deficit

1. Consume 95 credits via a real job.
2. Verify balance: `5`.
3. Issue a full refund in the Stripe Dashboard for the charge.
4. Wait for the webhook (< 10 s).
5. Verify DB: `credit_balance = 0`, `refund_deficit_credits = 95`.
6. Verify ledger: `SELECT type, amount, description FROM credit_transactions WHERE user_id = $UID ORDER BY created_at;` → should show `purchase(+100)`, `consumption(-95)`, `refund(-5)`, `refund_deficit(0, desc mentions 95)`.
7. Verify metric: `billing_refund_deficit_applied_total` incremented by 1.
8. ✅ If all pass, S-C4 is working.

### Scenario C: New purchase pays down deficit

1. With the test user at `balance=0, deficit=95`, issue a new 100-credit purchase.
2. Verify DB after webhook: `credit_balance = 5`, `refund_deficit_credits = 0`.
3. Verify ledger: new rows for `purchase(+100, balance_after=5)` and `deficit_paydown(0, desc mentions 95)`.
4. ✅ If all pass, the deficit paydown is working.

### Scenario D: Webhook replay

1. Replay the `checkout.session.completed` event from the Stripe Dashboard.
2. Verify DB: balance unchanged.
3. ✅ If the balance is unchanged, the `processed_webhook_events` dedup is intact.

### Scenario E: Signature failure

1. Use `curl` to POST a fake webhook payload to `/api/v1/billing/webhook` with a bogus `Stripe-Signature` header.
2. Expect: 400 Bad Request.
3. Expect log: `invalid_webhook_signature`.
4. ✅ If both pass, signature verification is working.

---

## §6 — Out-of-Scope Items (Follow-up Work)

The following are related but explicitly out of this plan. File a follow-up ticket for each:

- **Stripe Customer Portal** — enable self-service refund requests, payment method management. Requires S-C3 as a prerequisite; otherwise P3.
- **Subscription mode** — recurring billing for enterprise tier. Would require a substantial new plan; none of the current code paths need to change.
- **Stripe Tax** — automatic tax calculation. Requires business tax registration work; not a code-side blocker.
- **Radar Rules** — fraud prevention for the checkout flow. Stripe-native, no code change needed to enable basic rules.
- **Multi-currency support** — currently USD-only by hard-check at `billing/service.go:71`. Requires rethinking the `credits_purchased` pricing (unit_amount is in smallest currency unit).
- **Refund request UI in the frontend** — frontend should have a "Request refund" button that, at S-C3+ time, calls an internal endpoint, checks deficit implications, and only calls `refund.New` if policy permits. This plan's deficit ledger *handles* any refund that lands; the UI work shapes *which* refunds are initiated.
- **Refund policy in ToS** — document the "credits are non-refundable once consumed; refund requests within 14 days of purchase that have not been consumed are fully refundable" policy. Legal work, not code work.

---

## §7 — Summary Checklist (for the executor)

### P0 Critical (release blockers)
- [ ] **S-C1** Task 1.1: Cap credits per checkout session (10,000)
- [ ] **S-C2** Task 1.2: Backfill `stripe_payment_intent_id` on `checkout.session.completed`
- [ ] **S-C3** Task 1.3: Create Stripe Customer at user provisioning + persist + pass to checkout
- [ ] **S-C4** Task 1.4: Refund deficit ledger (migration 000027 + refund handler rewrite + paydown logic)
- [ ] **S-C5** Task 1.5: Fail-fast on missing `STRIPE_WEBHOOK_SECRET` / `ENCRYPTION_KEY` in production

### P1 High (fix before launch)
- [ ] **S-H1** Task 2.1: Stripe `Idempotency-Key` on `checkoutsession.New` (and `customer.New` already in Task 1.3)
- [ ] **S-H2** Task 2.2: Decimal-safe refund math via `shopspring/decimal`
- [ ] **S-H3** Task 2.3: Strict credit parsing (`strconv.Atoi`, not `fmt.Sscan`) — covered by Task 1.1
- [ ] **S-H4** Task 2.4: `ClientReferenceID` + `PaymentIntentData.Metadata` on checkout session
- [ ] **S-H5** Task 2.5: `charge.dispute.created` handler

### P2 Medium (fix shortly after launch)
- [ ] **S-M1** Task 3.1: Drop `session.Metadata` fallback for credit amounts
- [ ] **S-M2** Task 3.2: Expand `line_items` + `payment_intent` in `ReconcileSession`
- [ ] **S-M3** Task 3.3: `refund.updated` handler (stub now, expand when enabling async PMs)
- [ ] **S-M4** Task 3.4: `checkout.session.async_payment_*` handlers (stub now)
- [ ] **S-M5** Task 3.5: Repair `handleChargeFailed` with PaymentIntent fallback
- [ ] **S-M6** Task 3.6: Persist `stripe_receipt_url`

### P3 Minor (code hygiene)
- [ ] **S-L1** Task 4.1: Pin Stripe API version
- [ ] **S-L2** Task 4.2: `DisallowUnknownFields` on checkout + reconcile handlers
- [ ] **S-L3** Task 4.3: Replace `refund_cap_applied` metric with `refund_deficit_applied`
- [ ] **S-L4** Task 4.4: Document webhook path in OpenAPI spec

### Verified-correct (do NOT touch)
- [x] Webhook signature verification (`webhook.ConstructEvent`)
- [x] Webhook idempotency (`processed_webhook_events` inside SERIALIZABLE tx)
- [x] 410 Gone on retired legacy webhook paths
- [x] `MaxBodySize(64 KiB)` on the webhook route — does not mutate raw body
- [x] `credit_balance >= 0` DB constraint (paired with deficit ledger in S-C4)
- [x] `balance_after = balance_before + amount` ledger integrity CHECK

---

**End of plan.**
