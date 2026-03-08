package rqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/scraper"
	"github.com/gosom/scrapemate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/speps/go-hashids/v2"

	"github.com/gosom/google-maps-scraper/log"
)

var hashIDCodec *hashids.HashID

func init() {
	salt := os.Getenv("HASHID_SALT")
	if salt == "" {
		salt = "gmaps-scraper-pro-default-salt"
	}

	hd := hashids.NewData()
	hd.Salt = salt
	hd.MinLength = 32
	hashIDCodec, _ = hashids.NewWithData(hd)
}

func encodeJobID(id int64) string {
	encoded, _ := hashIDCodec.EncodeInt64([]int64{id})
	return encoded
}

func decodeJobID(hash string) (int64, error) {
	decoded, err := hashIDCodec.DecodeInt64WithError(hash)
	if err != nil {
		return 0, err
	}

	if len(decoded) == 0 {
		return 0, fmt.Errorf("invalid job id")
	}

	return decoded[0], nil
}

const (
	jobStatusRunning         = "running"
	jobStatusCompleted       = "completed"
	maxScrapeTimeout         = 5 * time.Minute
	defaultScrapeTimeoutSecs = 300
	retryPromoteMaxBatch     = 50
	flushWaitWarnThreshold   = 20 * time.Second
	jobRuntimeWarnThreshold  = 4*time.Minute + 30*time.Second
)

type ScrapeWatchdogMetrics struct {
	FlushWaitWarnTotal   int64
	LongRuntimeWarnTotal int64
}

var globalScrapeWatchdog struct {
	flushWaitWarnTotal   atomic.Int64
	longRuntimeWarnTotal atomic.Int64
}

// GetScrapeWatchdogMetrics returns cumulative watchdog counters for scrape jobs.
func GetScrapeWatchdogMetrics() ScrapeWatchdogMetrics {
	return ScrapeWatchdogMetrics{
		FlushWaitWarnTotal:   globalScrapeWatchdog.flushWaitWarnTotal.Load(),
		LongRuntimeWarnTotal: globalScrapeWatchdog.longRuntimeWarnTotal.Load(),
	}
}

type ScrapeJobArgs struct {
	Keyword        string  `json:"keyword"`
	Lang           string  `json:"lang"`
	MaxDepth       int     `json:"max_depth"`
	Email          bool    `json:"email"`
	GeoCoordinates string  `json:"geo_coordinates"`
	Zoom           int     `json:"zoom"`
	Radius         float64 `json:"radius"`
	FastMode       bool    `json:"fast_mode"`
	ExtraReviews   bool    `json:"extra_reviews"`
	TimeoutSecs    int     `json:"timeout"` // timeout in seconds
}

func (ScrapeJobArgs) Kind() string {
	return "scrape"
}

func (ScrapeJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 3,
	}
}

// ScrapeManager is the interface that ScrapeWorker uses to interact with
// the scraper lifecycle and result collection.
type ScrapeManager interface {
	JobDone()
	SubmitJob(ctx context.Context, job scrapemate.IJob) error
	RegisterJob(jobID string, riverJobID int64, keyword string) <-chan scraper.FlushResult
	MarkDone(jobID string)
	ForceFlush(jobID string)
}

type ScrapeWorker struct {
	river.WorkerDefaults[ScrapeJobArgs]
	Manager ScrapeManager
}

const flushResultWaitGrace = 90 * time.Second

// NextRetryAt returns a 2-minute fallback retry delay. In practice, the retry
// promoter goroutine periodically calls JobRetry() much sooner.
func (w *ScrapeWorker) NextRetryAt(_ *river.Job[ScrapeJobArgs]) time.Time {
	return time.Now().Add(2 * time.Minute)
}

// Timeout controls River's job context deadline for scrape jobs.
// Keep it above Work()'s internal timeout to allow force-flush and result save.
func (w *ScrapeWorker) Timeout(job *river.Job[ScrapeJobArgs]) time.Duration {
	timeout := maxScrapeTimeout
	if job != nil {
		timeout = effectiveScrapeTimeout(job.Args.TimeoutSecs)
	}

	return timeout + 2*time.Minute
}

