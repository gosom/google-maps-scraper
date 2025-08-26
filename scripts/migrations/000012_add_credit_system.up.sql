BEGIN;

-- 1. Fix existing schema issues first
ALTER TABLE jobs ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE results ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE results ALTER COLUMN job_id SET NOT NULL;

-- Add ON DELETE CASCADE to existing foreign keys
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_user_id_fkey;
ALTER TABLE jobs ADD CONSTRAINT jobs_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE results DROP CONSTRAINT IF EXISTS results_user_id_fkey;
ALTER TABLE results ADD CONSTRAINT results_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE results DROP CONSTRAINT IF EXISTS results_job_id_fkey;
ALTER TABLE results ADD CONSTRAINT results_job_id_fkey 
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE;

-- 2. Add credit and currency fields to users table
ALTER TABLE users 
    ADD COLUMN IF NOT EXISTS credit_balance INTEGER NOT NULL DEFAULT 0 CHECK (credit_balance >= 0),
    ADD COLUMN IF NOT EXISTS total_credits_purchased INTEGER NOT NULL DEFAULT 0 CHECK (total_credits_purchased >= 0),
    ADD COLUMN IF NOT EXISTS total_credits_consumed INTEGER NOT NULL DEFAULT 0 CHECK (total_credits_consumed >= 0),
    ADD COLUMN IF NOT EXISTS stripe_customer_id VARCHAR(255) UNIQUE,
    ADD COLUMN IF NOT EXISTS default_currency VARCHAR(3) DEFAULT 'USD',
    ADD COLUMN IF NOT EXISTS country_code VARCHAR(2), -- ISO country code for localization
    ADD COLUMN IF NOT EXISTS last_ip_address INET; -- For currency detection

-- 3. Create credit_transactions table (currency-agnostic)
CREATE TABLE IF NOT EXISTS credit_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL CHECK (type IN ('purchase', 'consumption', 'bonus', 'refund', 'adjustment', 'expiry')),
    amount INTEGER NOT NULL, -- positive for credit, negative for debit
    balance_before INTEGER NOT NULL CHECK (balance_before >= 0),
    balance_after INTEGER NOT NULL CHECK (balance_after >= 0),
    description TEXT NOT NULL,
    reference_id VARCHAR(255),
    reference_type VARCHAR(50) CHECK (reference_type IN ('job', 'payment', 'manual', 'system', 'promotion')),
    metadata JSONB DEFAULT '{}',
    expires_at TIMESTAMP WITH TIME ZONE, -- For bonus credits that expire
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Ensure balance math is correct
    CONSTRAINT balance_calculation_check CHECK (
        balance_after = balance_before + amount
    )
);

-- 4. Create stripe_payments table with multi-currency support
CREATE TABLE IF NOT EXISTS stripe_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_payment_intent_id VARCHAR(255) UNIQUE,
    stripe_checkout_session_id VARCHAR(255) UNIQUE,
    
    -- Multi-currency fields
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    currency VARCHAR(3) NOT NULL,
    converted_amount_cents INTEGER, -- Amount in user's local currency if different
    converted_currency VARCHAR(3), -- User's local currency
    exchange_rate DECIMAL(10, 6), -- Exchange rate used if conversion occurred
    
    credits_purchased INTEGER NOT NULL CHECK (credits_purchased > 0),
    status VARCHAR(50) NOT NULL CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'canceled', 'refunded', 'partial_refund')),
    stripe_receipt_url TEXT,
    failure_reason TEXT,
    refunded_amount_cents INTEGER DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    
    -- Ensure we have at least one Stripe ID
    CONSTRAINT must_have_stripe_id CHECK (
        stripe_payment_intent_id IS NOT NULL OR stripe_checkout_session_id IS NOT NULL
    )
);

-- 5. Create currency_pricing table for dynamic credit pricing
CREATE TABLE IF NOT EXISTS currency_pricing (
    currency VARCHAR(3) PRIMARY KEY,
    credits_per_unit INTEGER NOT NULL DEFAULT 1, -- How many credits per 1 unit of currency
    unit_price_cents INTEGER NOT NULL, -- Price in cents for credits_per_unit credits
    min_purchase_units INTEGER NOT NULL DEFAULT 10,
    max_purchase_units INTEGER NOT NULL DEFAULT 10000,
    is_active BOOLEAN DEFAULT TRUE,
    country_codes TEXT[], -- Array of country codes that use this currency
    display_symbol VARCHAR(5), -- Currency symbol for UI
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    CONSTRAINT positive_pricing CHECK (
        credits_per_unit > 0 AND unit_price_cents > 0
    )
);

-- 6. Create system_config table for global settings
CREATE TABLE IF NOT EXISTS system_config (
    key VARCHAR(255) PRIMARY KEY,
    value TEXT NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('integer', 'decimal', 'string', 'json', 'boolean')),
    description TEXT,
    min_value TEXT,
    max_value TEXT,
    is_secret BOOLEAN DEFAULT FALSE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_by TEXT
);

