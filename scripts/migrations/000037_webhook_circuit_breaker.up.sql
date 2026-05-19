-- 000037_webhook_circuit_breaker.up.sql
--
-- Adds a circuit-breaker / health-state model to webhook_configs so the
-- outbound delivery loop can auto-disable receivers that have been failing
-- consecutively. Without this, a customer's chronically-broken endpoint
-- would keep receiving (and 500ing on) every job-completion delivery
-- forever — wasting our outbound budget and continuing to spam their
-- on-call queue.
--
-- Threshold: 10 consecutive failed deliveries auto-disables. Matches the
-- common reference implementation (InvokeBot's webhook-reliability-patterns
-- guide). Each delivery has its own internal 5-retry budget with
-- exponential backoff (cap 1h, see web/services/webhook_delivery.go), so
-- this counter only increments after a delivery has truly given up.
-- Re-enable is user-driven via PATCH /webhooks/{id} (next migration phase).
--
-- The existing user-driven `revoked_at` soft-delete is preserved as a
-- distinct lifecycle channel: revoked_at means "the user deleted this
-- webhook", health_state='disabled' means "we stopped delivering because
-- the endpoint was broken". Both gate delivery; only the user can clear
-- revoked_at, only the user (or a future auto-probe) can clear disabled.

ALTER TABLE webhook_configs
	ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0
		CHECK (consecutive_failures >= 0),
	ADD COLUMN health_state TEXT NOT NULL DEFAULT 'healthy'
		CHECK (health_state IN ('healthy', 'degraded', 'disabled')),
	ADD COLUMN disabled_at TIMESTAMPTZ NULL,
	ADD COLUMN disabled_reason TEXT NULL;

-- Defensive: if health_state is 'disabled', disabled_at MUST be populated.
-- Catches a future bug where someone sets the state without recording the
-- moment. Conversely, disabled_at must be NULL when not disabled.
ALTER TABLE webhook_configs
	ADD CONSTRAINT chk_webhook_configs_disabled_at_consistency
	CHECK (
		(health_state = 'disabled' AND disabled_at IS NOT NULL) OR
		(health_state <> 'disabled' AND disabled_at IS NULL)
	);

-- The active-config index now also has to exclude disabled rows. Drop and
-- recreate so the index predicate matches the new "active" definition;
-- the index name stays stable so callers don't need to update query hints.
DROP INDEX IF EXISTS idx_webhook_configs_active;
CREATE INDEX idx_webhook_configs_active
	ON webhook_configs (user_id)
	WHERE revoked_at IS NULL AND health_state <> 'disabled';

COMMENT ON COLUMN webhook_configs.consecutive_failures IS
	'Running counter of non-2xx delivery attempts since the last 2xx. Reset to 0 on any success. When reaches AutoDisableThreshold (10), health_state transitions to disabled.';
COMMENT ON COLUMN webhook_configs.health_state IS
	'healthy = delivering normally; degraded = >=5 consecutive failures (reserved for future banner UX, no behaviour change); disabled = auto-disabled after 10 consecutive failures, user must re-enable.';
COMMENT ON COLUMN webhook_configs.disabled_at IS
	'When the circuit breaker tripped. NULL when health_state != disabled.';
COMMENT ON COLUMN webhook_configs.disabled_reason IS
	'Short machine-readable reason for the disable (e.g. "10_consecutive_failures"). NULL when not disabled.';
