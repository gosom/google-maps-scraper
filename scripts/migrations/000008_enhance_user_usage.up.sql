-- Enhance user_usage table with detailed tracking
ALTER TABLE user_usage ADD COLUMN IF NOT EXISTS total_jobs_run INTEGER DEFAULT 0;
ALTER TABLE user_usage ADD COLUMN IF NOT EXISTS total_locations_scraped INTEGER DEFAULT 0;
ALTER TABLE user_usage ADD COLUMN IF NOT EXISTS total_emails_extracted INTEGER DEFAULT 0;
ALTER TABLE user_usage ADD COLUMN IF NOT EXISTS current_month_jobs INTEGER DEFAULT 0;
ALTER TABLE user_usage ADD COLUMN IF NOT EXISTS current_month_locations INTEGER DEFAULT 0;
ALTER TABLE user_usage ADD COLUMN IF NOT EXISTS current_month_emails INTEGER DEFAULT 0;
ALTER TABLE user_usage ADD COLUMN IF NOT EXISTS last_reset_date DATE DEFAULT CURRENT_DATE;

-- Create job_usage_details table for individual job tracking
CREATE TABLE IF NOT EXISTS job_usage_details (
    id SERIAL PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    
    -- Job metrics
    total_locations_found INTEGER DEFAULT 0,
    total_emails_found INTEGER DEFAULT 0,
    job_duration_seconds INTEGER DEFAULT 0,
    job_status VARCHAR(20) NOT NULL DEFAULT 'pending',
    
    -- Features used
    email_extraction_enabled BOOLEAN DEFAULT FALSE,
    fast_mode_enabled BOOLEAN DEFAULT FALSE,
    
    -- Timestamps
    started_at TIMESTAMP NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    CONSTRAINT unique_job_usage UNIQUE(job_id)
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_job_usage_user_id ON job_usage_details(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_job_usage_status ON job_usage_details(job_status);
CREATE INDEX IF NOT EXISTS idx_user_usage_reset_date ON user_usage(last_reset_date);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE job_usage_details TO scraper;
GRANT ALL PRIVILEGES ON SEQUENCE job_usage_details_id_seq TO scraper;
