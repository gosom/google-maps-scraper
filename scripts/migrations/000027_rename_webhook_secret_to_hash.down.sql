-- Revert webhook_secret_hash back to webhook_secret
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS chk_webhook_secret_hash;
ALTER TABLE jobs RENAME COLUMN webhook_secret_hash TO webhook_secret;
ALTER TABLE jobs ADD CONSTRAINT chk_webhook_secret
  CHECK (webhook_url IS NULL OR webhook_secret IS NOT NULL);