func (w *ScrapeWorker) Work(ctx context.Context, job *river.Job[ScrapeJobArgs]) error {
	defer w.Manager.JobDone()

	jobStart := time.Now()
	defer func() {
		runtime := time.Since(jobStart)
		if runtime > jobRuntimeWarnThreshold {
			globalScrapeWatchdog.longRuntimeWarnTotal.Add(1)
			log.Warn("scrape job exceeded watchdog runtime",
				"job_id", job.ID,
				"runtime_ms", runtime.Milliseconds(),
				"threshold_ms", jobRuntimeWarnThreshold.Milliseconds(),
			)
		}
	}()

	args := job.Args
	jobID := strconv.FormatInt(job.ID, 10)

	timeout := effectiveScrapeTimeout(args.TimeoutSecs)

	// Register job — CentralWriter accumulates results and flushes to DB
	completionCh := w.Manager.RegisterJob(jobID, job.ID, args.Keyword)
	flushWaitStart := time.Now()

	// Create exit monitor to track job completion
	exitMon := exiter.New()
	exitMon.SetSeedCount(1)
	exitMon.SetCancelFunc(func() { w.Manager.MarkDone(jobID) })

	// Create the appropriate scrape job based on mode
	var scrapeJob scrapemate.IJob

	if args.FastMode {
		params := &gmaps.MapSearchParams{
			Query: args.Keyword,
			Hl:    args.Lang,
		}

		if args.GeoCoordinates != "" {
			lat, lon := parseGeoCoordinates(args.GeoCoordinates)
			params.Location = gmaps.MapLocation{
				Lat:     lat,
				Lon:     lon,
				ZoomLvl: float64(args.Zoom),
				Radius:  args.Radius,
			}
		}

		searchJob := gmaps.NewSearchJob(params,
			gmaps.WithSearchJobExitMonitor(exitMon),
			gmaps.WithSearchJobWriterManagedCompletion(),
		)
		searchJob.ID = jobID

		scrapeJob = searchJob
	} else {
		maxDepth := args.MaxDepth
		if maxDepth == 0 {
			maxDepth = 10
		}

		opts := []gmaps.GmapJobOptions{
			gmaps.WithExitMonitor(exitMon),
			gmaps.WithWriterManagedCompletion(),
		}

		if args.ExtraReviews {
			opts = append(opts, gmaps.WithExtraReviews())
		}

		scrapeJob = gmaps.NewGmapJob(
			jobID,
			args.Lang,
			args.Keyword,
			maxDepth,
			args.Email,
			args.GeoCoordinates,
			args.Zoom,
			opts...,
		)
	}

	// Submit job to the provider channel
	if err := w.Manager.SubmitJob(ctx, scrapeJob); err != nil {
		w.Manager.ForceFlush(jobID) // clean up the registered trackedJob

		if _, waitErr := waitForFlushResult(completionCh, flushResultWaitGrace); waitErr != nil {
			log.Warn("failed waiting for flush result after submit failure",
				"job_id", job.ID,
				"wait_error", waitErr,
			)
		}

		return fmt.Errorf("failed to submit job: %w", err)
	}

	// Start exit monitor in background
	exitCtx, exitCancel := context.WithCancel(ctx)
	defer exitCancel()

	go exitMon.Run(exitCtx)

	// Wait for CentralWriter flush, timeout, or context cancellation
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-completionCh:
		observeFlushWait(job.ID, time.Since(flushWaitStart))
		log.Debug("job flush completed",
			"job_id", job.ID,
			"result_count", result.ResultCount,
			"wait_ms", time.Since(flushWaitStart).Milliseconds(),
		)

		if result.Err != nil {
			return result.Err
		}

		return nil
	case <-timer.C:
		exitCancel()
		w.Manager.ForceFlush(jobID)

		result, err := waitForFlushResult(completionCh, flushResultWaitGrace)
		if err != nil {
			observeFlushWait(job.ID, time.Since(flushWaitStart))

			return err
		}

		observeFlushWait(job.ID, time.Since(flushWaitStart))

		if result.Err != nil {
			return result.Err
		}

		if result.ResultCount == 0 {
			log.Warn("job timed out with no results",
				"keyword", args.Keyword,
				"attempt", job.Attempt,
				"job_id", job.ID,
			)

			return fmt.Errorf("job timed out with no results")
		}

		log.Info("job timed out but saved partial results",
			"keyword", args.Keyword,
			"result_count", result.ResultCount,
			"job_id", job.ID,
		)

		return nil
	case <-ctx.Done():
		exitCancel()
		w.Manager.ForceFlush(jobID)

		result, err := waitForFlushResult(completionCh, flushResultWaitGrace)
		if err != nil {
			observeFlushWait(job.ID, time.Since(flushWaitStart))

			return err
		}

		observeFlushWait(job.ID, time.Since(flushWaitStart))

		if result.Err != nil {
			return result.Err
		}

		// River can cancel the job context at timeout before our local timer branch
		// is selected. If we already persisted partial/full results, treat it as success
		// to avoid unnecessary retries and duplicate work.
		if ctx.Err() == context.DeadlineExceeded {
			if result.ResultCount > 0 {
				log.Info("job context expired but saved results",
					"keyword", args.Keyword,
					"result_count", result.ResultCount,
					"job_id", job.ID,
				)

				return nil
			}

			return fmt.Errorf("job timed out with no results")
		}

		return ctx.Err()
	}
}

