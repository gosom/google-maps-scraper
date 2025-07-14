-- Reverse the user_usage table enhancements
ALTER TABLE user_usage DROP COLUMN IF EXISTS total_jobs_run;
ALTER TABLE user_usage DROP COLUMN IF EXISTS total_locations_scraped;
ALTER TABLE user_usage DROP COLUMN IF EXISTS total_emails_extracted;
ALTER TABLE user_usage DROP COLUMN IF EXISTS current_month_jobs;
ALTER TABLE user_usage DROP COLUMN IF EXISTS current_month_locations;
ALTER TABLE user_usage DROP COLUMN IF EXISTS current_month_emails;
ALTER TABLE user_usage DROP COLUMN IF EXISTS last_reset_date;

-- Drop job_usage_details table
DROP TABLE IF EXISTS job_usage_details;

-- Drop indexes
DROP INDEX IF EXISTS idx_job_usage_user_id;
DROP INDEX IF EXISTS idx_job_usage_status;
DROP INDEX IF EXISTS idx_user_usage_reset_date;
