BEGIN;

-- Remove indexes
DROP INDEX IF EXISTS idx_results_user_id;
DROP INDEX IF EXISTS idx_results_job_id;
DROP INDEX IF EXISTS idx_results_user_job;
DROP INDEX IF EXISTS idx_results_input_id;
DROP INDEX IF EXISTS idx_results_cid;

-- Remove added columns (in reverse order)
ALTER TABLE results DROP COLUMN IF EXISTS emails;
ALTER TABLE results DROP COLUMN IF EXISTS user_reviews_extended;
ALTER TABLE results DROP COLUMN IF EXISTS user_reviews;
ALTER TABLE results DROP COLUMN IF EXISTS about;
ALTER TABLE results DROP COLUMN IF EXISTS complete_address;
ALTER TABLE results DROP COLUMN IF EXISTS owner;
ALTER TABLE results DROP COLUMN IF EXISTS menu;
ALTER TABLE results DROP COLUMN IF EXISTS order_online;
ALTER TABLE results DROP COLUMN IF EXISTS reservations;
ALTER TABLE results DROP COLUMN IF EXISTS images;
ALTER TABLE results DROP COLUMN IF EXISTS data_id;
ALTER TABLE results DROP COLUMN IF EXISTS price_range;
ALTER TABLE results DROP COLUMN IF EXISTS timezone;
ALTER TABLE results DROP COLUMN IF EXISTS thumbnail;
ALTER TABLE results DROP COLUMN IF EXISTS reviews_link;
ALTER TABLE results DROP COLUMN IF EXISTS description;
ALTER TABLE results DROP COLUMN IF EXISTS status_info;
ALTER TABLE results DROP COLUMN IF EXISTS reviews_per_rating;
ALTER TABLE results DROP COLUMN IF EXISTS popular_times;
ALTER TABLE results DROP COLUMN IF EXISTS open_hours;
ALTER TABLE results DROP COLUMN IF EXISTS categories;
ALTER TABLE results DROP COLUMN IF EXISTS cid;
ALTER TABLE results DROP COLUMN IF EXISTS link;
ALTER TABLE results DROP COLUMN IF EXISTS input_id;
ALTER TABLE results DROP COLUMN IF EXISTS created_at;
ALTER TABLE results DROP COLUMN IF EXISTS job_id;
ALTER TABLE results DROP COLUMN IF EXISTS user_id;

COMMIT;
