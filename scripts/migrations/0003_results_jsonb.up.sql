BEGIN;
    ALTER TABLE results DROP COLUMN title;
    ALTER TABLE results DROP COLUMN category;
    ALTER TABLE results DROP COLUMN address;
    ALTER TABLE results DROP COLUMN openhours;
    ALTER TABLE results DROP COLUMN website;
    ALTER TABLE results DROP COLUMN phone;
    ALTER TABLE results DROP COLUMN pluscode;
    ALTER TABLE results DROP COLUMN review_count;
    ALTER TABLE results DROP COLUMN rating;
    ALTER TABLE results DROP COLUMN latitude; 
    ALTER TABLE results DROP COLUMN longitude;

    ALTER TABLE results 
        ADD COLUMN data JSONB NOT NULL;

COMMIT;
