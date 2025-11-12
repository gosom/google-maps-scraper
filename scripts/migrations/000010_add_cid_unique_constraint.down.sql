BEGIN;

-- Remove the unique constraint on cid column
ALTER TABLE results DROP CONSTRAINT IF EXISTS unique_cid;

COMMIT;
