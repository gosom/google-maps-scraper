-- Rollback soft delete support from jobs table

BEGIN;

-- Drop the indexes first
DROP INDEX IF EXISTS idx_jobs_user_not_deleted;
DROP INDEX IF EXISTS idx_jobs_status_not_deleted;
DROP INDEX IF EXISTS idx_jobs_deleted_at;

-- Remove the deleted_at column
ALTER TABLE jobs DROP COLUMN IF EXISTS deleted_at;

COMMIT;
