-- 000037_webhook_circuit_breaker.down.sql
--
-- Reverse of 000037_webhook_circuit_breaker.up.sql. Drops the
-- circuit-breaker columns and restores the original active-config index
-- predicate.

DROP INDEX IF EXISTS idx_webhook_configs_active;

ALTER TABLE webhook_configs
	DROP CONSTRAINT IF EXISTS chk_webhook_configs_disabled_at_consistency;

ALTER TABLE webhook_configs
	DROP COLUMN IF EXISTS disabled_reason,
	DROP COLUMN IF EXISTS disabled_at,
	DROP COLUMN IF EXISTS health_state,
	DROP COLUMN IF EXISTS consecutive_failures;

-- Restore the original active-config index predicate (only revoked_at gates
-- "active"; before this migration, disabled didn't exist as a concept).
CREATE INDEX IF NOT EXISTS idx_webhook_configs_active
	ON webhook_configs (user_id)
	WHERE revoked_at IS NULL;
