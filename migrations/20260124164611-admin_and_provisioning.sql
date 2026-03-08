-- +migrate Up

CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_api_keys_key_hash ON api_keys(key_hash) WHERE revoked_at IS NULL;
CREATE INDEX idx_api_keys_user_id ON api_keys(user_id) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS app_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    encrypted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    ip_address TEXT,
    user_agent TEXT
);

CREATE INDEX idx_admin_sessions_expires ON admin_sessions(expires_at);

CREATE TABLE IF NOT EXISTS provisioned_resources (
    id SERIAL PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT 'digitalocean',
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    name TEXT NOT NULL,
    region TEXT,
    size TEXT,
    status TEXT NOT NULL DEFAULT 'provisioning',
    ip_address TEXT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(provider, resource_type, resource_id)
);

CREATE INDEX idx_provisioned_resources_status ON provisioned_resources(status) WHERE deleted_at IS NULL;

INSERT INTO app_config (key, value, encrypted) VALUES
    ('worker_concurrency', '8', false),
    ('worker_max_jobs_per_cycle', '100', false),
    ('worker_fast_mode', 'false', false)
ON CONFLICT (key) DO NOTHING;

-- +migrate Down

DROP TABLE IF EXISTS provisioned_resources;
DROP TABLE IF EXISTS admin_sessions;
DROP TABLE IF EXISTS app_config;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
