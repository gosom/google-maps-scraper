-- Job creation idempotency: store the response for each (user_id, key) so
-- network retries on POST /api/v1/jobs cannot double-bill a user.
--
-- Two-phase lifecycle (Stripe pattern):
--   1. status='started' is inserted BEFORE the handler runs, using the
--      UNIQUE (user_id, key) constraint as the atomic reservation lock.
--      Concurrent retries arriving at the same time hit the unique
--      constraint and bounce off — only one request runs the handler.
--   2. status='completed' replaces 'started' AFTER the handler returns
--      with the captured status_code and response_body. Subsequent
--      retries with the same key + body hash get the cached response.
--
-- A periodic cleanup job (web/middleware/idempotency.go cleanup goroutine)
-- removes:
--   - completed rows past expires_at (24 h TTL — Stripe default)
--   - started rows older than 15 min (crashed/abandoned requests, so a
--     panic doesn't permanently block its key)
-- The expires_at index supports the cleanup query.
--
-- user_id is TEXT, not UUID, to match the rest of the schema — Clerk
-- user IDs (`user_2abc...`) are not UUIDs.

CREATE TABLE IF NOT EXISTS idempotency_keys (
    id            UUID PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    method        TEXT NOT NULL,
    path          TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('started', 'completed')),
    status_code   INTEGER,
    response_body BYTEA,
    created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMP WITH TIME ZONE,
    expires_at    TIMESTAMP WITH TIME ZONE NOT NULL,

    UNIQUE (user_id, key)
);

-- Cleanup query support: WHERE expires_at < NOW() (completed) and
-- WHERE created_at < NOW() - INTERVAL '15 minutes' (started). The
-- expires_at index covers the common case; the cleanup of stuck
-- 'started' rows is rare enough to seq-scan.
CREATE INDEX IF NOT EXISTS idempotency_keys_expires_at_idx
    ON idempotency_keys (expires_at);

COMMENT ON TABLE idempotency_keys IS 'Stripe-style two-phase idempotency for POST /api/v1/jobs. See web/middleware/idempotency.go for the middleware that owns this table.';
