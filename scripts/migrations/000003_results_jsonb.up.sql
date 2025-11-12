BEGIN;
    -- Convert results to use JSONB for more flexibility
    ALTER TABLE results ADD COLUMN IF NOT EXISTS data JSONB;

    -- Migrate data if needed (this would typically have data migration logic)
    
    -- Grant permissions
    GRANT ALL PRIVILEGES ON TABLE results TO scraper;
COMMIT;