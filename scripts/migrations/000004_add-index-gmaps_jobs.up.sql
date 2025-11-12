BEGIN;

-- Create index on gmaps_jobs for efficient job selection
CREATE INDEX IF NOT EXISTS idx_gmaps_jobs_status_priority_created ON gmaps_jobs(status, priority ASC, created_at ASC);

-- Grant permissions on the table to ensure access to the index
GRANT ALL PRIVILEGES ON TABLE gmaps_jobs TO scraper;

COMMIT;