func waitForFlushResult(ch <-chan scraper.FlushResult, grace time.Duration) (scraper.FlushResult, error) {
	if grace <= 0 {
		grace = flushResultWaitGrace
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case result := <-ch:
		return result, nil
	case <-timer.C:
		return scraper.FlushResult{}, fmt.Errorf("timed out waiting for flush result")
	}
}

func observeFlushWait(jobID int64, wait time.Duration) {
	if wait <= flushWaitWarnThreshold {
		return
	}

	globalScrapeWatchdog.flushWaitWarnTotal.Add(1)
	log.Warn("flush wait exceeded watchdog threshold",
		"job_id", jobID,
		"wait_ms", wait.Milliseconds(),
		"threshold_ms", flushWaitWarnThreshold.Milliseconds(),
	)
}

func effectiveScrapeTimeout(timeoutSecs int) time.Duration {
	if timeoutSecs <= 0 {
		return maxScrapeTimeout
	}

	timeout := time.Duration(timeoutSecs) * time.Second
	if timeout > maxScrapeTimeout {
		return maxScrapeTimeout
	}

	return timeout
}

// parseGeoCoordinates parses a "lat,lon" string into separate float64 values.
func parseGeoCoordinates(coords string) (lat, lon float64) {
	parts := strings.Split(coords, ",")
	if len(parts) != 2 {
		return 0, 0
	}

	lat, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, _ = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)

	return lat, lon
}

type Client struct {
	riverClient *river.Client[pgx.Tx]
	dbPool      *pgxpool.Pool
}

// NewClient creates a new Client for the API server.
// It processes maintenance jobs (worker provisioning/deletion) and can insert scrape jobs.
// encryptionKey is needed to decrypt secrets from app_config at job execution time.
func NewClient(dbPool *pgxpool.Pool, encryptionKey []byte) (*Client, error) {
	logger := log.With("component", "river-server")

	workers := river.NewWorkers()
	// Register ScrapeWorker so Insert validates the "scrape" kind.
	// The server never processes scrape jobs (only QueueMaintenance runs here).
	river.AddWorker(workers, &ScrapeWorker{})
	river.AddWorker(workers, &JobDeleteWorker{
		dbPool: dbPool,
	})
	river.AddWorker(workers, &WorkerProvisionWorker{
		dbPool:        dbPool,
		encryptionKey: encryptionKey,
	})
	river.AddWorker(workers, &WorkerDeleteWorker{
		dbPool:        dbPool,
		encryptionKey: encryptionKey,
	})
	river.AddWorker(workers, &WorkerHealthCheckWorker{
		dbPool:        dbPool,
		encryptionKey: encryptionKey,
	})
	river.AddWorker(workers, &CleanupWorker{
		dbPool: dbPool,
	})

	periodicJobs := []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(30*time.Second),
			func() (river.JobArgs, *river.InsertOpts) {
				return WorkerHealthCheckArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(1*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return CleanupArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		),
	}

	riverClient, err := river.NewClient(riverpgxv5.New(dbPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			QueueMaintenance: {MaxWorkers: 2},
		},
		Workers:              workers,
		PeriodicJobs:         periodicJobs,
		Logger:               logger,
		RescueStuckJobsAfter: 20 * time.Minute,
	})
	if err != nil {
		return nil, err
	}

	return &Client{
		riverClient: riverClient,
		dbPool:      dbPool,
	}, nil
}

