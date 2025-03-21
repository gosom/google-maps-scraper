BEGIN;
    -- Remove latitude and longitude columns from results table
    ALTER TABLE results DROP COLUMN IF EXISTS latitude;
    ALTER TABLE results DROP COLUMN IF EXISTS longitude;
COMMIT;