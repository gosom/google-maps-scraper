package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gosom/google-maps-scraper/models"
)

type jobFileRepository struct {
	db *sql.DB
}

// NewJobFileRepository creates a new PostgreSQL implementation of models.JobFileRepository
func NewJobFileRepository(db *sql.DB) (models.JobFileRepository, error) {
	if err := db.Ping(); err != nil {
		return nil, err
	}

	return &jobFileRepository{db: db}, nil
}

// Create inserts a new job file record into the database
func (r *jobFileRepository) Create(ctx context.Context, jobFile *models.JobFile) error {
	const q = `
		INSERT INTO job_files (
			id, job_id, user_id, file_type, bucket_name, object_key,
			version_id, etag, size_bytes, mime_type, status, error_message,
			created_at, uploaded_at, last_accessed_at
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11,
			$12, $13, $14
		) RETURNING id`

	err := r.db.QueryRowContext(
		ctx, q,
		jobFile.JobID,
		jobFile.UserID,
		jobFile.FileType,
		jobFile.BucketName,
		jobFile.ObjectKey,
		jobFile.VersionID,
		jobFile.ETag,
		jobFile.SizeBytes,
		jobFile.MimeType,
		jobFile.Status,
		jobFile.ErrorMessage,
		jobFile.CreatedAt,
		jobFile.UploadedAt,
		jobFile.LastAccessedAt,
	).Scan(&jobFile.ID)

	if err != nil {
		return fmt.Errorf("failed to create job file: %w", err)
	}

	return nil
}

// Get retrieves a job file by ID
func (r *jobFileRepository) Get(ctx context.Context, id string) (*models.JobFile, error) {
	const q = `
		SELECT
			id, job_id, user_id, file_type, bucket_name, object_key,
			version_id, etag, size_bytes, mime_type, status, error_message,
			created_at, uploaded_at, last_accessed_at
		FROM job_files
		WHERE id = $1`

	jobFile := &models.JobFile{}
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&jobFile.ID,
		&jobFile.JobID,
		&jobFile.UserID,
		&jobFile.FileType,
		&jobFile.BucketName,
		&jobFile.ObjectKey,
		&jobFile.VersionID,
		&jobFile.ETag,
		&jobFile.SizeBytes,
		&jobFile.MimeType,
		&jobFile.Status,
		&jobFile.ErrorMessage,
		&jobFile.CreatedAt,
		&jobFile.UploadedAt,
		&jobFile.LastAccessedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("job file with id %s not found", id)
		}
		return nil, fmt.Errorf("failed to get job file: %w", err)
	}

	return jobFile, nil
}

// GetByJobID retrieves a job file by job ID and file type
func (r *jobFileRepository) GetByJobID(ctx context.Context, jobID string, fileType string) (*models.JobFile, error) {
	const q = `
		SELECT
			id, job_id, user_id, file_type, bucket_name, object_key,
			version_id, etag, size_bytes, mime_type, status, error_message,
			created_at, uploaded_at, last_accessed_at
		FROM job_files
		WHERE job_id = $1 AND file_type = $2
		ORDER BY created_at DESC
		LIMIT 1`

	jobFile := &models.JobFile{}
	err := r.db.QueryRowContext(ctx, q, jobID, fileType).Scan(
		&jobFile.ID,
		&jobFile.JobID,
		&jobFile.UserID,
		&jobFile.FileType,
		&jobFile.BucketName,
		&jobFile.ObjectKey,
		&jobFile.VersionID,
		&jobFile.ETag,
		&jobFile.SizeBytes,
		&jobFile.MimeType,
		&jobFile.Status,
		&jobFile.ErrorMessage,
		&jobFile.CreatedAt,
		&jobFile.UploadedAt,
		&jobFile.LastAccessedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("job file for job %s with type %s not found", jobID, fileType)
		}
		return nil, fmt.Errorf("failed to get job file: %w", err)
	}

	return jobFile, nil
}

// Update modifies an existing job file
func (r *jobFileRepository) Update(ctx context.Context, jobFile *models.JobFile) error {
	const q = `
		UPDATE job_files SET
			status = $1,
			error_message = $2,
			uploaded_at = $3,
			last_accessed_at = $4,
			etag = $5,
			version_id = $6,
			size_bytes = $7
		WHERE id = $8`

	result, err := r.db.ExecContext(
		ctx, q,
		jobFile.Status,
		jobFile.ErrorMessage,
		jobFile.UploadedAt,
		jobFile.LastAccessedAt,
		jobFile.ETag,
		jobFile.VersionID,
		jobFile.SizeBytes,
		jobFile.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update job file: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("job file with id %s not found", jobFile.ID)
	}

	return nil
}

// Delete removes a job file record from the database
func (r *jobFileRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM job_files WHERE id = $1`

	result, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("failed to delete job file: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("job file with id %s not found", id)
	}

	return nil
}

// ListByJobID retrieves all files for a specific job
func (r *jobFileRepository) ListByJobID(ctx context.Context, jobID string) ([]*models.JobFile, error) {
	const q = `
		SELECT
			id, job_id, user_id, file_type, bucket_name, object_key,
			version_id, etag, size_bytes, mime_type, status, error_message,
			created_at, uploaded_at, last_accessed_at
		FROM job_files
		WHERE job_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to list job files: %w", err)
	}
	defer rows.Close()

	var files []*models.JobFile
	for rows.Next() {
		jobFile := &models.JobFile{}
		err := rows.Scan(
			&jobFile.ID,
			&jobFile.JobID,
			&jobFile.UserID,
			&jobFile.FileType,
			&jobFile.BucketName,
			&jobFile.ObjectKey,
			&jobFile.VersionID,
			&jobFile.ETag,
			&jobFile.SizeBytes,
			&jobFile.MimeType,
			&jobFile.Status,
			&jobFile.ErrorMessage,
			&jobFile.CreatedAt,
			&jobFile.UploadedAt,
			&jobFile.LastAccessedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job file: %w", err)
		}
		files = append(files, jobFile)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating job files: %w", err)
	}

	return files, nil
}

// ListByUserID retrieves all files for a specific user
func (r *jobFileRepository) ListByUserID(ctx context.Context, userID string) ([]*models.JobFile, error) {
	const q = `
		SELECT
			id, job_id, user_id, file_type, bucket_name, object_key,
			version_id, etag, size_bytes, mime_type, status, error_message,
			created_at, uploaded_at, last_accessed_at
		FROM job_files
		WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list job files: %w", err)
	}
	defer rows.Close()

	var files []*models.JobFile
	for rows.Next() {
		jobFile := &models.JobFile{}
		err := rows.Scan(
			&jobFile.ID,
			&jobFile.JobID,
			&jobFile.UserID,
			&jobFile.FileType,
			&jobFile.BucketName,
			&jobFile.ObjectKey,
			&jobFile.VersionID,
			&jobFile.ETag,
			&jobFile.SizeBytes,
			&jobFile.MimeType,
			&jobFile.Status,
			&jobFile.ErrorMessage,
			&jobFile.CreatedAt,
			&jobFile.UploadedAt,
			&jobFile.LastAccessedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job file: %w", err)
		}
		files = append(files, jobFile)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating job files: %w", err)
	}

	return files, nil
}
