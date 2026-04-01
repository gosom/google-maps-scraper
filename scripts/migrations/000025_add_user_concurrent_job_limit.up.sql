-- Add per-user concurrent job limit column.
-- Default of 2 applies to free-tier and any user without an explicit override.
-- Admins can raise this per-user for paid tiers or manual overrides.
ALTER TABLE users ADD COLUMN IF NOT EXISTS max_concurrent_jobs INTEGER NOT NULL DEFAULT 2;
