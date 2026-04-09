BEGIN;

-- Restore stripe_payments.status to the post-000031 state (without 'disputed').
ALTER TABLE stripe_payments
    DROP CONSTRAINT stripe_payments_status_check;
ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled',
                          'refunded', 'partial_refund', 'refund_partial_cap',
                          'refund_deficit_applied'));

COMMIT;
