BEGIN;

-- 1. Fix schema requirements
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

-- 2. Add credit fields to users table
ALTER TABLE users 
    ADD COLUMN credit_balance NUMERIC(18,6) NOT NULL DEFAULT 0 CHECK (credit_balance >= 0),
    ADD COLUMN total_credits_purchased NUMERIC(18,6) NOT NULL DEFAULT 0 CHECK (total_credits_purchased >= 0),
    ADD COLUMN total_credits_consumed NUMERIC(18,6) NOT NULL DEFAULT 0 CHECK (total_credits_consumed >= 0),
    ADD COLUMN stripe_customer_id VARCHAR(255) UNIQUE,
    ADD COLUMN default_currency VARCHAR(3) DEFAULT 'USD',
    ADD COLUMN country_code VARCHAR(2),
    ADD COLUMN last_ip_address INET;

-- 3. Create credit_transactions table - tracks all balance changes
CREATE TABLE credit_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL CHECK (type IN ('purchase', 'consumption', 'bonus', 'refund', 'adjustment')),
    amount NUMERIC(18,6) NOT NULL, -- positive for credit, negative for debit
    balance_before NUMERIC(18,6) NOT NULL CHECK (balance_before >= 0),
    balance_after NUMERIC(18,6) NOT NULL CHECK (balance_after >= 0),
    description TEXT NOT NULL,
    reference_id VARCHAR(255),
    reference_type VARCHAR(50) CHECK (reference_type IN ('job', 'payment', 'manual', 'system', 'billing_event')),
    billing_event_id UUID, -- THIS LINE IS DIFFERENT - NO FOREIGN KEY!
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Ensure balance math is correct
    CONSTRAINT balance_calculation_check CHECK (
        balance_after = balance_before + amount
    )
);

-- 4. Create stripe_payments table
CREATE TABLE stripe_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_payment_intent_id VARCHAR(255) UNIQUE,
    stripe_checkout_session_id VARCHAR(255) UNIQUE,
    
    -- Payment details
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    currency VARCHAR(3) NOT NULL,
    credits_purchased NUMERIC(18,6) NOT NULL CHECK (credits_purchased > 0),
    
    -- Status tracking
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

-- 5. Add job credit tracking (complementing billing system's cost tracking)
ALTER TABLE jobs 
    ADD COLUMN refunded_credits NUMERIC(18,6) DEFAULT 0,
    ADD COLUMN cost_calculated_at TIMESTAMP WITH TIME ZONE;

-- 6. Create indexes for performance
CREATE INDEX idx_credit_transactions_user_id ON credit_transactions(user_id, created_at DESC);
CREATE INDEX idx_credit_transactions_reference ON credit_transactions(reference_id, reference_type);
CREATE INDEX idx_credit_transactions_billing_event ON credit_transactions(billing_event_id);
CREATE INDEX idx_stripe_payments_user_id ON stripe_payments(user_id, created_at DESC);
CREATE INDEX idx_stripe_payments_status ON stripe_payments(status) WHERE status IN ('pending', 'processing');

-- 7. Create updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 8. Apply triggers
CREATE TRIGGER update_stripe_payments_updated_at 
    BEFORE UPDATE ON stripe_payments
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_users_updated_at 
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 9. Grant permissions
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO scraper;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO scraper;
GRANT EXECUTE ON FUNCTION update_updated_at_column() TO scraper;

-- 10. Add comments
COMMENT ON TABLE credit_transactions IS 'Audit log of all credit balance changes';
COMMENT ON TABLE stripe_payments IS 'Stripe payment records for credit purchases';
COMMENT ON COLUMN users.credit_balance IS 'Current credit balance';
COMMENT ON COLUMN credit_transactions.billing_event_id IS 'Logical link to billing_events (no FK to allow independent migrations)';

COMMIT;