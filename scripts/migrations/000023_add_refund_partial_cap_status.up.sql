-- Add refund_partial_cap status to stripe_payments.
-- This status is set when a charge.refunded event fires but the credit deduction
-- is capped at the user's remaining balance (they had already consumed credits).
-- Payments with this status are flagged for manual ops review.

ALTER TABLE stripe_payments DROP CONSTRAINT IF EXISTS stripe_payments_status_check;

ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
    CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled', 'refunded', 'partial_refund', 'refund_partial_cap'));
