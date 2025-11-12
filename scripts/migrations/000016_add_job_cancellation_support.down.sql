-- Rollback job cancellation support
-- This migration removes the job cancellation status support

-- Drop the indexes we created
DROP INDEX IF EXISTS idx_jobs_status_deleted_at;
DROP INDEX IF EXISTS idx_jobs_updated_at;

-- Revert cancelled jobs back to pending (if they're still in the system)
UPDATE jobs 
SET status = 'pending', updated_at = NOW() 
WHERE status = 'cancelled';

-- Revert aborting jobs back to working (if they're still in the system)
UPDATE jobs 
SET status = 'working', updated_at = NOW() 
WHERE status = 'aborting';
