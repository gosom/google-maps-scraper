-- Migrate JobData.images bool → JobData.images_max int (per-job total).
--
-- The old schema used `images: true|false` as an on/off toggle. The new
-- schema uses `images_max: <int>` as a per-job total budget — 0 means
-- "skip image scraping", any positive value enables image scraping with
-- a hard cap on total images across all places in the job.
--
-- This migration only touches in-flight jobs (pending/working). Historical
-- completed rows are read-only and keep their old shape — no caller writes
-- back to those rows.
--
-- Backfill rule: rows with `images: true` get `images_max: 1000`. 1000 is
-- a sane mid-default per-job total — enough to cover a typical 20-50
-- place job at ~20 images/place average without hitting the cap, but well
-- under the 20000 hard ceiling. Rows with `images: false` (or no images
-- key at all) skip the backfill and stay at images_max=0.

-- Step 1: backfill images_max for in-flight rows that had images=true.
UPDATE jobs
SET data = jsonb_set(
    data,
    '{images_max}',
    to_jsonb(1000),
    true  -- create the key if missing
)
WHERE status IN ('pending', 'running')
  AND COALESCE((data->>'images')::bool, false) = true
  AND data->>'images_max' IS NULL;

-- Step 2: drop the now-unused `images` key from all in-flight rows.
UPDATE jobs
SET data = data - 'images'
WHERE status IN ('pending', 'running')
  AND data ? 'images';
