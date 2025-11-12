-- Migration: Add stripe webhook event idempotency tracking 
-- Purpose: Prevent duplicate processing of Stripe webhook events
-- Author: Yasseen
-- Date: 16.09.2025

BEGIN;

-- Create table for tracking processed webhook events
CREATE TABLE IF NOT EXISTS processed_webhook_events (
    event_id VARCHAR(255) PRIMARY KEY,
    event_type VARCHAR(100) NOT NULL,
    processed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    processing_result VARCHAR(20) NOT NULL DEFAULT 'success' CHECK (processing_result IN ('success', 'failed', 'skipped')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Add comments for documentation
COMMENT ON TABLE processed_webhook_events IS 'Tracks processed Stripe webhook events for idempotency';
COMMENT ON COLUMN processed_webhook_events.event_id IS 'Stripe event ID (e.g., evt_1234567890)';
COMMENT ON COLUMN processed_webhook_events.event_type IS 'Stripe event type (e.g., checkout.session.completed)';
COMMENT ON COLUMN processed_webhook_events.processed_at IS 'Timestamp when the event was processed';
COMMENT ON COLUMN processed_webhook_events.processing_result IS 'Result of processing: success, failed, or skipped';
COMMENT ON COLUMN processed_webhook_events.metadata IS 'Additional event metadata for debugging';

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_processed_webhook_events_processed_at 
    ON processed_webhook_events(processed_at DESC);

CREATE INDEX IF NOT EXISTS idx_processed_webhook_events_event_type 
    ON processed_webhook_events(event_type);

CREATE INDEX IF NOT EXISTS idx_processed_webhook_events_result 
    ON processed_webhook_events(processing_result)
    WHERE processing_result = 'failed';

-- Create an index for recent events lookup (without WHERE clause due to NOW() not being immutable)
CREATE INDEX IF NOT EXISTS idx_processed_webhook_events_recent 
    ON processed_webhook_events(event_id, processed_at DESC);

-- Add a cleanup function to remove old processed events (optional)
CREATE OR REPLACE FUNCTION fn_cleanup_old_webhook_events() 
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    -- Keep only last 90 days of webhook events for audit trail
    DELETE FROM processed_webhook_events 
    WHERE processed_at < (NOW() - INTERVAL '90 days')
    AND processing_result = 'success';
    
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION fn_cleanup_old_webhook_events() IS 'Removes successfully processed webhook events older than 90 days';

-- Add constraint to ensure event_id format is valid (optional but recommended)
ALTER TABLE processed_webhook_events 
    ADD CONSTRAINT chk_event_id_format 
    CHECK (event_id ~ '^evt_[a-zA-Z0-9]+$');

-- Create a view for monitoring webhook processing (useful for debugging)
CREATE OR REPLACE VIEW v_webhook_processing_stats AS
SELECT 
    event_type,
    processing_result,
    DATE_TRUNC('hour', processed_at) as hour,
    COUNT(*) as event_count,
    MIN(processed_at) as first_event,
    MAX(processed_at) as last_event
FROM processed_webhook_events
WHERE processed_at > (NOW() - INTERVAL '7 days')
GROUP BY event_type, processing_result, DATE_TRUNC('hour', processed_at)
ORDER BY hour DESC, event_type;

COMMENT ON VIEW v_webhook_processing_stats IS 'Webhook processing statistics for the last 7 days';

-- Grant permissions to scraper user (consistent with other migrations)
GRANT ALL PRIVILEGES ON processed_webhook_events TO scraper;
GRANT SELECT ON v_webhook_processing_stats TO scraper;
GRANT EXECUTE ON FUNCTION fn_cleanup_old_webhook_events() TO scraper;

COMMIT;