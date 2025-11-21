-- Add failure_reason column to jobs table for tracking why jobs failed
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS failure_reason TEXT;

-- Create index for querying failed jobs by reason
CREATE INDEX IF NOT EXISTS idx_jobs_failure_reason ON jobs(failure_reason) WHERE failure_reason IS NOT NULL;







