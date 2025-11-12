package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

type repository struct {
	db *sql.DB
}

// NewRepository creates a new PostgreSQL implementation of models.JobRepository
func NewRepository(db *sql.DB) (models.JobRepository, error) {
	if err := db.Ping(); err != nil {
		return nil, err
	}

	// We don't create schema here anymore since we're using migrations
	return &repository{db: db}, nil
}

// Get retrieves a job by ID (only non-deleted jobs)
func (repo *repository) Get(ctx context.Context, id string) (models.Job, error) {
	const q = `SELECT id, name, status, data, extract(epoch from created_at), extract(epoch from updated_at), user_id, COALESCE(failure_reason, '') 
               FROM jobs WHERE id = $1 AND deleted_at IS NULL`

	row := repo.db.QueryRowContext(ctx, q, id)

	return rowToJob(row)
}

// Create inserts a new job into the database
func (repo *repository) Create(ctx context.Context, job *models.Job) error {
	item, err := jobToRow(job)
	if err != nil {
		return err
	}

	const q = `INSERT INTO jobs (id, name, status, data, created_at, updated_at, user_id, failure_reason) 
               VALUES ($1, $2, $3, $4, to_timestamp($5), to_timestamp($6), $7, $8)`

	_, err = repo.db.ExecContext(ctx, q, item.ID, item.Name, item.Status, item.Data, item.CreatedAt, item.UpdatedAt, item.UserID, item.FailureReason)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

// Delete marks a job as deleted (soft delete) without removing the valuable results data
// This preserves all scraped results for potential future business use
func (repo *repository) Delete(ctx context.Context, id string) error {
	const q = `UPDATE jobs SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`

	result, err := repo.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	// Check if the job actually existed and wasn't already deleted
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("job with id %s not found or already deleted", id)
	}

	return nil
}

// Select finds jobs based on the provided parameters (only non-deleted jobs)
func (repo *repository) Select(ctx context.Context, params models.SelectParams) ([]models.Job, error) {
	q := `SELECT id, name, status, data, extract(epoch from created_at), extract(epoch from updated_at), user_id, COALESCE(failure_reason, '') FROM jobs`

	var args []interface{}
	var conditions []string
	var argNum int = 1

	// Always filter out deleted jobs
	conditions = append(conditions, "deleted_at IS NULL")

	if params.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argNum))
		args = append(args, params.Status)
		argNum++
	}

	if params.UserID != "" {
		conditions = append(conditions, fmt.Sprintf("(user_id = $%d OR user_id IS NULL)", argNum))
		args = append(args, params.UserID)
		argNum++
	}

	if len(conditions) > 0 {
		q += " WHERE " + conditions[0]
		for i := 1; i < len(conditions); i++ {
			q += " AND " + conditions[i]
		}
	}

	q += " ORDER BY created_at DESC"

	if params.Limit > 0 {
		q += fmt.Sprintf(" LIMIT $%d", argNum)
		args = append(args, params.Limit)
	}

	rows, err := repo.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to select jobs: %w", err)
	}

	defer rows.Close()

	var ans []models.Job

	for rows.Next() {
		job, err := rowToJob(rows)
		if err != nil {
			return nil, err
		}

		ans = append(ans, job)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ans, nil
}

// Update modifies an existing job
func (repo *repository) Update(ctx context.Context, job *models.Job) error {
	item, err := jobToRow(job)
	if err != nil {
		return err
	}

	const q = `UPDATE jobs SET name = $1, status = $2, data = $3, updated_at = to_timestamp($4), user_id = $5, failure_reason = $6 WHERE id = $7`

	_, err = repo.db.ExecContext(ctx, q, item.Name, item.Status, item.Data, item.UpdatedAt, item.UserID, item.FailureReason, item.ID)
	if err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	return nil
}

// Cancel marks a job for cancellation
func (repo *repository) Cancel(ctx context.Context, id string) error {
	// First check if job exists and is in a cancellable state
	var currentStatus string
	err := repo.db.QueryRowContext(ctx, "SELECT status FROM jobs WHERE id = $1 AND deleted_at IS NULL", id).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("job with id %s not found", id)
		}
		return fmt.Errorf("failed to get job status: %w", err)
	}

	// Check if job can be cancelled
	if currentStatus == models.StatusOK || currentStatus == models.StatusFailed || currentStatus == models.StatusCancelled {
		return fmt.Errorf("job with status '%s' cannot be cancelled", currentStatus)
	}

	// Set status to cancelling/aborting
	newStatus := models.StatusAborting
	if currentStatus == models.StatusPending {
		// If job is pending, mark it as cancelled immediately
		newStatus = models.StatusCancelled
	}

	const q = `UPDATE jobs SET status = $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`

	result, err := repo.db.ExecContext(ctx, q, newStatus, id)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	// Check if the job actually existed and wasn't already deleted
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("job with id %s not found or already deleted", id)
	}

	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func rowToJob(row scannable) (models.Job, error) {
	var j job

	err := row.Scan(&j.ID, &j.Name, &j.Status, &j.Data, &j.CreatedAt, &j.UpdatedAt, &j.UserID, &j.FailureReason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Job{}, errors.New("job not found")
		}
		return models.Job{}, fmt.Errorf("failed to scan job: %w", err)
	}

	createdAt := time.Unix(int64(j.CreatedAt), 0).UTC()
	updatedAt := time.Unix(int64(j.UpdatedAt), 0).UTC()

	ans := models.Job{
		ID:            j.ID,
		UserID:        j.UserID,
		Name:          j.Name,
		Status:        j.Status,
		Date:          createdAt,
		CreatedAt:     &createdAt,
		UpdatedAt:     &updatedAt,
		FailureReason: j.FailureReason,
	}

	err = json.Unmarshal([]byte(j.Data), &ans.Data)
	if err != nil {
		return models.Job{}, fmt.Errorf("failed to unmarshal job data: %w", err)
	}

	return ans, nil
}

