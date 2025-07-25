BEGIN;

-- Remove the per-job unique constraint
DROP INDEX IF EXISTS idx_results_unique_per_job;

-- Restore the global unique constraint on cid
-- First remove duplicates, keeping the earliest entry
DELETE FROM results 
WHERE id NOT IN (
    SELECT MIN(id) 
    FROM results 
    WHERE cid IS NOT NULL AND cid != ''
    GROUP BY cid
) AND cid IS NOT NULL AND cid != '';

-- Add back the global unique constraint
ALTER TABLE results ADD CONSTRAINT unique_cid UNIQUE (cid);

COMMIT;
