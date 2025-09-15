BEGIN;

-- Revert grants first (optional)
REVOKE ALL PRIVILEGES ON TABLE job_cost_breakdown FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE job_filters FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE billing_events FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE pricing_rules FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE billing_event_types FROM scraper;

-- Remove integration columns
ALTER TABLE credit_transactions DROP COLUMN IF EXISTS billing_event_id;

-- Drop triggers and functions
DROP TRIGGER IF EXISTS trg_billing_events_after_insert ON billing_events;
DROP FUNCTION IF EXISTS fn_billing_events_after_insert();

DROP TRIGGER IF EXISTS trg_billing_events_before_insert ON billing_events;
DROP FUNCTION IF EXISTS fn_billing_events_before_insert();

DROP TRIGGER IF EXISTS trg_billing_events_block_update ON billing_events;
DROP TRIGGER IF EXISTS trg_billing_events_block_delete ON billing_events;
DROP FUNCTION IF EXISTS fn_billing_events_block_mod();

-- Drop supporting and read models
DROP TABLE IF EXISTS job_cost_breakdown;
DROP TABLE IF EXISTS job_filters;

-- Drop main events table
DROP TABLE IF EXISTS billing_events;

-- Drop pricing and event types
ALTER TABLE pricing_rules DROP CONSTRAINT IF EXISTS pricing_rules_no_overlap;
DROP INDEX IF EXISTS uq_pricing_active_one;
DROP INDEX IF EXISTS idx_pricing_rules_range;
DROP INDEX IF EXISTS idx_pricing_active_lookup;
DROP TABLE IF EXISTS pricing_rules;
DROP TABLE IF EXISTS billing_event_types;

-- Remove added job columns
ALTER TABLE jobs DROP COLUMN IF EXISTS estimated_cost_precise;
ALTER TABLE jobs DROP COLUMN IF EXISTS actual_cost_precise;
ALTER TABLE jobs DROP COLUMN IF EXISTS billing_status;

COMMIT;
