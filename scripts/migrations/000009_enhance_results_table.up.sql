BEGIN;

-- Add user_id and job_id to results table for proper data association
ALTER TABLE results ADD COLUMN IF NOT EXISTS user_id TEXT REFERENCES users(id);
ALTER TABLE results ADD COLUMN IF NOT EXISTS job_id TEXT REFERENCES jobs(id);

-- Add created_at timestamp for tracking when results were saved
ALTER TABLE results ADD COLUMN IF NOT EXISTS created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW();

-- Add all missing columns to match CSV format exactly
ALTER TABLE results ADD COLUMN IF NOT EXISTS input_id TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS link TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS cid TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS categories TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS open_hours JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS popular_times JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS reviews_per_rating JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS status_info TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS description TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS reviews_link TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS thumbnail TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS timezone TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS price_range TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS data_id TEXT;
ALTER TABLE results ADD COLUMN IF NOT EXISTS images JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS reservations JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS order_online JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS menu JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS owner JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS complete_address JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS about JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS user_reviews JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS user_reviews_extended JSONB;
ALTER TABLE results ADD COLUMN IF NOT EXISTS emails TEXT;

-- Create indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_results_user_id ON results(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_results_job_id ON results(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_results_user_job ON results(user_id, job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_results_input_id ON results(input_id);
CREATE INDEX IF NOT EXISTS idx_results_cid ON results(cid);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE results TO scraper;

COMMIT;
