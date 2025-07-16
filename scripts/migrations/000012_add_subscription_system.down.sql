-- Rollback subscription system tables
-- This migration removes subscription plans, user subscriptions, and webhook events tracking

-- Revoke permissions from scraper user
REVOKE ALL PRIVILEGES ON TABLE subscription_plans FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE user_subscriptions FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE subscription_items FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE invoices FROM scraper;
REVOKE ALL PRIVILEGES ON TABLE webhook_events FROM scraper;
REVOKE ALL PRIVILEGES ON SEQUENCE user_subscriptions_id_seq FROM scraper;
REVOKE ALL PRIVILEGES ON SEQUENCE subscription_items_id_seq FROM scraper;
REVOKE ALL PRIVILEGES ON SEQUENCE invoices_id_seq FROM scraper;
REVOKE ALL PRIVILEGES ON SEQUENCE webhook_events_id_seq FROM scraper;

-- Drop indexes
DROP INDEX IF EXISTS idx_users_subscription_plan_id;

DROP INDEX IF EXISTS idx_webhook_events_processed_at;
DROP INDEX IF EXISTS idx_webhook_events_event_type;
DROP INDEX IF EXISTS idx_webhook_events_stripe_event_id;

DROP INDEX IF EXISTS idx_invoices_stripe_invoice_id;
DROP INDEX IF EXISTS idx_invoices_status;
DROP INDEX IF EXISTS idx_invoices_customer_id;
DROP INDEX IF EXISTS idx_invoices_subscription_id;

DROP INDEX IF EXISTS idx_subscription_items_stripe_price_id;
DROP INDEX IF EXISTS idx_subscription_items_stripe_id;
DROP INDEX IF EXISTS idx_subscription_items_subscription_id;

DROP INDEX IF EXISTS idx_user_subscriptions_current_period;
DROP INDEX IF EXISTS idx_user_subscriptions_billing_cycle_anchor;
DROP INDEX IF EXISTS idx_user_subscriptions_trial_end;
DROP INDEX IF EXISTS idx_user_subscriptions_status;
DROP INDEX IF EXISTS idx_user_subscriptions_stripe_subscription_id;
DROP INDEX IF EXISTS idx_user_subscriptions_stripe_customer_id;
DROP INDEX IF EXISTS idx_user_subscriptions_user_id;

-- Remove foreign key constraint from users table
ALTER TABLE users DROP CONSTRAINT IF EXISTS fk_users_subscription_plan_id;

-- Remove subscription_plan_id column from users table
ALTER TABLE users DROP COLUMN IF EXISTS subscription_plan_id;

-- Drop tables in reverse order of creation (respecting foreign key dependencies)
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS subscription_items;
DROP TABLE IF EXISTS user_subscriptions;
DROP TABLE IF EXISTS subscription_plans;