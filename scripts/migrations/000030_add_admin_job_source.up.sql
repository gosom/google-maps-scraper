-- Allow admin-sourced jobs (created by admin users, bypassing credit system).
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_source_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_source_check CHECK (source IN ('web', 'api', 'admin'));
