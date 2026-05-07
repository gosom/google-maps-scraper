-- 000035_relax_processed_webhook_events_event_id_check.up.sql
-- Drop the Stripe-specific CHECK constraint so Clerk's Svix msg_* IDs can be
-- inserted into the same dedupe table. The constraint added no real safety
-- (the column is text either way); the cost is zero — we gain table reuse
-- across providers.
BEGIN;

ALTER TABLE processed_webhook_events
    DROP CONSTRAINT IF EXISTS chk_event_id_format;

-- Update the column and table comments set in 000018 — the table is no
-- longer Stripe-only; Clerk webhook events will live alongside Stripe ones.
COMMENT ON TABLE processed_webhook_events
    IS 'Tracks processed webhook events for idempotency (Stripe and Clerk).';

COMMENT ON COLUMN processed_webhook_events.event_id
    IS 'Webhook provider event ID (e.g., Stripe evt_*, Clerk Svix msg_*).';

COMMIT;
