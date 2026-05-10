package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/shopspring/decimal"
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

// ErrInsufficientBalance is returned when a user's available credit balance
// (credit_balance − credit_held_precise) is too low to cover the estimated
// job cost. Callers should convert this to HTTP 402.
//
// Error() returns a low-cardinality string ("insufficient available
// balance") so log aggregators (Loki, Datadog) can group these
// occurrences together regardless of the specific numbers. The
// user-facing message is rendered at the HTTP handler from the typed
// fields below; that's the single place that knows about i18n,
// formatting tone, and the "Please purchase more" call to action.
//
// Field names are kept (Balance / RequiredCost / EstimatedCount) for
// backwards-compat with any caller that already uses errors.As to read
// them. Balance now carries AVAILABLE balance (i.e. balance minus
// holds) — the figure the user can act on, not the gross wallet.
type ErrInsufficientBalance struct {
	Balance        float64
	RequiredCost   float64
	EstimatedCount int
}

// insufficientBalanceMessage is the stable low-cardinality string used for
// log grouping. Exported as a const so the HTTP handler and tests can
// detect it without depending on the formatting of UserMessage.
const insufficientBalanceMessage = "insufficient available balance"

func (e ErrInsufficientBalance) Error() string {
	return insufficientBalanceMessage
}

// UserMessage renders the human-readable message for the 402 response
// body. Constructed from the typed fields so the wire string can change
// (i18n, tone) without affecting log grouping on Error().
func (e ErrInsufficientBalance) UserMessage() string {
	return fmt.Sprintf(
		"Insufficient credits: %.4f available, this job requires %.4f credits (estimated for %d places). Please purchase more credits to continue.",
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

// CreateJobWithLimit checks the user's concurrent job cap, reserves the
// estimated cost as a credit hold, persists the estimate on the job row,
// and inserts the job — all within a single transaction with the user row
// locked FOR UPDATE.
//
// Reservation semantics (added 2026-05-10 in response to the
// architectural review):
//   - available = credit_balance - credit_held_precise
//   - submission requires available ≥ EstimatedCost
//   - on success the same transaction increments credit_held_precise by
//     the estimate and writes that amount to jobs.estimated_cost_precise
//   - the hold is released at end-of-job (webrunner.go), regardless of
//     whether ChargeAllJobEvents charged less, equal, or more than the
//     estimate. Released-but-not-yet-charged credits become spendable
//     for the user's NEXT submission.
//
// Why a hold and not a debit: a debit would change the user-visible
// credit balance the moment they click Run, which is confusing because
// the actual charge happens at job end based on real scrape output.
// Holds let us keep credit_balance as "money you have spent" while
// preventing two concurrent jobs from each passing the gate against the
// same dollar.
//
// Returns ErrConcurrentJobLimitReached when the limit is hit.
// Returns ErrInsufficientBalance when (balance - held) < EstimatedCost.
// The job's CreatedAt/UpdatedAt timestamps are set to now if not set.
func (s *ConcurrentLimitService) CreateJobWithLimit(ctx context.Context, job *models.Job, opts *JobLimitOpts) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("concurrent_limit: begin tx: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			// Transaction rollback failed for a reason other than already-committed.
			_ = rbErr // logged at caller if needed
		}
	}()

	// Lock the user row. Read both balance and held so we can compute the
	// available balance under the lock — same transactional shape as the
	// existing balance check, just one extra column.
	var limit int
	var balanceStr, heldStr string
	err = tx.QueryRowContext(ctx,
		`SELECT
		     COALESCE(max_concurrent_jobs, $1),
		     COALESCE(credit_balance, 0)::text,
		     COALESCE(credit_held_precise, 0)::text
		 FROM users WHERE id = $2 FOR UPDATE`,
		DefaultMaxConcurrentJobs, job.UserID,
	).Scan(&limit, &balanceStr, &heldStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// User row not yet provisioned — use the safe defaults.
			// EstimatedCost > 0 will fail the affordability check below,
			// so this branch only matters for cost-less paths.
			limit = DefaultMaxConcurrentJobs
			balanceStr = "0"
			heldStr = "0"
		} else {
			return fmt.Errorf("concurrent_limit: get user state: %w", err)
		}
	}

	// Atomic affordability check under the FOR UPDATE lock.
	// Use Total (not MinTotal) — the same value the user saw on the
	// frontend preview and the same value we will persist as the quote.
	// Parsing as decimal -> micro-credits keeps the comparison exact.
	if opts != nil && opts.EstimatedCost > 0 {
		balanceDec, parseErr := decimal.NewFromString(balanceStr)
		if parseErr != nil {
			return fmt.Errorf("concurrent_limit: parse credit balance: %w", parseErr)
		}
		heldDec, parseErr := decimal.NewFromString(heldStr)
		if parseErr != nil {
			return fmt.Errorf("concurrent_limit: parse credit held: %w", parseErr)
		}
		availableMicro := balanceDec.Sub(heldDec).Mul(decimal.NewFromInt(models.MicroUnit)).IntPart()
		costMicro := decimal.NewFromFloat(opts.EstimatedCost).Mul(decimal.NewFromInt(models.MicroUnit)).IntPart()
		if availableMicro < costMicro {
			availableFloat, _ := balanceDec.Sub(heldDec).Float64()
			return ErrInsufficientBalance{
				Balance:        availableFloat,
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

	// Insert the job within the same transaction. estimated_cost_precise
	// is the persisted quote — the contract we showed the user. It is
	// READ later by webrunner.go to release the hold and by analytics to
	// measure quote-vs-actual variance.
	data, err := json.Marshal(job.Data)
	if err != nil {
		return fmt.Errorf("concurrent_limit: marshal job data: %w", err)
	}

	now := time.Now().UTC().Unix()
	createdAt := float64(job.Date.Unix())
	if createdAt <= 0 {
		createdAt = float64(now)
	}

	estimatedCost := 0.0
	if opts != nil {
		estimatedCost = opts.EstimatedCost
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO jobs (id, name, status, data, created_at, updated_at, user_id, failure_reason, source, estimated_cost_precise)
		 VALUES ($1, $2, $3, $4, to_timestamp($5), to_timestamp($6), $7, $8, $9, $10::numeric)`,
		job.ID, job.Name, job.Status, string(data),
		createdAt, float64(now),
		job.UserID, job.FailureReason, job.Source,
		estimatedCost,
	)
	if err != nil {
		return fmt.Errorf("concurrent_limit: insert job: %w", err)
	}

	// Reserve the estimate on the user. The CHECK constraint
	// (credit_held_precise <= credit_balance) is a defence-in-depth: the
	// availability check above should already guarantee it, but if it
	// trips we get a clear DB error rather than negative available.
	if opts != nil && opts.EstimatedCost > 0 {
		_, err = tx.ExecContext(ctx,
			`UPDATE users
			 SET credit_held_precise = credit_held_precise + $1::numeric
			 WHERE id = $2`,
			opts.EstimatedCost, job.UserID,
		)
		if err != nil {
			return fmt.Errorf("concurrent_limit: reserve credit hold: %w", err)
		}
	}

	return tx.Commit()
}

// ReleaseHold decrements the user's credit_held_precise by the given amount.
// Called at job end (success or failure) to make the reserved credits
// available for the user's next submission.
//
// Idempotent in spirit: callers should pass the EXACT amount that was
// originally reserved (i.e. the persisted jobs.estimated_cost_precise).
// Releasing more than was held will trip the
// credit_held_precise_non_negative CHECK and return an error — that is
// the desired loud failure, not silent corruption.
//
// This routine intentionally does NOT touch credit_balance; the actual
// charge happens through ChargeJobStart / ChargeAllJobEvents which
// already debit credit_balance against scraped events. Hold and charge
// are independent ledgers that meet only at "available =
// balance - held".
func (s *ConcurrentLimitService) ReleaseHold(ctx context.Context, userID string, amount float64) error {
	if amount <= 0 {
		return nil
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users
		 SET credit_held_precise = credit_held_precise - $1::numeric
		 WHERE id = $2`,
		amount, userID,
	)
	if err != nil {
		return fmt.Errorf("concurrent_limit: release hold: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("concurrent_limit: release hold: user %q not found", userID)
	}
	return nil
}
