-- Rollback S3 file storage table

BEGIN;

-- Drop indexes first (good practice: indexes before tables)
DROP INDEX IF EXISTS idx_job_files_etag;
DROP INDEX IF EXISTS idx_job_files_type;
DROP INDEX IF EXISTS idx_job_files_status;
DROP INDEX IF EXISTS idx_job_files_s3_location;
DROP INDEX IF EXISTS idx_job_files_user_id;
DROP INDEX IF EXISTS idx_job_files_job_id;

-- Drop the table (CASCADE will handle foreign key constraints)
DROP TABLE IF EXISTS job_files CASCADE;

COMMIT;
