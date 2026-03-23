-- Create web jobs table for the web API
CREATE TABLE IF NOT EXISTS jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('web', 'api'))
);

-- Create index for status queries
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at);

-- Grant permissions to scraper user
GRANT ALL PRIVILEGES ON TABLE jobs TO scraper;