package rqueue

import (
	"context"
	"fmt"

	"github.com/gosom/google-maps-scraper/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// QueueMaintenance is the queue name for maintenance jobs like deletion.
const QueueMaintenance = "maintenance"

// JobDeleteArgs contains the arguments for deleting a scrape job.
type JobDeleteArgs struct {
	JobID int64 `json:"job_id"`
}

// Kind returns the job type identifier.
func (JobDeleteArgs) Kind() string {
	return "job_delete"
}

// InsertOpts returns the insert options for this job type.
func (JobDeleteArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueMaintenance,
		MaxAttempts: 3,
	}
}

// JobDeleteWorker handles job deletion in the background.
type JobDeleteWorker struct {
	river.WorkerDefaults[JobDeleteArgs]
	dbPool *pgxpool.Pool
}

// Work deletes the job and its associated results.
func (w *JobDeleteWorker) Work(ctx context.Context, job *river.Job[JobDeleteArgs]) error {
	jobID := job.Args.JobID

	log.Info("deleting job",
		"job_id", jobID,
		"attempt", job.Attempt,
	)

	// 1. Delete results from scrape_results table
	result, err := w.dbPool.Exec(ctx,
		"DELETE FROM scrape_results WHERE job_id = $1", jobID)
	if err != nil {
		log.Error("failed to delete results", "error", err, "job_id", jobID)
		return fmt.Errorf("failed to delete results: %w", err)
	}

	log.Info("deleted scrape results",
		"job_id", jobID,
		"rows_affected", result.RowsAffected(),
	)

	// 2. Delete the job from river_job table directly
	//    (Can't use JobDelete on self, so use raw SQL)
	result, err = w.dbPool.Exec(ctx,
		"DELETE FROM river_job WHERE id = $1", jobID)
	if err != nil {
		log.Error("failed to delete river job", "error", err, "job_id", jobID)
		return fmt.Errorf("failed to delete job: %w", err)
	}

	log.Info("deleted river job",
		"job_id", jobID,
		"rows_affected", result.RowsAffected(),
	)

	return nil
}