// NewWorkerClient creates a new Client for worker mode.
// This client only processes scrape jobs. Maintenance jobs (worker provisioning,
// deletion) are handled by the server's River client.
// Worker-mode concurrency is intentionally fixed to one River worker per process.
func NewWorkerClient(dbPool *pgxpool.Pool, manager ScrapeManager) (*Client, error) {
	logger := log.With("component", "river-worker")

	workers := river.NewWorkers()
	river.AddWorker(workers, &ScrapeWorker{
		Manager: manager,
	})

	riverClient, err := river.NewClient(riverpgxv5.New(dbPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 1},
		},
		Workers:              workers,
		Logger:               logger,
		JobTimeout:           maxScrapeTimeout + 2*time.Minute,
		RescueStuckJobsAfter: 20 * time.Minute,
	})
	if err != nil {
		return nil, err
	}

	return &Client{
		riverClient: riverClient,
		dbPool:      dbPool,
	}, nil
}

func (c *Client) Start(ctx context.Context) error {
	return c.riverClient.Start(ctx)
}

// StartRetryPromoter runs a background goroutine that periodically promotes
// retryable scrape jobs for immediate retry.
func (c *Client) StartRetryPromoter(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.promoteRetryableJobs(ctx, 1)
			}
		}
	}()
}

func (c *Client) promoteRetryableJobs(ctx context.Context, limit int) {
	if limit <= 0 {
		return
	}

	if limit > retryPromoteMaxBatch {
		limit = retryPromoteMaxBatch
	}

	jobs, err := c.riverClient.JobList(ctx,
		river.NewJobListParams().
			Kinds("scrape").
			States(rivertype.JobStateRetryable).
			First(limit),
	)
	if err != nil {
		return
	}

	for _, job := range jobs.Jobs {
		if _, err := c.riverClient.JobRetry(ctx, job.ID); err != nil {
			log.Error("failed to promote retryable job", "job_id", job.ID, "error", err)
		} else {
			log.Info("promoted retryable job for immediate retry",
				"job_id", job.ID,
				"attempt", job.Attempt,
			)
		}
	}
}

func (c *Client) Stop(ctx context.Context) error {
	return c.riverClient.Stop(ctx)
}

// RiverClient returns the underlying River client for use with River UI.
func (c *Client) RiverClient() *river.Client[pgx.Tx] {
	return c.riverClient
}

// JobListItem represents a job in the list (without full results).
type JobListItem struct {
	JobID       string
	Status      string
	Keyword     string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	ResultCount int
	Error       string
}

// JobListResult represents the result of listing jobs.
type JobListResult struct {
	Jobs       []JobListItem
	NextCursor string
	HasMore    bool
}

// ValidJobStates are the valid states for filtering jobs.
var ValidJobStates = map[string]rivertype.JobState{
	"available": rivertype.JobStateAvailable,
	"cancelled": rivertype.JobStateCancelled,
	"completed": rivertype.JobStateCompleted,
	"discarded": rivertype.JobStateDiscarded,
	"pending":   rivertype.JobStatePending,
	"retryable": rivertype.JobStateRetryable,
	"running":   rivertype.JobStateRunning,
	"scheduled": rivertype.JobStateScheduled,
}

