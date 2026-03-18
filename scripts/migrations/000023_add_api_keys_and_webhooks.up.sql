-- Create api_keys table for API key management
CREATE TABLE IF NOT EXISTS api_keys (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  -- User-defined label
  name VARCHAR(100) NOT NULL,  -- "Production", "n8n workflow", "Dev"

  -- Dual hash system (fast lookup + slow verification)
  lookup_hash TEXT NOT NULL UNIQUE,      -- HMAC-SHA256(server_secret, full_key)
  key_hash TEXT NOT NULL,                -- Argon2id(full_key, key_salt)
  key_salt BYTEA NOT NULL,               -- Unique random salt per key
  hash_algorithm VARCHAR(20) NOT NULL DEFAULT 'argon2id',

  -- Display hints (minimal entropy exposure: <35%)
  key_hint_prefix VARCHAR(8) NOT NULL,   -- "bscraper_Ab12" (prefix + 2-3 random)
  key_hint_suffix VARCHAR(4) NOT NULL,   -- "9012" (last 4 chars)

  -- Usage tracking (never log the key itself)
  last_used_at TIMESTAMPTZ,
  last_used_ip INET,
  usage_count BIGINT DEFAULT 0,

  -- Lifecycle (soft delete, preserve audit trail)
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_at TIMESTAMPTZ,  -- Soft delete, never hard delete

  -- Future: scoped permissions (v2+)
  scopes JSONB DEFAULT '["full_access"]'
);

-- Create indexes for api_keys
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_lookup ON api_keys(lookup_hash) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_active ON api_keys(user_id) WHERE revoked_at IS NULL;

-- Create api_key_usage_log table for audit trail
CREATE TABLE IF NOT EXISTS api_key_usage_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
  used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ip_address INET NOT NULL,
  endpoint TEXT NOT NULL,          -- Which endpoint was called
  user_agent TEXT,

  -- Optional: geo data from IP (future enhancement)
  country_code VARCHAR(2),
  city VARCHAR(100),

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create indexes for api_key_usage_log
CREATE INDEX IF NOT EXISTS idx_api_key_usage_log_key_id ON api_key_usage_log(api_key_id, used_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_key_usage_log_ip ON api_key_usage_log(ip_address);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE api_keys TO scraper;
GRANT ALL PRIVILEGES ON TABLE api_key_usage_log TO scraper;
