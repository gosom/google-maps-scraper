package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// DefaultMaxConcurrentJobs is the cap applied when no per-user override exists.
const DefaultMaxConcurrentJobs = 2

// ErrConcurrentJobLimitReached is returned when a user has already hit their
// concurrent job cap. Callers should convert this to HTTP 429.
type ErrConcurrentJobLimitReached struct {
	Limit int
}

func (e ErrConcurrentJobLimitReached) Error() string {
	return fmt.Sprintf("concurrent job limit reached (limit: %d)", e.Limit)
}

// ConcurrentLimitService enforces per-user concurrent job limits.
// The check and insert are executed inside a single transaction, with the
// user row locked FOR UPDATE to prevent race conditions between simultaneous
// job submission requests from the same user.
type ConcurrentLimitService struct {
	db *sql.DB
}

// NewConcurrentLimitService constructs a ConcurrentLimitService.
func NewConcurrentLimitService(db *sql.DB) *ConcurrentLimitService {
	return &ConcurrentLimitService{db: db}
}

// CreateJobWithLimit checks the user's concurrent job cap and, if not exceeded,
// inserts the job — all within a single serialised transaction.
//
// Returns ErrConcurrentJobLimitReached when the limit is hit.
// The job's CreatedAt/UpdatedAt timestamps are set to now if not already set.
func (s *ConcurrentLimitService) CreateJobWithLimit(ctx context.Context, job *models.Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("concurrent_limit: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Lock the user row to serialise concurrent submissions from the same user.
	// COALESCE falls back to the compile-time default if the column is NULL
	// (shouldn't happen with NOT NULL DEFAULT 2, but defensive).
	var limit int
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(max_concurrent_jobs, $1) FROM users WHERE id = $2 FOR UPDATE`,
		DefaultMaxConcurrentJobs, job.UserID,
	).Scan(&limit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// User row not yet provisioned — use the safe default.
			limit = DefaultMaxConcurrentJobs
		} else {
			return fmt.Errorf("concurrent_limit: get user limit: %w", err)
		}
	}

	// Count jobs that are actively consuming worker capacity.
	// Zombie jobs (stuck in 'working' > 5 min) still count — callers must
	// handle zombie detection separately (see stuck_jobs.go).
	var count int
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs
		 WHERE user_id = $1
		   AND status IN ('pending', 'working')
		   AND deleted_at IS NULL`,
		job.UserID,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("concurrent_limit: count active jobs: %w", err)
	}

	if count >= limit {
		return ErrConcurrentJobLimitReached{Limit: limit}
	}

	// Insert the job within the same transaction.
	data, err := json.Marshal(job.Data)
	if err != nil {
		return fmt.Errorf("concurrent_limit: marshal job data: %w", err)
	}

	now := time.Now().UTC().Unix()
	createdAt := float64(job.Date.Unix())
	if createdAt <= 0 {
		createdAt = float64(now)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO jobs (id, name, status, data, created_at, updated_at, user_id, failure_reason)
		 VALUES ($1, $2, $3, $4, to_timestamp($5), to_timestamp($6), $7, $8)`,
		job.ID, job.Name, job.Status, string(data),
		createdAt, float64(now),
		job.UserID, job.FailureReason,
	)
	if err != nil {
		return fmt.Errorf("concurrent_limit: insert job: %w", err)
	}

	return tx.Commit()
}
