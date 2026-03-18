-- Rollback migration for API keys

-- Drop api_key_usage_log table
DROP TABLE IF EXISTS api_key_usage_log CASCADE;

-- Drop api_keys table
DROP TABLE IF EXISTS api_keys CASCADE;
