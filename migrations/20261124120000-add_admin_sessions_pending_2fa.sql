-- +migrate Up
-- Old servers look up and issue untyped credentials through the id column.
-- Rename it so those operations fail closed throughout a rolling deployment.
ALTER TABLE admin_sessions RENAME COLUMN id TO session_token;
ALTER TABLE admin_sessions ADD COLUMN pending_2fa BOOLEAN NOT NULL DEFAULT FALSE;

-- Five minutes was the maximum lifetime of an unfinished login. Any legacy
-- credential issued in that window is ambiguous. Compare database timestamps
-- so this cutoff is independent of the application host's clock.
DELETE FROM admin_sessions WHERE created_at >= statement_timestamp() - INTERVAL '5 minutes';
ALTER TABLE admin_sessions ALTER COLUMN pending_2fa DROP DEFAULT;

-- +migrate Down
DELETE FROM admin_sessions WHERE pending_2fa;
ALTER TABLE admin_sessions DROP COLUMN pending_2fa;
ALTER TABLE admin_sessions RENAME COLUMN session_token TO id;
