BEGIN;

-- Remove the global unique constraint on cid
-- This allows the same business to appear in multiple jobs
ALTER TABLE results DROP CONSTRAINT IF EXISTS unique_cid;

-- Add a composite unique constraint to prevent true duplicates within the same job
-- This allows the same business to be scraped in different jobs but prevents 
-- duplicates within a single job
CREATE UNIQUE INDEX IF NOT EXISTS idx_results_unique_per_job 
ON results(cid, job_id);

COMMIT;
