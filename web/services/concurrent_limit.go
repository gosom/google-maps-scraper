package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
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

// ErrInsufficientBalance is returned when a user's credit balance is too low
// to cover the estimated job cost. Callers should convert this to HTTP 402.
type ErrInsufficientBalance struct {
	Balance        float64
	RequiredCost   float64
	EstimatedCount int
}

func (e ErrInsufficientBalance) Error() string {
	return fmt.Sprintf(
		"insufficient credits: you have %.4f credits but this job requires a minimum of %.4f credits to start (estimated cost for %d places). Please purchase more credits to continue",
		e.Balance, e.RequiredCost, e.EstimatedCount,
	)
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

// JobLimitOpts holds optional parameters for CreateJobWithLimit.
type JobLimitOpts struct {
	// EstimatedCost is the pre-flight cost estimate for the job. When > 0,
	// the user's credit_balance is checked inside the same transaction that
	// locks the user row, preventing a TOCTOU race where two concurrent
	// requests both pass the balance check before either debits.
	EstimatedCost   float64
	EstimatedPlaces int
}

// CreateJobWithLimit checks the user's concurrent job cap and, if not exceeded,
// inserts the job — all within a single serialised transaction.
//
// When opts.EstimatedCost > 0, the credit balance is also verified under the
// same FOR UPDATE lock to prevent concurrent overdraft (TOCTOU race).
//
// Returns ErrConcurrentJobLimitReached when the limit is hit.
// Returns ErrInsufficientBalance when the credit balance is too low.
// The job's CreatedAt/UpdatedAt timestamps are set to now if not already set.
func (s *ConcurrentLimitService) CreateJobWithLimit(ctx context.Context, job *models.Job, opts *JobLimitOpts) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("concurrent_limit: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Lock the user row to serialise concurrent submissions from the same user.
	// Also fetch credit_balance as text to perform an atomic balance check
	// using integer micro-credits, preventing both the TOCTOU race and
	// IEEE 754 float rounding errors in monetary comparisons.
	var limit int
	var balanceStr string
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(max_concurrent_jobs, $1), COALESCE(credit_balance, 0)::text FROM users WHERE id = $2 FOR UPDATE`,
		DefaultMaxConcurrentJobs, job.UserID,
	).Scan(&limit, &balanceStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// User row not yet provisioned — use the safe default.
			limit = DefaultMaxConcurrentJobs
			balanceStr = "0"
		} else {
			return fmt.Errorf("concurrent_limit: get user limit: %w", err)
		}
	}

	// Atomic credit balance check under the FOR UPDATE lock.
	// Parse balance as string -> micro-credits for precise integer comparison.
	if opts != nil && opts.EstimatedCost > 0 {
		balanceFloat, parseErr := strconv.ParseFloat(balanceStr, 64)
		if parseErr != nil {
			return fmt.Errorf("concurrent_limit: parse credit balance: %w", parseErr)
		}
		balanceMicro := int64(math.Round(balanceFloat * models.MicroUnit))
		costMicro := int64(math.Round(opts.EstimatedCost * models.MicroUnit))
		if balanceMicro < costMicro {
			return ErrInsufficientBalance{
				Balance:        balanceFloat,
				RequiredCost:   opts.EstimatedCost,
				EstimatedCount: opts.EstimatedPlaces,
			}
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
