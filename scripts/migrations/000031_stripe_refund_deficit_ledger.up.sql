BEGIN;

-- 1. Add refund deficit column: tracks uncollectable refund amounts when
-- credits have been consumed. Next purchase applies here first, then to balance.
-- The >= 0 CHECK preserves the financial safety invariant — deficit can only
-- grow (via refunds) and shrink (via paydowns), never go negative.
ALTER TABLE users
    ADD COLUMN refund_deficit_credits NUMERIC(18,6) NOT NULL DEFAULT 0
        CHECK (refund_deficit_credits >= 0);

COMMENT ON COLUMN users.refund_deficit_credits IS
    'Uncollectable refund amount in credits. Set when a Stripe refund exceeds current balance because credits have already been consumed. Next purchase applies to deficit first.';

-- 2. Expand credit_transactions.type to allow refund_deficit + deficit_paydown
-- entries so the audit ledger has dedicated row types for these events.
ALTER TABLE credit_transactions
    DROP CONSTRAINT credit_transactions_type_check;
ALTER TABLE credit_transactions
    ADD CONSTRAINT credit_transactions_type_check
        CHECK (type IN ('purchase', 'consumption', 'bonus', 'refund', 'refund_deficit', 'deficit_paydown', 'adjustment'));

-- 3. Expand stripe_payments.status so the ops dashboard can surface
-- deficit-applied payments without joining credit_transactions.
-- Preserves the refund_partial_cap status added in 000026 for backwards
-- compatibility with any in-flight rows, and adds refund_deficit_applied
-- for the new refund deficit ledger pipeline introduced by S-C4.
ALTER TABLE stripe_payments
    DROP CONSTRAINT stripe_payments_status_check;
ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled',
                          'refunded', 'partial_refund', 'refund_partial_cap', 'refund_deficit_applied'));

-- 4. Index for the ops dashboard query "users who owe us credits"
CREATE INDEX idx_users_refund_deficit ON users(refund_deficit_credits)
    WHERE refund_deficit_credits > 0;

COMMIT;