// ListJobs returns a paginated list of jobs with optional state filtering.
func (c *Client) ListJobs(ctx context.Context, state string, limit int, cursor string) (*JobListResult, error) {
	if limit <= 0 {
		limit = 20
	}

	if limit > 100 {
		limit = 100
	}

	params := river.NewJobListParams().
		Kinds("scrape").
		First(limit)

	// Apply state filter if provided
	if state != "" {
		riverState, ok := ValidJobStates[state]
		if !ok {
			return nil, fmt.Errorf("invalid state: %s", state)
		}

		params = params.States(riverState)
	}

	// Apply cursor if provided
	if cursor != "" {
		var jobCursor river.JobListCursor
		if err := jobCursor.UnmarshalText([]byte(cursor)); err != nil {
			return nil, fmt.Errorf("invalid cursor: %w", err)
		}

		params = params.After(&jobCursor)
	}

	result, err := c.riverClient.JobList(ctx, params)
	if err != nil {
		return nil, err
	}

	// Collect job IDs for bulk result count fetch
	jobIDs := make([]int64, 0, len(result.Jobs))

	for _, job := range result.Jobs {
		if job.State == rivertype.JobStateCompleted {
			jobIDs = append(jobIDs, job.ID)
		}
	}

	// Fetch result counts in bulk
	resultCounts, err := c.getResultCounts(ctx, jobIDs)
	if err != nil {
		resultCounts = make(map[int64]int) // fallback to empty map on error
	}

	items := make([]JobListItem, 0, len(result.Jobs))

	for _, job := range result.Jobs {
		item := JobListItem{
			JobID:     encodeJobID(job.ID),
			CreatedAt: job.CreatedAt,
		}

		// Parse keyword from args
		var args ScrapeJobArgs
		if err := json.Unmarshal(job.EncodedArgs, &args); err == nil {
			item.Keyword = args.Keyword
		}

		// Map state
		switch job.State {
		case rivertype.JobStateAvailable, rivertype.JobStateScheduled, rivertype.JobStateRetryable, rivertype.JobStatePending:
			item.Status = "pending"
		case rivertype.JobStateRunning:
			item.Status = jobStatusRunning
			item.StartedAt = job.AttemptedAt
		case rivertype.JobStateCompleted:
			item.Status = jobStatusCompleted
			item.StartedAt = job.AttemptedAt
			item.CompletedAt = job.FinalizedAt
			item.ResultCount = resultCounts[job.ID]
		case rivertype.JobStateCancelled, rivertype.JobStateDiscarded:
			item.Status = "failed"
			if len(job.Errors) > 0 {
				item.Error = job.Errors[len(job.Errors)-1].Error
			}
		}

		items = append(items, item)
	}

	listResult := &JobListResult{
		Jobs:    items,
		HasMore: len(result.Jobs) == limit,
	}

	// Encode next cursor if there are more results
	if result.LastCursor != nil {
		cursorBytes, err := result.LastCursor.MarshalText()
		if err == nil {
			listResult.NextCursor = string(cursorBytes)
		}
	}

	return listResult, nil
}

// getResultCounts fetches result counts for multiple jobs in a single query.
func (c *Client) getResultCounts(ctx context.Context, jobIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int)
	if len(jobIDs) == 0 {
		return counts, nil
	}

	q := `SELECT job_id, result_count FROM scrape_results WHERE job_id = ANY($1)`

	rows, err := c.dbPool.Query(ctx, q, jobIDs)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	for rows.Next() {
		var jobID int64

		var count int
		if err := rows.Scan(&jobID, &count); err != nil {
			return nil, err
		}

		counts[jobID] = count
	}

	return counts, rows.Err()
}

func (c *Client) InsertJob(ctx context.Context, args ScrapeJobArgs) (string, error) { //nolint:gocritic // hugeParam: ScrapeJobArgs is 96 bytes but is a River job argument and must be passed by value
	if args.TimeoutSecs == 0 {
		args.TimeoutSecs = defaultScrapeTimeoutSecs
	}

	if args.TimeoutSecs < 1 || time.Duration(args.TimeoutSecs)*time.Second > maxScrapeTimeout {
		return "", fmt.Errorf("timeout must be between 1 and %d seconds", int(maxScrapeTimeout/time.Second))
	}

	result, err := c.riverClient.Insert(ctx, args, nil)
	if err != nil {
		return "", err
	}

	return encodeJobID(result.Job.ID), nil
}

type JobStatus struct {
	JobID       string
	Status      string
	Keyword     string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	Results     json.RawMessage
	Error       string
	ResultCount int
}

func (c *Client) GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error) {
	riverJobID, err := decodeJobID(jobID)
	if err != nil {
		return nil, fmt.Errorf("invalid job id: %w", err)
	}

	job, err := c.riverClient.JobGet(ctx, riverJobID)
	if err != nil {
		return nil, fmt.Errorf("job not found: %w", err)
	}

	status := &JobStatus{
		JobID:     jobID,
		CreatedAt: job.CreatedAt,
	}

	var args ScrapeJobArgs
	if err := json.Unmarshal(job.EncodedArgs, &args); err == nil {
		status.Keyword = args.Keyword
	}

	switch job.State { //nolint:exhaustive // raw SQL query returns string states, not all rivertype.JobState values are possible here
	case "available", "scheduled", "retryable":
		status.Status = "pending"
	case jobStatusRunning:
		status.Status = jobStatusRunning
		if job.AttemptedAt != nil {
			status.StartedAt = job.AttemptedAt
		}
	case jobStatusCompleted:
		status.Status = jobStatusCompleted
		if job.AttemptedAt != nil {
			status.StartedAt = job.AttemptedAt
		}

		if job.FinalizedAt != nil {
			status.CompletedAt = job.FinalizedAt
		}

		// Fetch results from scrape_results table
		results, resultCount, err := c.getResults(ctx, riverJobID)
		if err == nil {
			status.Results = results
			status.ResultCount = resultCount
		}
	case "cancelled", "discarded":
		status.Status = "failed"
		if len(job.Errors) > 0 {
			status.Error = job.Errors[len(job.Errors)-1].Error
		}
	}

	return status, nil
}

