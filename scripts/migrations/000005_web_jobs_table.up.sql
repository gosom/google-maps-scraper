-- Create web jobs table for the web API
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create index for status queries
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE jobs TO scraper;