package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

type repository struct {
	db  *sql.DB
	log *slog.Logger
}

// NewRepository creates a new PostgreSQL implementation of models.JobRepository
func NewRepository(db *sql.DB) (models.JobRepository, error) {
	if err := db.Ping(); err != nil {
		return nil, err
	}

	// We don't create schema here anymore since we're using migrations
	return &repository{
		db:  db,
		log: pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "repository"),
	}, nil
}

// Get retrieves a job by ID (only non-deleted jobs).
// Pass userID="" to bypass ownership check (admin access).
func (repo *repository) Get(ctx context.Context, id string, userID string) (models.Job, error) {
	const q = `SELECT id, name, status, data, extract(epoch from created_at), extract(epoch from updated_at), user_id, COALESCE(failure_reason, ''), COALESCE(source, 'web'), COALESCE(result_count, 0), COALESCE(actual_cost_precise, 0)::text
               FROM jobs WHERE id = $1 AND (user_id = $2 OR $2 = '') AND deleted_at IS NULL`

	row := repo.db.QueryRowContext(ctx, q, id, userID)

	return rowToJob(row)
}

// Create inserts a new job into the database
func (repo *repository) Create(ctx context.Context, job *models.Job) error {
	item, err := jobToRow(job)
	if err != nil {
		return err
	}

	const q = `INSERT INTO jobs (id, name, status, data, created_at, updated_at, user_id, failure_reason, source)
               VALUES ($1, $2, $3, $4, to_timestamp($5), to_timestamp($6), $7, $8, $9)`

	_, err = repo.db.ExecContext(ctx, q, item.ID, item.Name, item.Status, item.Data, item.CreatedAt, item.UpdatedAt, item.UserID, item.FailureReason, item.Source)
	if err != nil {
		repo.log.Error("job_create_failed", slog.String("job_id", job.ID), slog.String("user_id", job.UserID), slog.Any("error", err))
		return fmt.Errorf("failed to create job: %w", err)
	}

	repo.log.Info("job_created", slog.String("job_id", job.ID), slog.String("user_id", job.UserID), slog.String("status", job.Status))
	return nil
}

// Delete marks a job as deleted (soft delete) without removing the valuable results data.
// This preserves all scraped results for potential future business use.
// Pass userID="" to bypass ownership check (admin access).
func (repo *repository) Delete(ctx context.Context, id string, userID string) error {
	const q = `UPDATE jobs SET deleted_at = NOW() WHERE id = $1 AND (user_id = $2 OR $2 = '') AND deleted_at IS NULL`

	result, err := repo.db.ExecContext(ctx, q, id, userID)
	if err != nil {
		repo.log.Error("job_delete_failed", slog.String("job_id", id), slog.String("user_id", userID), slog.Any("error", err))
		return fmt.Errorf("failed to delete job: %w", err)
	}

	// Check if the job actually existed and wasn't already deleted
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("job with id %s not found or already deleted", id)
	}

	repo.log.Info("job_deleted", slog.String("job_id", id), slog.String("user_id", userID))
	return nil
}

