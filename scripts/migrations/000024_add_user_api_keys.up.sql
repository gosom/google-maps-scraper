-- 000024: add user_api_keys table for programmatic API access
-- Keys are stored as SHA-256 hashes; the raw key is never persisted.

CREATE TABLE user_api_keys (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_hash   TEXT        NOT NULL UNIQUE,
    plan_tier  TEXT        NOT NULL DEFAULT 'free' CHECK (plan_tier IN ('free', 'paid')),
    name       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_user_api_keys_user_id ON user_api_keys(user_id);
