-- Webhook configurations: user-level webhook endpoints (decoupled from jobs).
-- Follows the api_keys pattern: soft-delete via revoked_at, never hard delete.
CREATE TABLE IF NOT EXISTS webhook_configs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  -- User-defined label ("My n8n workflow", "Slack notifier")
  name VARCHAR(100) NOT NULL,

  -- Target URL (HTTPS enforced at application layer)
  url TEXT NOT NULL,

  -- HMAC-SHA256 hash of the signing secret (plaintext shown once at creation)
  secret_hash TEXT NOT NULL,

  -- DNS-rebinding prevention: resolved IP pinned at validation time
  resolved_ip INET,

  -- NULL until the first successful test delivery
  verified_at TIMESTAMPTZ,

  -- Lifecycle
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_at TIMESTAMPTZ  -- Soft delete
);

-- Access patterns:
-- 1. List all configs for a user (settings page)
CREATE INDEX IF NOT EXISTS idx_webhook_configs_user_id ON webhook_configs(user_id);
-- 2. List only active configs for a user (job creation dropdown)
CREATE INDEX IF NOT EXISTS idx_webhook_configs_active ON webhook_configs(user_id) WHERE revoked_at IS NULL;

-- Junction table: which webhooks should fire for which jobs, plus delivery state.
-- Composite PK enforces one-delivery-per-webhook-per-job (BCNF).
CREATE TABLE IF NOT EXISTS job_webhook_deliveries (
  job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  webhook_config_id UUID NOT NULL REFERENCES webhook_configs(id) ON DELETE CASCADE,

  -- Delivery tracking
  attempts INT NOT NULL DEFAULT 0,
  last_attempt_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,  -- NULL until successful delivery
  status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending | delivering | delivered | failed

  PRIMARY KEY (job_id, webhook_config_id)
);

-- Access patterns:
-- 1. Find all pending deliveries for a job (completion handler)
CREATE INDEX IF NOT EXISTS idx_job_webhook_deliveries_job ON job_webhook_deliveries(job_id) WHERE delivered_at IS NULL;
-- 2. Find all deliveries for a webhook config (config detail page)
CREATE INDEX IF NOT EXISTS idx_job_webhook_deliveries_config ON job_webhook_deliveries(webhook_config_id);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE webhook_configs TO scraper;
GRANT ALL PRIVILEGES ON TABLE job_webhook_deliveries TO scraper;
