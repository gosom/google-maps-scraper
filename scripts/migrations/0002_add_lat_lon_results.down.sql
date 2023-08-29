BEGIN;
    ALTER TABLE results DROP COLUMN latitude;
    ALTER TABLE results DROP COLUMN longitude;
COMMIT;
