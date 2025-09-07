BEGIN;

-- 1. Remove triggers
DROP TRIGGER IF EXISTS update_stripe_payments_updated_at ON stripe_payments;
DROP TRIGGER IF EXISTS update_users_updated_at ON users;
DROP TRIGGER IF EXISTS update_system_config_updated_at ON system_config;
DROP TRIGGER IF EXISTS update_currency_pricing_updated_at ON currency_pricing;

-- 2. Drop function
DROP FUNCTION IF EXISTS update_updated_at_column();

-- 3. Remove job cost columns
ALTER TABLE jobs 
    DROP COLUMN IF EXISTS estimated_cost,
    DROP COLUMN IF EXISTS actual_cost,
    DROP COLUMN IF EXISTS cost_calculated_at,
    DROP COLUMN IF EXISTS refunded_credits;

-- 4. Drop tables (reverse order of dependencies is safest)
DROP TABLE IF EXISTS promotional_codes;
DROP TABLE IF EXISTS currency_pricing;
DROP TABLE IF EXISTS system_config;
DROP TABLE IF EXISTS stripe_payments;
DROP TABLE IF EXISTS credit_transactions;

-- 5. Remove user columns
ALTER TABLE users 
    DROP COLUMN IF EXISTS credit_balance,
    DROP COLUMN IF EXISTS total_credits_purchased,
    DROP COLUMN IF EXISTS total_credits_consumed,
    DROP COLUMN IF EXISTS stripe_customer_id,
    DROP COLUMN IF EXISTS default_currency,
    DROP COLUMN IF EXISTS country_code,
    DROP COLUMN IF EXISTS last_ip_address;

COMMIT;