// Select finds jobs based on the provided parameters (only non-deleted jobs)
func (repo *repository) Select(ctx context.Context, params models.SelectParams) ([]models.Job, error) {
	// Apply default limit to prevent unbounded queries
	if params.Limit == 0 {
		params.Limit = 1000
	}

	q := `SELECT id, name, status, data, extract(epoch from created_at), extract(epoch from updated_at), user_id, COALESCE(failure_reason, ''), COALESCE(source, 'web'), COALESCE(result_count, 0), COALESCE(actual_cost_precise, 0)::text FROM jobs`

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

	q += " ORDER BY created_at DESC"

	if params.Limit > 0 {
		q += fmt.Sprintf(" LIMIT $%d", argNum)
		args = append(args, params.Limit)
	}

	rows, err := repo.db.QueryContext(ctx, q, args...)
	if err != nil {
		repo.log.Error("job_select_failed", slog.String("user_id", params.UserID), slog.String("status", params.Status), slog.Any("error", err))
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

	repo.log.Debug("job_select_done", slog.String("user_id", params.UserID), slog.String("status", params.Status), slog.Int("count", len(ans)))
	return ans, nil
}

// SelectPaginated returns a page of jobs for a user with optional search,
// along with the total count matching the filter. This is the server-side
// pagination companion to the existing Select method.
func (repo *repository) SelectPaginated(ctx context.Context, params models.PaginatedJobsParams) ([]models.Job, int, error) {
	// --- validate & default ---
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit < 1 {
		params.Limit = 10
	}
	if params.Limit > 100 {
		params.Limit = 100
	}

	// Allowlist for sort columns to prevent SQL injection (CWE-89).
	allowedSort := map[string]string{
		"created_at": "created_at",
		"name":       "name",
		"status":     "status",
		"updated_at": "updated_at",
	}
	sortCol, ok := allowedSort[params.Sort]
	if !ok {
		sortCol = "created_at"
	}
	orderDir := "DESC"
	if params.Order == "asc" {
		orderDir = "ASC"
	}

	// --- build WHERE clause ---
	var conditions []string
	var args []interface{}
	argNum := 1

	conditions = append(conditions, "deleted_at IS NULL")

	if params.UserID != "" {
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argNum))
		args = append(args, params.UserID)
		argNum++
	}

	if params.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(LOWER(name) LIKE $%d OR LOWER(status) LIKE $%d)", argNum, argNum))
		args = append(args, "%"+strings.ToLower(params.Search)+"%")
		argNum++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + conditions[0]
		for i := 1; i < len(conditions); i++ {
			whereClause += " AND " + conditions[i]
		}
	}

	// --- COUNT query ---
	countQ := "SELECT COUNT(*) FROM jobs" + whereClause
	var total int
	if err := repo.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		repo.log.Error("job_select_paginated_count_failed", slog.String("user_id", params.UserID), slog.Any("error", err))
		return nil, 0, fmt.Errorf("failed to count jobs: %w", err)
	}

	// --- SELECT query ---
	selectQ := `SELECT id, name, status, data, extract(epoch from created_at), extract(epoch from updated_at), user_id, COALESCE(failure_reason, ''), COALESCE(source, 'web'), COALESCE(result_count, 0), COALESCE(actual_cost_precise, 0)::text FROM jobs` +
		whereClause +
		fmt.Sprintf(" ORDER BY %s %s", sortCol, orderDir)

	offset := (params.Page - 1) * params.Limit
	selectQ += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argNum, argNum+1)
	selectArgs := append(append([]interface{}{}, args...), params.Limit, offset)

	rows, err := repo.db.QueryContext(ctx, selectQ, selectArgs...)
	if err != nil {
		repo.log.Error("job_select_paginated_failed", slog.String("user_id", params.UserID), slog.Any("error", err))
		return nil, 0, fmt.Errorf("failed to select paginated jobs: %w", err)
	}
	defer rows.Close()

	var jobs []models.Job
	for rows.Next() {
		job, err := rowToJob(rows)
		if err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	repo.log.Debug("job_select_paginated_done",
		slog.String("user_id", params.UserID),
		slog.Int("page", params.Page),
		slog.Int("limit", params.Limit),
		slog.Int("total", total),
		slog.Int("returned", len(jobs)),
	)
	return jobs, total, nil
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
		repo.log.Error("job_update_failed", slog.String("job_id", job.ID), slog.String("status", job.Status), slog.Any("error", err))
		return fmt.Errorf("failed to update job: %w", err)
	}

	repo.log.Debug("job_updated", slog.String("job_id", job.ID), slog.String("status", job.Status))
	return nil
}

// Cancel marks a job for cancellation.
// Pass userID="" to bypass ownership check (admin access).
func (repo *repository) Cancel(ctx context.Context, id string, userID string) error {
	// First check if job exists and is in a cancellable state
	var currentStatus string
	err := repo.db.QueryRowContext(ctx, "SELECT status FROM jobs WHERE id = $1 AND (user_id = $2 OR $2 = '') AND deleted_at IS NULL", id, userID).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("job with id %s not found", id)
		}
		return fmt.Errorf("failed to get job status: %w", err)
	}

	// Check if job can be cancelled
	if currentStatus == models.StatusOK || currentStatus == models.StatusFailed || currentStatus == models.StatusCancelled {
		repo.log.Warn("job_cancel_invalid_status", slog.String("job_id", id), slog.String("user_id", userID), slog.String("status", currentStatus))
		return fmt.Errorf("job with status '%s' cannot be cancelled", currentStatus)
	}

	// Set status directly to cancelled (skip aborting intermediate state)
	newStatus := models.StatusCancelled

	const q = `UPDATE jobs SET status = $1, updated_at = NOW() WHERE id = $2 AND (user_id = $3 OR $3 = '') AND deleted_at IS NULL`

	result, err := repo.db.ExecContext(ctx, q, newStatus, id, userID)
	if err != nil {
		repo.log.Error("job_cancel_failed", slog.String("job_id", id), slog.String("user_id", userID), slog.Any("error", err))
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	// Check if the job actually existed and wasn't already deleted
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("job with id %s not found or already deleted", id)
	}

	repo.log.Info("job_cancelled", slog.String("job_id", id), slog.String("user_id", userID))
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func rowToJob(row scannable) (models.Job, error) {
	var j job

	err := row.Scan(&j.ID, &j.Name, &j.Status, &j.Data, &j.CreatedAt, &j.UpdatedAt, &j.UserID, &j.FailureReason, &j.Source, &j.ResultCount, &j.TotalCost)
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
		Source:        j.Source,
		ResultCount:   j.ResultCount,
		TotalCost:     j.TotalCost,
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
		Source:        item.Source,
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
	Source        string
	ResultCount   int
	TotalCost     string
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
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			repo.log.Error("rollback_failed", slog.Any("error", rbErr))
		}
	}()

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
		repo.log.Error("job_permanent_delete_commit_failed", slog.String("job_id", id), slog.Any("error", err))
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	repo.log.Info("job_permanently_deleted", slog.String("job_id", id))
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
	// Apply default limit to prevent unbounded queries
	if params.Limit == 0 {
		params.Limit = 1000
	}

	q := `SELECT id, name, status, data, extract(epoch from created_at), extract(epoch from updated_at), user_id, COALESCE(failure_reason, ''), COALESCE(source, 'web'), COALESCE(result_count, 0), COALESCE(actual_cost_precise, 0)::text FROM jobs`

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
