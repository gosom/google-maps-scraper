-- Revert: remove refund_partial_cap from the stripe_payments status check constraint.
-- Note: any rows with status='refund_partial_cap' must be updated before running this rollback.

UPDATE stripe_payments SET status = 'refunded' WHERE status = 'refund_partial_cap';

ALTER TABLE stripe_payments DROP CONSTRAINT IF EXISTS stripe_payments_status_check;

ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
    CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled', 'refunded', 'partial_refund'));
