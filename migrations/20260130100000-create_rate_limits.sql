-- +migrate Up
CREATE TABLE rate_limits (
    key TEXT PRIMARY KEY,
    counter INT NOT NULL DEFAULT 1,
    window_start TIMESTAMPTZ NOT NULL DEFAULT (NOW() AT TIME ZONE 'UTC')
);

CREATE INDEX idx_rate_limits_window_start ON rate_limits(window_start);

-- +migrate Down
DROP TABLE IF EXISTS rate_limits;
