BEGIN;

-- Add unique constraint on cid column to prevent duplicate entries
-- First, let's remove any existing duplicates by keeping only the first occurrence
DELETE FROM results 
WHERE id NOT IN (
    SELECT MIN(id) 
    FROM results 
    WHERE cid IS NOT NULL AND cid != ''
    GROUP BY cid
) AND cid IS NOT NULL AND cid != '';

-- Now add the unique constraint
ALTER TABLE results ADD CONSTRAINT unique_cid UNIQUE (cid);

COMMIT;