// getResults fetches results from the scrape_results table.
func (c *Client) getResults(ctx context.Context, jobID int64) (json.RawMessage, int, error) {
	var results json.RawMessage

	var resultCount int

	q := `SELECT results, result_count FROM scrape_results WHERE job_id = $1`

	err := c.dbPool.QueryRow(ctx, q, jobID).Scan(&results, &resultCount)
	if err != nil {
		return nil, 0, err
	}

	return results, resultCount, nil
}

// GetJobResults fetches the raw JSON results and keyword for a job by its encoded ID.
func (c *Client) GetJobResults(ctx context.Context, encodedJobID string) (json.RawMessage, string, error) {
	riverJobID, err := decodeJobID(encodedJobID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid job id: %w", err)
	}

	// Fetch keyword from river job args
	job, err := c.riverClient.JobGet(ctx, riverJobID)
	if err != nil {
		return nil, "", fmt.Errorf("job not found: %w", err)
	}

	var keyword string

	var args ScrapeJobArgs
	if err := json.Unmarshal(job.EncodedArgs, &args); err == nil {
		keyword = args.Keyword
	}

	results, _, err := c.getResults(ctx, riverJobID)
	if err != nil {
		return nil, "", fmt.Errorf("results not found: %w", err)
	}

	return results, keyword, nil
}

// DeleteJob queues a background job to delete a scrape job and its results.
// Returns immediately after validation; actual deletion happens async.
func (c *Client) DeleteJob(ctx context.Context, encodedJobID string) error {
	// 1. Decode the job ID
	jobID, err := decodeJobID(encodedJobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}

	// 2. Check job exists
	job, err := c.riverClient.JobGet(ctx, jobID)
	if err != nil {
		return fmt.Errorf("job not found: %w", err)
	}

	// 3. If running, cancel it first (sync - fast operation)
	if job.State == rivertype.JobStateRunning {
		_, _ = c.riverClient.JobCancel(ctx, jobID)
	}

	// 4. Queue background deletion job
	_, err = c.riverClient.Insert(ctx, JobDeleteArgs{JobID: jobID}, nil)
	if err != nil {
		return fmt.Errorf("failed to queue deletion: %w", err)
	}

	return nil
}

// InsertWorkerProvisionJob queues a background job to provision a cloud worker.
func (c *Client) InsertWorkerProvisionJob(ctx context.Context, args WorkerProvisionArgs) error { //nolint:gocritic // hugeParam: WorkerProvisionArgs is 112 bytes but is a River job argument and must be passed by value
	_, err := c.riverClient.Insert(ctx, args, nil)
	if err != nil {
		return fmt.Errorf("failed to queue worker provision: %w", err)
	}

	return nil
}

// InsertWorkerDeleteJob queues a background job to delete a cloud worker.
// DashboardStats holds summary statistics for the admin dashboard.
type DashboardStats struct {
	JobsToday    int
	TotalResults int
}

// GetDashboardStats fetches job and result counts for the dashboard.
func (c *Client) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	stats := &DashboardStats{}

	err := c.dbPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM river_job WHERE kind = 'scrape' AND created_at >= CURRENT_DATE`,
	).Scan(&stats.JobsToday)
	if err != nil {
		return nil, err
	}

	err = c.dbPool.QueryRow(ctx,
		`SELECT COALESCE(SUM(result_count), 0) FROM scrape_results`,
	).Scan(&stats.TotalResults)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

func (c *Client) InsertWorkerDeleteJob(ctx context.Context, args WorkerDeleteArgs) error {
	_, err := c.riverClient.Insert(ctx, args, nil)
	if err != nil {
		return fmt.Errorf("failed to queue worker deletion: %w", err)
	}

	return nil
}
