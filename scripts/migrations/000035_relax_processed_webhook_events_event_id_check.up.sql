-- 000035_relax_processed_webhook_events_event_id_check.up.sql
-- Drop the Stripe-specific CHECK constraint so Clerk's Svix msg_* IDs can be
-- inserted into the same dedupe table. The constraint added no real safety
-- (the column is text either way); the trade is zero — we gain table reuse
-- across providers.
BEGIN;

ALTER TABLE processed_webhook_events
    DROP CONSTRAINT IF EXISTS chk_event_id_format;

COMMIT;