func jobToRow(item *models.Job) (job, error) {
	data, err := json.Marshal(item.Data)
	if err != nil {
		return job{}, fmt.Errorf("failed to marshal job data: %w", err)
	}

	return job{
		ID:            item.ID,
		UserID:        item.UserID,
		Name:          item.Name,
		Status:        item.Status,
		Data:          string(data),
		CreatedAt:     float64(item.Date.Unix()),
		UpdatedAt:     float64(time.Now().UTC().Unix()),
		FailureReason: item.FailureReason,
	}, nil
}

type job struct {
	ID            string
	UserID        string
	Name          string
	Status        string
	Data          string
	CreatedAt     float64
	UpdatedAt     float64
	FailureReason string
}

// Additional methods for soft delete management

// PermanentDelete permanently removes a job and its results from the database
// Use with caution - this operation cannot be undone
func (repo *repository) PermanentDelete(ctx context.Context, id string) error {
	// Start a transaction to ensure atomicity
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// First, delete all results that reference this job
	const deleteResultsQuery = `DELETE FROM results WHERE job_id = $1`
	_, err = tx.ExecContext(ctx, deleteResultsQuery, id)
	if err != nil {
		return fmt.Errorf("failed to delete job results: %w", err)
	}

	// Then permanently delete the job itself
	const deleteJobQuery = `DELETE FROM jobs WHERE id = $1`
	jobResult, err := tx.ExecContext(ctx, deleteJobQuery, id)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	// Check if the job actually existed
	jobRowsAffected, _ := jobResult.RowsAffected()
	if jobRowsAffected == 0 {
		return fmt.Errorf("job with id %s not found", id)
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// RestoreJob restores a soft-deleted job
func (repo *repository) RestoreJob(ctx context.Context, id string) error {
	const q = `UPDATE jobs SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL`

	result, err := repo.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("failed to restore job: %w", err)
	}

	// Check if the job actually existed and was deleted
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("job with id %s not found in deleted jobs", id)
	}

	return nil
}

// GetDeletedJobs retrieves all soft-deleted jobs (for admin purposes)
func (repo *repository) GetDeletedJobs(ctx context.Context, params models.SelectParams) ([]models.Job, error) {
	q := `SELECT id, name, status, data, extract(epoch from created_at), extract(epoch from updated_at), user_id, COALESCE(failure_reason, '') FROM jobs`

	var args []interface{}
	var conditions []string
	var argNum int = 1

	// Only get deleted jobs
	conditions = append(conditions, "deleted_at IS NOT NULL")

	if params.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argNum))
		args = append(args, params.Status)
		argNum++
	}

	if params.UserID != "" {
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argNum))
		args = append(args, params.UserID)
		argNum++
	}

	if len(conditions) > 0 {
		q += " WHERE " + conditions[0]
		for i := 1; i < len(conditions); i++ {
			q += " AND " + conditions[i]
		}
	}

	q += " ORDER BY deleted_at DESC"

	if params.Limit > 0 {
		q += fmt.Sprintf(" LIMIT $%d", argNum)
		args = append(args, params.Limit)
	}

	rows, err := repo.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to select deleted jobs: %w", err)
	}

	defer rows.Close()

	var ans []models.Job

	for rows.Next() {
		job, err := rowToJob(rows)
		if err != nil {
			return nil, err
		}

		ans = append(ans, job)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ans, nil
}

// createSchema ensures the required database schema exists
func createSchema(db *sql.DB) error {
	// Create jobs table - split into individual statements for better error handling
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			data JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			user_id TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create jobs table: %w", err)
	}

	// Create users table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}

	// Add foreign key constraint for jobs to users if not exists
	_, err = db.Exec(`
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.tables 
				WHERE table_schema = 'public' AND table_name = 'users'
			) AND NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'jobs_user_id_fkey'
			) THEN
				ALTER TABLE jobs ADD CONSTRAINT jobs_user_id_fkey
					FOREIGN KEY (user_id) REFERENCES users(id);
			END IF;
		END
		$$;
	`)
	if err != nil {
		return fmt.Errorf("failed to add foreign key constraint: %w", err)
	}

	// Create indexes
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at)`)
	if err != nil {
		return fmt.Errorf("failed to create status index: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_user_id ON jobs(user_id)`)
	if err != nil {
		return fmt.Errorf("failed to create user_id index: %w", err)
	}

	return nil
}
