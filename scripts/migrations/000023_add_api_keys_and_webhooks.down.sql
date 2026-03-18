-- Rollback migration for API keys and webhooks

-- Drop constraint from jobs table
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS chk_webhook_secret;

-- Remove webhook columns from jobs table
ALTER TABLE jobs DROP COLUMN IF EXISTS webhook_last_attempt;
ALTER TABLE jobs DROP COLUMN IF EXISTS webhook_attempts;
ALTER TABLE jobs DROP COLUMN IF EXISTS webhook_verified_at;
ALTER TABLE jobs DROP COLUMN IF EXISTS webhook_resolved_ip;
ALTER TABLE jobs DROP COLUMN IF EXISTS webhook_secret;
ALTER TABLE jobs DROP COLUMN IF EXISTS webhook_url;

-- Drop api_key_usage_log table
DROP TABLE IF EXISTS api_key_usage_log CASCADE;

-- Drop api_keys table
DROP TABLE IF EXISTS api_keys CASCADE;
