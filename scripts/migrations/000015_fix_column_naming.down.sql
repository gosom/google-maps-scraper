BEGIN;

-- Revert column naming fixes
-- Convert openhours back to TEXT
ALTER TABLE results ALTER COLUMN openhours TYPE TEXT USING 
    CASE 
        WHEN openhours IS NULL THEN ''
        ELSE openhours::text
    END;

-- Re-add the NOT NULL constraint
ALTER TABLE results ALTER COLUMN openhours SET NOT NULL;

-- Re-add the open_hours column that was dropped
ALTER TABLE results ADD COLUMN IF NOT EXISTS open_hours JSONB;

COMMIT;
