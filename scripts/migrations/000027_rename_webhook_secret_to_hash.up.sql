-- Rename webhook_secret to webhook_secret_hash to signal that this column
-- MUST store a hashed value (HMAC-SHA256 or Argon2id), NOT plaintext.
-- The plaintext secret should only be returned to the user once at creation time.
--
-- TODO: Before enabling webhook delivery, implement hashing in the application
-- layer. The webhook handler must:
--   1. Hash the user-provided secret with HMAC-SHA256 (using WEBHOOK_SECRET_KEY env)
--      or Argon2id before storing.
--   2. Return the plaintext secret to the user exactly once at job creation.
--   3. When delivering webhooks, sign the payload with the plaintext key (which the
--      user stored) and let the receiver verify — the DB only stores the hash for
--      verification that the user still has the correct secret if needed.

-- Drop old constraint that references the old column name
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS chk_webhook_secret;

-- Rename the column
ALTER TABLE jobs RENAME COLUMN webhook_secret TO webhook_secret_hash;

-- Recreate constraint with new column name
ALTER TABLE jobs ADD CONSTRAINT chk_webhook_secret_hash
  CHECK (webhook_url IS NULL OR webhook_secret_hash IS NOT NULL);
