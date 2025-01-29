BEGIN;

CREATE INDEX idx_gmaps_jobs_status_priority_created ON gmaps_jobs(status, priority ASC, created_at ASC);

COMMIT;
