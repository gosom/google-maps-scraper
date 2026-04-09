BEGIN;

-- Add the 'disputed' status to stripe_payments.status so the
-- charge.dispute.created webhook handler (S-H5) can flag affected payments
-- for ops review. Disputes are distinct from refunds — Stripe pulls funds
-- AND a dispute fee, and gives us a response deadline (evidence_details.due_by)
-- typically 7-21 days depending on the network and reason code.
--
-- Preserves all prior statuses including the legacy refund_partial_cap
-- (deprecated by 000031 but still allowed for in-flight rows) and the new
-- refund_deficit_applied from 000031.
ALTER TABLE stripe_payments
    DROP CONSTRAINT stripe_payments_status_check;
ALTER TABLE stripe_payments
    ADD CONSTRAINT stripe_payments_status_check
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled',
                          'refunded', 'partial_refund', 'refund_partial_cap',
                          'refund_deficit_applied', 'disputed'));

COMMIT;
