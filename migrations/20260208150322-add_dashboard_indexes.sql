
-- +migrate Up
CREATE INDEX idx_river_job_kind_created_at ON river_job(kind, created_at);

-- +migrate Down
DROP INDEX IF EXISTS idx_river_job_kind_created_at;
