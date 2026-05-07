-- 000035_relax_processed_webhook_events_event_id_check.down.sql
-- Restore the Stripe-only CHECK constraint added in 000018. WARNING: this
-- will hold an ACCESS EXCLUSIVE lock while PostgreSQL scans the whole
-- processed_webhook_events table to validate every existing row. If any
-- non-evt_* rows are present (e.g., Clerk Svix msg_* rows inserted after
-- the up migration ran), this rollback will fail with a constraint violation;
-- delete or rewrite those rows before retrying.
BEGIN;

ALTER TABLE processed_webhook_events
    ADD CONSTRAINT chk_event_id_format
    CHECK (event_id ~ '^evt_[a-zA-Z0-9]+$');

COMMIT;
