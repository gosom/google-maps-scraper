-- Add subscription system tables
-- This migration adds subscription plans, user subscriptions, and webhook events tracking

-- Create subscription plans table
CREATE TABLE IF NOT EXISTS subscription_plans (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    stripe_price_id TEXT NOT NULL UNIQUE,
    price_cents INTEGER NOT NULL,
    interval TEXT NOT NULL CHECK (interval IN ('month', 'year')),
    daily_job_limit INTEGER NOT NULL,
    features JSONB DEFAULT '{}',
    active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create user subscriptions table
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id SERIAL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_customer_id TEXT NOT NULL,
    stripe_subscription_id TEXT UNIQUE,
    plan_id TEXT NOT NULL REFERENCES subscription_plans(id),
    status TEXT NOT NULL CHECK (status IN ('active', 'past_due', 'canceled', 'incomplete', 'incomplete_expired', 'trialing', 'unpaid', 'paused')),
    current_period_start TIMESTAMP,
    current_period_end TIMESTAMP,
    trial_start TIMESTAMP,
    trial_end TIMESTAMP,
    billing_cycle_anchor TIMESTAMP,
    canceled_at TIMESTAMP,
    latest_invoice_id TEXT,
    cancel_at_period_end BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_subscription UNIQUE(user_id),
    CONSTRAINT trial_dates_consistency CHECK (trial_start IS NULL OR trial_end IS NULL OR trial_start <= trial_end)
);

-- Create subscription items table (for multiple items per subscription)
CREATE TABLE IF NOT EXISTS subscription_items (
    id SERIAL PRIMARY KEY,
    stripe_subscription_item_id TEXT NOT NULL UNIQUE,
    subscription_id INTEGER NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
    stripe_price_id TEXT NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 1,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create invoices table
CREATE TABLE IF NOT EXISTS invoices (
    id SERIAL PRIMARY KEY,
    stripe_invoice_id TEXT NOT NULL UNIQUE,
    subscription_id INTEGER REFERENCES user_subscriptions(id) ON DELETE SET NULL,
    customer_id TEXT NOT NULL,
    amount_due INTEGER NOT NULL,
    amount_paid INTEGER NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'usd',
    status TEXT NOT NULL CHECK (status IN ('draft', 'open', 'paid', 'void', 'uncollectible')),
    billing_reason TEXT,
    due_date TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create webhook events table for idempotency
CREATE TABLE IF NOT EXISTS webhook_events (
    id SERIAL PRIMARY KEY,
    stripe_event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMP NOT NULL DEFAULT NOW(),
    data JSONB
);

-- Insert default subscription plans first
INSERT INTO subscription_plans (id, name, stripe_price_id, price_cents, interval, daily_job_limit, features) VALUES
('free', 'Free Plan', 'price_free', 0, 'month', 5, '{"basic_scraping": true, "email_extraction": false, "advanced_filters": false}'),
('pro', 'Pro Plan', 'price_pro_monthly', 2900, 'month', 100, '{"basic_scraping": true, "email_extraction": true, "advanced_filters": true, "priority_support": true}')
ON CONFLICT (id) DO NOTHING;

-- Add subscription_plan_id to users table for easier queries (without FK constraint initially)
ALTER TABLE users ADD COLUMN IF NOT EXISTS subscription_plan_id TEXT DEFAULT 'free';

-- Update existing users to have free plan
UPDATE users SET subscription_plan_id = 'free' WHERE subscription_plan_id IS NULL;

-- Now add the foreign key constraint
ALTER TABLE users DROP CONSTRAINT IF EXISTS fk_users_subscription_plan_id;
ALTER TABLE users ADD CONSTRAINT fk_users_subscription_plan_id FOREIGN KEY (subscription_plan_id) REFERENCES subscription_plans(id);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user_id ON user_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_stripe_customer_id ON user_subscriptions(stripe_customer_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_stripe_subscription_id ON user_subscriptions(stripe_subscription_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_status ON user_subscriptions(status);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_trial_end ON user_subscriptions(trial_end) WHERE trial_end IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_billing_cycle_anchor ON user_subscriptions(billing_cycle_anchor);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_current_period ON user_subscriptions(current_period_start, current_period_end);

CREATE INDEX IF NOT EXISTS idx_subscription_items_subscription_id ON subscription_items(subscription_id);
CREATE INDEX IF NOT EXISTS idx_subscription_items_stripe_id ON subscription_items(stripe_subscription_item_id);
CREATE INDEX IF NOT EXISTS idx_subscription_items_stripe_price_id ON subscription_items(stripe_price_id);

CREATE INDEX IF NOT EXISTS idx_invoices_subscription_id ON invoices(subscription_id);
CREATE INDEX IF NOT EXISTS idx_invoices_customer_id ON invoices(customer_id);
CREATE INDEX IF NOT EXISTS idx_invoices_status ON invoices(status);
CREATE INDEX IF NOT EXISTS idx_invoices_stripe_invoice_id ON invoices(stripe_invoice_id);

CREATE INDEX IF NOT EXISTS idx_webhook_events_stripe_event_id ON webhook_events(stripe_event_id);
CREATE INDEX IF NOT EXISTS idx_webhook_events_event_type ON webhook_events(event_type);
CREATE INDEX IF NOT EXISTS idx_webhook_events_processed_at ON webhook_events(processed_at);

CREATE INDEX IF NOT EXISTS idx_users_subscription_plan_id ON users(subscription_plan_id);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE subscription_plans TO scraper;
GRANT ALL PRIVILEGES ON TABLE user_subscriptions TO scraper;
GRANT ALL PRIVILEGES ON TABLE subscription_items TO scraper;
GRANT ALL PRIVILEGES ON TABLE invoices TO scraper;
GRANT ALL PRIVILEGES ON TABLE webhook_events TO scraper;
GRANT ALL PRIVILEGES ON SEQUENCE user_subscriptions_id_seq TO scraper;
GRANT ALL PRIVILEGES ON SEQUENCE subscription_items_id_seq TO scraper;
GRANT ALL PRIVILEGES ON SEQUENCE invoices_id_seq TO scraper;
GRANT ALL PRIVILEGES ON SEQUENCE webhook_events_id_seq TO scraper;