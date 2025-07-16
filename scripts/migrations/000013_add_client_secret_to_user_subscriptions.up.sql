-- Add client_secret field to user_subscriptions table
-- This field stores the Stripe PaymentIntent client_secret for incomplete subscriptions

ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS client_secret TEXT;

-- Create index for client_secret lookups
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_client_secret ON user_subscriptions(client_secret);

-- Update permissions for scraper user
GRANT ALL PRIVILEGES ON TABLE user_subscriptions TO scraper;