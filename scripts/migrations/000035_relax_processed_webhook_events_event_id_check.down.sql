-- 000035_relax_processed_webhook_events_event_id_check.down.sql
-- Restore the Stripe-only CHECK constraint. Note: this will fail if any
-- non-evt_* rows exist (e.g., Clerk msg_* rows from after the up migration).
BEGIN;

ALTER TABLE processed_webhook_events
    ADD CONSTRAINT chk_event_id_format
    CHECK (event_id ~ '^evt_[a-zA-Z0-9]+$');

COMMIT;
