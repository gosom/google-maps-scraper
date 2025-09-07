-- Remove foreign key constraint from jobs table
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_user_id_fkey;

-- Remove user_id column from jobs table
ALTER TABLE jobs DROP COLUMN IF EXISTS user_id;

-- Drop indexes
DROP INDEX IF EXISTS idx_jobs_user_id;

-- Drop tables
DROP TABLE IF EXISTS users;