-- 7. Create promotional_codes table for marketing
CREATE TABLE IF NOT EXISTS promotional_codes (
    code VARCHAR(50) PRIMARY KEY,
    type VARCHAR(20) NOT NULL CHECK (type IN ('percentage', 'fixed_credits', 'bonus_credits')),
    value INTEGER NOT NULL, -- Percentage (0-100) or credit amount
    min_purchase_credits INTEGER, -- Minimum purchase to apply
    max_uses INTEGER,
    uses_count INTEGER DEFAULT 0,
    valid_from TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    valid_until TIMESTAMP WITH TIME ZONE,
    currency_restrictions VARCHAR(3)[], -- Limit to specific currencies
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    metadata JSONB DEFAULT '{}'
);

-- 8. Add job cost tracking
ALTER TABLE jobs 
    ADD COLUMN IF NOT EXISTS estimated_cost INTEGER,
    ADD COLUMN IF NOT EXISTS actual_cost INTEGER,
    ADD COLUMN IF NOT EXISTS cost_calculated_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS refunded_credits INTEGER DEFAULT 0;

-- 9. Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_credit_transactions_user_id ON credit_transactions(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_credit_transactions_reference ON credit_transactions(reference_id, reference_type);
CREATE INDEX IF NOT EXISTS idx_credit_transactions_expires ON credit_transactions(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_stripe_payments_user_id ON stripe_payments(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stripe_payments_status ON stripe_payments(status) WHERE status IN ('pending', 'processing');
CREATE INDEX IF NOT EXISTS idx_stripe_payments_currency ON stripe_payments(currency, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_promotional_codes_active ON promotional_codes(code) WHERE is_active = TRUE;

-- 10. Create updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply triggers
CREATE TRIGGER update_stripe_payments_updated_at BEFORE UPDATE ON stripe_payments
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_system_config_updated_at BEFORE UPDATE ON system_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_currency_pricing_updated_at BEFORE UPDATE ON currency_pricing
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 11. Insert default configuration
INSERT INTO system_config (key, value, type, description, min_value, max_value) VALUES
    ('signup_bonus_credits', '100', 'integer', 'Free credits for new users', '0', '1000'),
    ('base_job_cost', '10', 'integer', 'Base cost in credits per job', '1', '1000'),
    ('cost_per_keyword', '2', 'integer', 'Credits per keyword', '0', '100'),
    ('cost_per_depth_level', '5', 'integer', 'Credits per depth level', '0', '100'),
    ('cost_per_result', '1', 'integer', 'Credits per result returned', '0', '10'),
    ('fast_mode_multiplier', '1.5', 'decimal', 'Multiplier for fast mode', '1.0', '5.0'),
    ('max_depth_multiplier', '2.0', 'decimal', 'Multiplier for deep scraping', '1.0', '10.0'),
    ('credit_expiry_days', '365', 'integer', 'Days until purchased credits expire', '0', '730'),
    ('bonus_credit_expiry_days', '30', 'integer', 'Days until bonus credits expire', '0', '365'),
    ('low_balance_threshold', '50', 'integer', 'Low balance warning threshold', '0', '1000'),
    ('stripe_adaptive_pricing', 'true', 'boolean', 'Enable Stripe Adaptive Pricing', NULL, NULL),
    ('default_currency', 'USD', 'string', 'Default currency for new users', NULL, NULL)
ON CONFLICT (key) DO NOTHING;

-- 12. Insert currency pricing (only USD and EUR)
INSERT INTO currency_pricing (currency, credits_per_unit, unit_price_cents, min_purchase_units, max_purchase_units, country_codes, display_symbol) VALUES
    ('USD', 1, 100, 10, 10000, ARRAY['US', 'PR', 'GU', 'VI'], '$'),
    ('EUR', 1, 85, 10, 10000, ARRAY['DE', 'FR', 'IT', 'ES', 'NL', 'BE', 'AT', 'PT', 'FI', 'IE', 'GR', 'LU'], '€')
ON CONFLICT (currency) DO NOTHING;

-- 13. Grant permissions
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO scraper;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO scraper;
GRANT EXECUTE ON FUNCTION update_updated_at_column() TO scraper;

-- 14. Add helpful comments
COMMENT ON TABLE credit_transactions IS 'Audit log of all credit balance changes';
COMMENT ON TABLE stripe_payments IS 'Payment records with multi-currency support';
COMMENT ON TABLE currency_pricing IS 'Dynamic credit pricing per currency';
COMMENT ON TABLE promotional_codes IS 'Marketing promotional codes for discounts';
COMMENT ON COLUMN users.credit_balance IS 'Current credit balance (currency-agnostic)';
COMMENT ON COLUMN users.default_currency IS 'User preferred currency for display';
COMMENT ON COLUMN stripe_payments.converted_amount_cents IS 'Amount in local currency if Adaptive Pricing was used';

COMMIT;
