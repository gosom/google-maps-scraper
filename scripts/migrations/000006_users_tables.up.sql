-- Create users table for authentication
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- Add user_id to jobs table for data isolation
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS user_id TEXT REFERENCES users(id);

-- Create index for user jobs
CREATE INDEX IF NOT EXISTS idx_jobs_user_id ON jobs(user_id, created_at);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE users TO scraper;