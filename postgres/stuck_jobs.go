package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
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

	// Update each stuck job individually so we can log per-job results.
	const updateQ = `
		UPDATE jobs
		SET status = 'failed',
		    failure_reason = $1,
		    updated_at = NOW()
		WHERE id = $2
		  AND status = 'working'
		  AND deleted_at IS NULL`

	for _, j := range stuck {
		result, err := db.ExecContext(ctx, updateQ, failureReason, j.id)
		if err != nil {
			log.Error("stuck_job_reaper_update_failed",
				slog.String("job_id", j.id),
				slog.Any("error", err),
			)
			continue
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			// Job status changed between SELECT and UPDATE — skip silently.
			continue
		}

		log.Warn("stuck_job_auto_failed",
			slog.String("job_id", j.id),
			slog.String("user_id", j.userID),
			slog.Time("started_at", j.createdAt),
		)
	}
}
