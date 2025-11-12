-- Add soft delete support to jobs table
-- This allows us to "delete" jobs while preserving valuable results data

BEGIN;

-- Add deleted_at column to jobs table
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

-- Create index for efficient querying of non-deleted jobs
CREATE INDEX IF NOT EXISTS idx_jobs_deleted_at ON jobs(deleted_at) WHERE deleted_at IS NULL;

-- Create composite index for status queries on non-deleted jobs
CREATE INDEX IF NOT EXISTS idx_jobs_status_not_deleted ON jobs(status, created_at) WHERE deleted_at IS NULL;

-- Create index for user queries on non-deleted jobs
CREATE INDEX IF NOT EXISTS idx_jobs_user_not_deleted ON jobs(user_id, created_at) WHERE deleted_at IS NULL;

COMMIT;
