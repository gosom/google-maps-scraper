BEGIN;
    CREATE TABLE IF NOT EXISTS gmaps_jobs(
        id UUID PRIMARY KEY,
        priority SMALLINT NOT NULL,
        payload_type TEXT NOT NULL,
        payload BYTEA NOT NULL,
        created_at TIMESTAMP WITH TIME ZONE NOT NULL,
        status TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS results(
        id INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
        title TEXT NOT NULL,
        category TEXT NOT NULL,
        address TEXT NOT NULL,
        openhours TEXT NOT NULL,
        website TEXT NOT NULL,
        phone TEXT NOT NULL,
        pluscode TEXT  NOT NULL,
        review_count INT NOT NULL,
        rating NUMERIC NOT NULL
    );

    -- Grant permissions to scraper user
    GRANT ALL PRIVILEGES ON TABLE gmaps_jobs TO scraper;
    GRANT ALL PRIVILEGES ON TABLE results TO scraper;
    GRANT ALL PRIVILEGES ON SEQUENCE results_id_seq TO scraper;

COMMIT;