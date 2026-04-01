-- Revert: remove 'admin' from allowed sources.
-- Re-tag any admin-sourced jobs as 'web' so the CHECK constraint can be restored.
UPDATE jobs SET source = 'web' WHERE source = 'admin';

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_source_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_source_check CHECK (source IN ('web', 'api'));
