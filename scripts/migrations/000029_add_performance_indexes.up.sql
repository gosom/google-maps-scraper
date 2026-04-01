-- Jobs table: composite index for the hot Select() query that filters by user_id + status
-- on non-deleted jobs. Covers: WHERE deleted_at IS NULL AND status = $1 AND user_id = $2
-- Existing idx_jobs_user_not_deleted covers (user_id, created_at) but not status filtering.
CREATE INDEX IF NOT EXISTS idx_jobs_user_status
    ON jobs(user_id, status)
    WHERE deleted_at IS NULL;

-- Jobs table: stuck job detection — find 'working' jobs ordered by updated_at
-- Allows quick identification of stale jobs without scanning the full table.
CREATE INDEX IF NOT EXISTS idx_jobs_status_updated
    ON jobs(updated_at)
    WHERE status = 'working';

-- Jobs table: ordering by creation date (descending) used by SELECT … ORDER BY created_at DESC
-- The existing idx_jobs_status covers (status, created_at) ASC; this index supports
-- pure created_at DESC ordering without a status filter.
CREATE INDEX IF NOT EXISTS idx_jobs_created_at
    ON jobs(created_at DESC)
    WHERE deleted_at IS NULL;
