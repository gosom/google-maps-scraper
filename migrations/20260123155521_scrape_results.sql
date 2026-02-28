-- +migrate Up

CREATE TABLE IF NOT EXISTS scrape_results (
    job_id BIGINT PRIMARY KEY,
    keyword TEXT NOT NULL,
    results JSONB,
    result_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() AT TIME ZONE 'UTC')
);

CREATE INDEX idx_scrape_results_created_at ON scrape_results(created_at);
CREATE INDEX idx_scrape_results_keyword ON scrape_results(keyword);

-- +migrate Down
