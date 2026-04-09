BEGIN;

DROP INDEX IF EXISTS idx_users_refund_deficit;

-- Restore stripe_payments.status constraint to the post-000026 state
-- (with refund_partial_cap but without refund_deficit_applied).
ALTER TABLE stripe_payments
    DROP CONSTRAINT stripe_payments_status_check;
ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled',
                          'refunded', 'partial_refund', 'refund_partial_cap'));

ALTER TABLE credit_transactions
    DROP CONSTRAINT credit_transactions_type_check;
ALTER TABLE credit_transactions
    ADD CONSTRAINT credit_transactions_type_check
        CHECK (type IN ('purchase', 'consumption', 'bonus', 'refund', 'adjustment'));

ALTER TABLE users
    DROP COLUMN refund_deficit_credits;

COMMIT;
