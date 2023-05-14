BEGIN;
    CREATE TABLE gmaps_jobs(
        id UUID PRIMARY KEY,
        priority SMALLINT NOT NULL,
        payload_type TEXT NOT NULL,
        payload BYTEA NOT NULL,
        created_at TIMESTAMP WITH TIME ZONE NOT NULL,
        status TEXT NOT NULL
    );

    CREATE TABLE results(
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

COMMIT;
