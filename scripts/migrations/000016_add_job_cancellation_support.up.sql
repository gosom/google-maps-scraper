-- Add job cancellation statuses to support job abortion and better lifecycle management
-- This migration adds new status values to support cancelling running jobs

-- Update the jobs table to support new status values
-- The status column already exists, we just need to ensure our application can use the new values
-- No schema change is needed since status is already a text field

-- Add an index to optimize queries filtering by status and deleted_at
CREATE INDEX IF NOT EXISTS idx_jobs_status_deleted_at ON jobs(status, deleted_at);

-- Add an index to optimize monitoring queries by updated_at
CREATE INDEX IF NOT EXISTS idx_jobs_updated_at ON jobs(updated_at) WHERE deleted_at IS NULL;

-- Update any existing 'pending' jobs that have been deleted to 'cancelled' status
UPDATE jobs 
SET status = 'cancelled', updated_at = NOW() 
WHERE status = 'pending' AND deleted_at IS NOT NULL;

-- Update any existing 'working' jobs that have been deleted to 'aborting' status  
UPDATE jobs 
SET status = 'aborting', updated_at = NOW() 
WHERE status = 'working' AND deleted_at IS NOT NULL;
