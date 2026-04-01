-- Revert: remove 'admin' from allowed sources.
-- WARNING: This will fail if any jobs with source='admin' exist.
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_source_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_source_check CHECK (source IN ('web', 'api'));
