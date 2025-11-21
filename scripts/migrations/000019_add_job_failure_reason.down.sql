-- Remove failure_reason column from jobs table
DROP INDEX IF EXISTS idx_jobs_failure_reason;
ALTER TABLE jobs DROP COLUMN IF EXISTS failure_reason;







