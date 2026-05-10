-- 000036: Credit holds (estimate-as-quote reservation)
--
-- Why: previously the only protection against a user submitting two jobs
-- whose individual estimates each fit but combined exceed their balance
-- was a SELECT … FOR UPDATE on users in ConcurrentLimitService. That
-- correctly serialised the BALANCE READS but did NOT reserve credits —
-- both jobs would pass the check before either had charged anything,
-- then the second's end-of-job ChargeAllJobEvents would race the first's
-- and one would be silently underbilled or fail mid-charge while
-- results were already in the DB (free-results adversarial vector,
-- see architectural review 2026-05-10).
--
-- The fix introduces a hold (reservation) column on users:
--
--   available = credit_balance - credit_held_precise
--
-- Submitting a job atomically increments credit_held_precise by the
-- estimate. The hold is released at end-of-job (success OR failure)
-- regardless of the actual charged amount; the actual charge still
-- decrements credit_balance via the existing ChargeJobStart /
-- ChargeAllJobEvents path.
--
-- The companion handler logic uses the persisted estimated_cost_precise
-- column (already in 000017) to remember how much to release.
--
-- Backfill: existing rows get DEFAULT 0. Jobs in flight at migration
-- time were NOT retroactively held (they pre-date this contract); they
-- will release 0 at end and are unaffected.
--
-- ─── Constraint design ─────────────────────────────────────────────
--
-- Only `credit_held_precise >= 0` is enforced at the DB level. We
-- DELIBERATELY do NOT add `credit_held_precise <= credit_balance`
-- as a row-level CHECK, even though "held cannot exceed balance" is
-- the intuitive invariant.
--
-- Why: Postgres re-evaluates every row-level CHECK on every UPDATE
-- of any column in the row, regardless of which columns changed. The
-- end-of-job charge in ChargeAllJobEvents (billing/service.go) does
--
--     UPDATE users SET credit_balance = credit_balance - $1
--      WHERE id = $2 AND credit_balance >= $1
--
-- which would fire the (held <= balance) CHECK. With a hold of 3.0
-- and balance 10.0, a charge of 8.0 would drop balance to 2.0 BEFORE
-- the deferred releaseHoldAndLogBilling runs (it fires after status
-- persists at end of scrapeJob). At that intermediate state held=3.0
-- > balance=2.0 — the CHECK would fail, the entire charge transaction
-- rolls back, and the job lands in "failed" with results already in
-- the DB but the user uncharged. That is precisely the free-results
-- vector this migration is meant to close, just expressed differently.
--
-- The application-side gate (concurrent_limit.go: available >= cost
-- under FOR UPDATE) is the authoritative invariant. The DB-level
-- non-negative check is a low-cost trip-wire for "release more than
-- was held" bugs and stays.
--
-- ─── Lock-time mitigation ──────────────────────────────────────────
--
-- ALTER TABLE … ADD CONSTRAINT … CHECK without NOT VALID takes an
-- AccessExclusiveLock on `users` for the entire validation scan. On
-- a populated production users table this blocks every read AND
-- write to the table for the duration of the scan, including login
-- and dashboard hits. We split the constraint add into two steps:
--
--   1. ADD CONSTRAINT … NOT VALID  — takes AccessExclusiveLock
--      briefly (catalog mutation only, no row scan) and starts
--      enforcing the constraint for FUTURE writes immediately.
--   2. VALIDATE CONSTRAINT          — takes ShareUpdateExclusiveLock
--      (does not block reads or writes), scans existing rows.
--
-- For our trivial constraint (column default is 0, so every existing
-- row trivially satisfies it) the validation is fast, but the lock
-- pattern matters under load and should be the default for any
-- prod-touching CHECK. Postgres ≥ 11 — confirmed in our deploy
-- target.

BEGIN;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS credit_held_precise NUMERIC(18,6)
        NOT NULL DEFAULT 0;

-- See "Constraint design" comment above: only the non-negative
-- invariant lives at the DB level; the (held <= balance) invariant
-- is enforced at the application gate, NOT here.
ALTER TABLE users
    ADD CONSTRAINT credit_held_precise_non_negative
        CHECK (credit_held_precise >= 0) NOT VALID;

ALTER TABLE users
    VALIDATE CONSTRAINT credit_held_precise_non_negative;

COMMENT ON COLUMN users.credit_held_precise IS
    'Reserved credits for in-flight jobs. Available balance = credit_balance - credit_held_precise. Incremented by estimated_cost_precise at job creation, decremented by the same amount at job end (success or failure). The actual charge happens against credit_balance via billing_events.';

COMMIT;
