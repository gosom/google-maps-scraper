-- Migration: Remove stripe webhook event idempotency tracking
-- Purpose: Rollback stripe webhook event tracking tables and related objects
-- Author: Yasseen
-- Date: 16.09.2025

BEGIN;

DROP VIEW IF EXISTS v_webhook_processing_stats;

DROP FUNCTION IF EXISTS fn_cleanup_old_webhook_events();

-- Drop indexes (will be automatically dropped with table, but being explicit)
DROP INDEX IF EXISTS idx_processed_webhook_events_recent;
DROP INDEX IF EXISTS idx_processed_webhook_events_result;
DROP INDEX IF EXISTS idx_processed_webhook_events_event_type;
DROP INDEX IF EXISTS idx_processed_webhook_events_processed_at;

DROP TABLE IF EXISTS processed_webhook_events;

COMMIT;