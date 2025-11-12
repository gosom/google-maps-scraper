BEGIN;

-- 1. Drop triggers first (reverse of step 10 and 8)
DROP TRIGGER IF EXISTS billing_event_consume_credits ON billing_events;
DROP TRIGGER IF EXISTS update_stripe_payments_updated_at ON stripe_payments;
DROP TRIGGER IF EXISTS update_users_updated_at ON users;

-- 2. Drop functions (reverse of step 9 and 7)
DROP FUNCTION IF EXISTS consume_credits_from_billing_event();
DROP FUNCTION IF EXISTS update_updated_at_column();

-- 3. Drop indexes (reverse of step 6)
DROP INDEX IF EXISTS idx_credit_transactions_user_id;
DROP INDEX IF EXISTS idx_credit_transactions_reference;
DROP INDEX IF EXISTS idx_credit_transactions_billing_event;
DROP INDEX IF EXISTS idx_stripe_payments_user_id;
DROP INDEX IF EXISTS idx_stripe_payments_status;

-- 4. Remove columns from jobs table (reverse of step 5)
ALTER TABLE jobs 
    DROP COLUMN IF EXISTS refunded_credits,
    DROP COLUMN IF EXISTS cost_calculated_at;

-- 5. Drop tables (reverse of step 4 and 3)
DROP TABLE IF EXISTS stripe_payments;
DROP TABLE IF EXISTS credit_transactions;

-- 6. Remove columns from users table (reverse of step 2)
ALTER TABLE users 
    DROP COLUMN IF EXISTS credit_balance,
    DROP COLUMN IF EXISTS total_credits_purchased,
    DROP COLUMN IF EXISTS total_credits_consumed,
    DROP COLUMN IF EXISTS stripe_customer_id,
    DROP COLUMN IF EXISTS default_currency,
    DROP COLUMN IF EXISTS country_code,
    DROP COLUMN IF EXISTS last_ip_address;

-- 7. Restore original foreign key constraints without CASCADE (reverse of step 1)
-- Note: We're restoring them to their original state (without CASCADE)
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_user_id_fkey;
ALTER TABLE jobs ADD CONSTRAINT jobs_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id);

ALTER TABLE results DROP CONSTRAINT IF EXISTS results_user_id_fkey;
ALTER TABLE results ADD CONSTRAINT results_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id);

ALTER TABLE results DROP CONSTRAINT IF EXISTS results_job_id_fkey;
ALTER TABLE results ADD CONSTRAIaNT results_job_id_fkey 
    FOREIGN KEY (job_id) REFERENCES jobs(id);

-- 8. Remove NOT NULL constraints if they weren't there originally
-- (This assumes these columns were nullable before - adjust if needed)
-- ALTER TABLE jobs ALTER COLUMN user_id DROP NOT NULL;
-- ALTER TABLE results ALTER COLUMN user_id DROP NOT NULL;
-- ALTER TABLE results ALTER COLUMN job_id DROP NOT NULL;

COMMIT;