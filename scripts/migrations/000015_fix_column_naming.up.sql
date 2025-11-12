BEGIN;

-- Fix column naming conflicts between original schema and enhanced columns
-- The issue is that original table has 'openhours' but migration added 'open_hours'

-- Drop the conflicting column added in migration 000009 if it exists
ALTER TABLE results DROP COLUMN IF EXISTS open_hours;

-- Convert the existing openhours column to JSONB to match expected format
-- First check if the column contains data that can be converted
DO $$
BEGIN
    -- Try to convert existing openhours column to JSONB
    -- If it fails, we'll handle it gracefully
    BEGIN
        ALTER TABLE results ALTER COLUMN openhours TYPE JSONB USING 
            CASE 
                WHEN openhours = '' OR openhours IS NULL THEN NULL
                ELSE openhours::jsonb
            END;
    EXCEPTION
        WHEN others THEN
            -- If conversion fails, just set all values to NULL and change type
            UPDATE results SET openhours = NULL WHERE openhours IS NOT NULL;
            ALTER TABLE results ALTER COLUMN openhours TYPE JSONB USING NULL;
    END;
END $$;

-- Ensure the openhours column can accept NULL values for new JSONB format
ALTER TABLE results ALTER COLUMN openhours DROP NOT NULL;

COMMIT;
