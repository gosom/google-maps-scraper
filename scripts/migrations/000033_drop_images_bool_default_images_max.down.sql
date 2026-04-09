-- Best-effort revert of 000033: restore `images: true` on in-flight rows
-- that have a positive images_max budget. This is NOT lossless — the exact
-- per-job budget value is discarded, and rows that were originally
-- images=false but now have images_max>0 (which could happen if a user
-- created a new job after the up migration) will get images=true here.
--
-- Only touches in-flight rows so historical completed jobs stay untouched.

UPDATE jobs
SET data = jsonb_set(data, '{images}', 'true'::jsonb, true)
WHERE status IN ('pending', 'working')
  AND COALESCE((data->>'images_max')::int, 0) > 0;
