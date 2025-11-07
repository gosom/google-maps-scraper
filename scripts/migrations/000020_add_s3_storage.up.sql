BEGIN;

-- Create job_files table for S3 file storage
CREATE TABLE IF NOT EXISTS job_files (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Relationships (proper foreign keys with cascading deletes)
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- File type/format
    file_type VARCHAR(20) NOT NULL CHECK (file_type IN ('csv', 'json', 'xlsx', 'log', 'archive')),

    -- S3 location (BEST PRACTICE: separate bucket and key columns)
    bucket_name VARCHAR(63) NOT NULL,
    object_key TEXT NOT NULL,

    -- S3 metadata (from AWS API responses)
    version_id VARCHAR(1024),  -- S3 version ID if bucket versioning enabled
    etag VARCHAR(100) NOT NULL,  -- S3 ETag for integrity verification

    -- File metadata
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    mime_type VARCHAR(100) NOT NULL DEFAULT 'text/csv',

    -- Status lifecycle
    status VARCHAR(20) NOT NULL DEFAULT 'uploading'
        CHECK (status IN ('uploading', 'available', 'failed', 'archived', 'deleted')),

    -- Error tracking
    error_message TEXT,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    uploaded_at TIMESTAMPTZ,
    last_accessed_at TIMESTAMPTZ,

    -- Business constraints
    UNIQUE(job_id, file_type),  -- One file per type per job (one CSV, one JSON, etc.)
    UNIQUE(bucket_name, object_key),  -- Prevent duplicate S3 references

    -- Data validation constraints
    CHECK (bucket_name != '' AND object_key != ''),
    CHECK ((status = 'available' AND uploaded_at IS NOT NULL) OR status != 'available'),
    CHECK ((status = 'failed' AND error_message IS NOT NULL) OR status != 'failed')
);

-- Indexes for performance
CREATE INDEX idx_job_files_job_id ON job_files(job_id);
CREATE INDEX idx_job_files_user_id ON job_files(user_id, created_at DESC);
CREATE INDEX idx_job_files_s3_location ON job_files(bucket_name, object_key);
CREATE INDEX idx_job_files_status ON job_files(status) WHERE status = 'available';
CREATE INDEX idx_job_files_type ON job_files(file_type, created_at DESC);
CREATE INDEX idx_job_files_etag ON job_files(etag);

-- Grant permissions to scraper user (consistent with other migrations)
GRANT ALL PRIVILEGES ON TABLE job_files TO scraper;

-- Documentation
COMMENT ON TABLE job_files IS 'S3-backed file storage for job outputs - separate table for separation of concerns';
COMMENT ON COLUMN job_files.job_id IS 'Foreign key to jobs table - cascades on delete';
COMMENT ON COLUMN job_files.user_id IS 'Foreign key to users table for data isolation - cascades on delete';
COMMENT ON COLUMN job_files.file_type IS 'File format: csv (main export), json (alternative), xlsx (spreadsheet), log (debug), archive (compressed)';
COMMENT ON COLUMN job_files.bucket_name IS 'S3 bucket name (separate from key for easy bucket migration) - Stack Overflow consensus';
COMMENT ON COLUMN job_files.object_key IS 'S3 object key path, format: users/{user_id}/jobs/{job_id}/{file_type}.{ext}';
COMMENT ON COLUMN job_files.version_id IS 'S3 version ID if bucket versioning is enabled (for file history tracking)';
COMMENT ON COLUMN job_files.etag IS 'S3 ETag from PutObject response - use for integrity verification (MD5 for <5MB, multipart hash for larger)';
COMMENT ON COLUMN job_files.status IS 'File lifecycle: uploading → available (success) or → failed (error)';

COMMIT;
