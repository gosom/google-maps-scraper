package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// RunStuckJobReaper finds jobs stuck in 'working' status beyond timeoutHours
// and marks them as failed. Safe to run as a goroutine.
//
// It ticks every checkInterval, queries for jobs whose updated_at has not
// changed in more than timeoutHours hours, and sets their status to 'failed'
// with a descriptive failure_reason. Each auto-failed job emits a slog.Warn.
// The loop stops when ctx is cancelled. Any panic is recovered and logged.
func RunStuckJobReaper(ctx context.Context, db *sql.DB, log *slog.Logger, checkInterval time.Duration, timeoutHours int) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	log.Info("stuck_job_reaper_started",
		slog.Duration("check_interval", checkInterval),
		slog.Int("timeout_hours", timeoutHours),
	)

	for {
		select {
		case <-ctx.Done():
			log.Info("stuck_job_reaper_stopped")
			return
		case <-ticker.C:
			runReaperTick(ctx, db, log, timeoutHours)
		}
	}
}

// runReaperTick performs a single reap cycle inside a panic-safe wrapper.
func runReaperTick(ctx context.Context, db *sql.DB, log *slog.Logger, timeoutHours int) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("stuck_job_reaper_panic", slog.Any("panic", r))
		}
	}()

	failureReason := fmt.Sprintf("job timed out after %d hours", timeoutHours)

	// Find all working jobs whose updated_at is older than timeoutHours.
	const selectQ = `
		SELECT id, user_id, created_at
		FROM jobs
		WHERE status = 'working'
		  AND updated_at < NOW() - INTERVAL '1 hour' * $1
		  AND deleted_at IS NULL`

	rows, err := db.QueryContext(ctx, selectQ, timeoutHours)
	if err != nil {
		log.Error("stuck_job_reaper_query_failed", slog.Any("error", err))
		return
	}
	defer rows.Close()

	type stuckJob struct {
		id        string
		userID    string
		createdAt time.Time
	}

	var stuck []stuckJob

	for rows.Next() {
		var j stuckJob
		if err := rows.Scan(&j.id, &j.userID, &j.createdAt); err != nil {
			log.Error("stuck_job_reaper_scan_failed", slog.Any("error", err))
			continue
		}
		stuck = append(stuck, j)
	}

	if err := rows.Err(); err != nil {
		log.Error("stuck_job_reaper_rows_error", slog.Any("error", err))
		return
	}

	if len(stuck) == 0 {
		return
	}

	// Collect IDs for a single batch UPDATE — O(1) round trips instead of O(n).
	ids := make([]string, len(stuck))
	for i, j := range stuck {
		ids[i] = j.id
	}

	// Reap stuck jobs AND release their credit holds in a single statement
	// via two data-modifying CTEs. Both CTEs run inside the same implicit
	// transaction and see the same snapshot.
	//
	//   reaped     — flips status=working → failed for each stuck job and
	//                exposes (id, user_id, estimated_cost_precise).
	//   per_user   — sums the held estimate per user_id (a user can have
	//                multiple stuck jobs, e.g. after a long backend outage).
	//   released   — decrements credit_held_precise per user by the sum.
	//
	// Why a CTE rather than two app-side queries: makes reaping atomic with
	// the hold release, so a partial failure either marks all jobs failed
	// AND releases their holds, or leaves the world unchanged. A previous
	// version of this file marked jobs failed but did NOT touch holds,
	// silently leaking credit_held_precise on every worker crash. The PR
	// thread that introduced the holds (#66 / migration 000036) called
	// this leak out — see the architectural-review section on bounded
	// failure modes.
	//
	// COALESCE(estimated_cost_precise, 0) covers (a) jobs created before
	// migration 000036 (column default 0), (b) admin jobs (bypassed
	// CreateJobWithLimit, no hold reserved), and (c) any future code path
	// that inserts a job without an estimate. Those release nothing — the
	// same idempotency contract that webrunner.releaseHoldAndLogBilling
	// follows for non-admin success/failure paths.
	const updateQ = `
		WITH reaped AS (
			UPDATE jobs
			SET status = 'failed',
			    failure_reason = $1,
			    updated_at = NOW()
			WHERE id = ANY($2)
			  AND status = 'working'
			  AND deleted_at IS NULL
			RETURNING id, user_id, COALESCE(estimated_cost_precise, 0) AS reserved
		),
		per_user AS (
			SELECT user_id, SUM(reserved) AS total_reserved
			FROM reaped
			WHERE reserved > 0
			GROUP BY user_id
		),
		released AS (
			UPDATE users u
			SET credit_held_precise = u.credit_held_precise - p.total_reserved
			FROM per_user p
			WHERE u.id = p.user_id
			RETURNING u.id
		)
		SELECT id FROM reaped`

	updatedRows, err := db.QueryContext(ctx, updateQ, failureReason, pq.Array(ids))
	if err != nil {
		log.Error("stuck_job_reaper_update_failed", slog.Any("error", err))
		return
	}
	defer updatedRows.Close()

	updatedSet := make(map[string]struct{})
	for updatedRows.Next() {
		var updatedID string
		if err := updatedRows.Scan(&updatedID); err != nil {
			log.Error("stuck_job_reaper_scan_updated_failed", slog.Any("error", err))
			continue
		}
		updatedSet[updatedID] = struct{}{}
	}

	if err := updatedRows.Err(); err != nil {
		log.Error("stuck_job_reaper_rows_error", slog.Any("error", err))
		return
	}

	// Log each updated job with its original metadata.
	for _, j := range stuck {
		if _, ok := updatedSet[j.id]; ok {
			log.Warn("stuck_job_auto_failed",
				slog.String("job_id", j.id),
				slog.String("user_id", j.userID),
				slog.Time("started_at", j.createdAt),
			)
		}
	}
}
