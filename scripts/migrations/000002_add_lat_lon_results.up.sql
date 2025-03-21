BEGIN;
    -- Add latitude and longitude columns to results table
    ALTER TABLE results ADD COLUMN IF NOT EXISTS latitude NUMERIC;
    ALTER TABLE results ADD COLUMN IF NOT EXISTS longitude NUMERIC;
COMMIT;