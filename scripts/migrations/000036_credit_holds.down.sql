-- Rollback for 000036_credit_holds.
--
-- Drops the constraints and the column. If there are in-flight jobs at
-- the moment of rollback their holds are simply forgotten — the balance
-- they reserved becomes available again, which is the safest direction.

BEGIN;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS credit_held_precise_non_negative;

ALTER TABLE users
    DROP COLUMN IF EXISTS credit_held_precise;

COMMIT;
