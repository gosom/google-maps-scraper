package webrunner

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/proxy"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	"golang.org/x/sync/errgroup"
)

type webrunner struct {
	srv        *web.Server
	svc        *web.Service
	cfg        *runner.Config
	db         *sql.DB
	billingSvc *billing.Service
	proxyPool  *proxy.Pool
}

// buildServerConfig loads integration settings from environment, enforces required
// dependencies (Clerk), and constructs the web.ServerConfig in a single place.
// Stripe settings are optional; if present, they are applied.
func buildServerConfig(cfg *runner.Config, db *sql.DB, svc *web.Service) (web.ServerConfig, error) {
	clerkAPIKey := os.Getenv("CLERK_API_KEY")
	stripeAPIKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")

	if clerkAPIKey == "" {
		log.Println("[WebRunner] FATAL: CLERK_API_KEY is required but missing. Set the CLERK_API_KEY environment variable.")
		return web.ServerConfig{}, fmt.Errorf("CLERK_API_KEY environment variable is required")
	}

	userRepo := postgres.NewUserRepository(db)

	serverCfg := web.ServerConfig{
		Service:             svc,
		Addr:                cfg.Addr,
		PgDB:                db,
		UserRepo:            userRepo,
		ClerkAPIKey:         clerkAPIKey,
		StripeAPIKey:        stripeAPIKey,
		StripeWebhookSecret: stripeWebhookSecret,
	}

	log.Println("[WebRunner] Authentication enabled with Clerk")
	if stripeAPIKey != "" {
		log.Println("[WebRunner] Payment enabled with Stripe")
	}

	return serverCfg, nil
}

func New(cfg *runner.Config) (runner.Runner, error) {
	if cfg.DataFolder == "" {
		return nil, fmt.Errorf("data folder is required")
	}
	if cfg.Dsn == "" {
		return nil, fmt.Errorf("PostgreSQL DSN is required")
	}

	if err := os.MkdirAll(cfg.DataFolder, os.ModePerm); err != nil {
		return nil, err
	}

	var repo web.JobRepository
	var err error
	db, err := sql.Open("pgx", cfg.Dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	// connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	// Run database migrations automatically on startup with meaningful logs
	mr := postgres.NewMigrationRunner(cfg.Dsn)
	if err := mr.RunMigrations(); err != nil {
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	repo, err = postgres.NewRepository(db)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL repository: %w", err)
	}

	svc := web.NewService(repo, cfg.DataFolder)

	// Initialize server configuration (built below by buildServerConfig)

	// Build server config (enforces Clerk, applies Stripe if present)

	serverCfg, err := buildServerConfig(cfg, db, svc)
	if err != nil {
		return nil, err
	}

	// Create web server
	srv, err := web.New(serverCfg)
	if err != nil {
		return nil, err
	}

	// Initialize billing service for event charging (no Stripe required here)
	cfgSvc := config.New(db)
	billSvc := billing.New(db, cfgSvc, "", "")
	if billSvc != nil {
		log.Println("[WebRunner] Billing service initialized successfully for event charging")
	} else {
		log.Println("[WebRunner] WARNING: Billing service is nil - charges will not be applied!")
	}

	// Initialize proxy pool if proxies are configured
	var proxyPool *proxy.Pool
	if len(cfg.Proxies) > 0 {
		log.Printf("DEBUG: WebRunner - Creating proxy pool with %d proxies", len(cfg.Proxies))
		log.Printf("DEBUG: WebRunner - All cfg.Proxies: %v", cfg.Proxies)

		// Create proxy pool with port range 8888-9998 (1000 ports)
		proxyPool, err = proxy.NewPool(cfg.Proxies, 8888, 9998)
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy pool: %w", err)
		}
		log.Printf("🔧 Started proxy pool with %d proxies", len(cfg.Proxies))
	}

	ans := webrunner{
		srv:        srv,
		svc:        svc,
		cfg:        cfg,
		db:         db,
		billingSvc: billSvc,
		proxyPool:  proxyPool,
	}

	return &ans, nil
}

func (w *webrunner) Run(ctx context.Context) error {
	egroup, ctx := errgroup.WithContext(ctx)

	egroup.Go(func() error {
		return w.work(ctx)
	})

	egroup.Go(func() error {
		return w.srv.Start(ctx)
	})

	return egroup.Wait()
}

func (w *webrunner) Close(context.Context) error {
	if w.proxyPool != nil {
		// Proxy pool cleanup would go here if needed
		// For now, individual servers are cleaned up when jobs finish
	}
	if w.db != nil {
		w.db.Close()
	}
	return nil
}

func (w *webrunner) work(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Create a semaphore to limit concurrent jobs
	// Use CONCURRENCY env var or default to 1 for job concurrency
	maxConcurrentJobs := 1
	if w.cfg.Concurrency > 1 {
		// Allow up to half of the concurrency for job-level parallelism
		// This leaves resources for URL-level concurrency within each job
		maxConcurrentJobs = w.cfg.Concurrency / 2
		if maxConcurrentJobs < 1 {
			maxConcurrentJobs = 1
		}
	}

	log.Printf("Starting job worker with max concurrent jobs: %d (total concurrency: %d)", maxConcurrentJobs, w.cfg.Concurrency)

	// Use buffered channel as semaphore
	jobSemaphore := make(chan struct{}, maxConcurrentJobs)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			jobs, err := w.svc.SelectPending(ctx)
			if err != nil {
				return err
			}

			for i := range jobs {
				select {
				case <-ctx.Done():
					return nil
				case jobSemaphore <- struct{}{}: // Acquire semaphore
					// Launch job in goroutine for concurrent execution
					go func(job web.Job) {
						defer func() { <-jobSemaphore }() // Release semaphore when done

						t0 := time.Now().UTC()
						if err := w.scrapeJob(ctx, &job); err != nil {
							params := map[string]any{
								"job_count": len(job.Data.Keywords),
								"duration":  time.Now().UTC().Sub(t0).String(),
								"error":     err.Error(),
							}

							evt := tlmt.NewEvent("web_runner", params)
							_ = runner.Telemetry().Send(ctx, evt)

							log.Printf("error scraping job %s: %v", job.ID, err)
						} else {
							params := map[string]any{
								"job_count": len(job.Data.Keywords),
								"duration":  time.Now().UTC().Sub(t0).String(),
							}

							_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("web_runner", params))

							log.Printf("job %s scraped successfully", job.ID)
						}
					}(jobs[i]) // Pass by value to avoid race condition
				default:
					// Semaphore full, skip this job for now (will be picked up in next tick)
					log.Printf("Job %s skipped - max concurrent jobs (%d) reached", jobs[i].ID, maxConcurrentJobs)
				}
			}
		}
	}
}

func (w *webrunner) scrapeJob(ctx context.Context, job *web.Job) error {
	// Always persist the final job status on exit
	defer func() {
		if err := w.svc.Update(context.Background(), job); err != nil {
			log.Printf("failed to persist final job status for %s: %v", job.ID, err)
		} else {
			log.Printf("DEBUG: Job %s - Final status persisted: %s", job.ID, job.Status)
		}
	}()

	// Charge actor_start at job start (requires sufficient balance)
	if w.billingSvc != nil {
		log.Printf("INFO: Job %s - Attempting actor_start charge for user %s", job.ID, job.UserID)
		if err := w.billingSvc.ChargeActorStart(context.Background(), job.UserID, job.ID); err != nil {
			log.Printf("ERROR: billing: actor_start charge failed for job %s: %v", job.ID, err)
			job.Status = web.StatusFailed
			job.FailureReason = "insufficient credit balance to start job"
			return err
		}
		log.Printf("SUCCESS: billing: actor_start charged successfully for job %s (user: %s)", job.ID, job.UserID)
	} else {
		log.Printf("WARNING: Job %s - Billing service is nil, skipping actor_start charge", job.ID)
	}

	// Check if job has been cancelled before starting
	if job.Status == web.StatusCancelled || job.Status == web.StatusAborting {
		log.Printf("DEBUG: Job %s already cancelled/aborting, skipping execution", job.ID)
		return nil
	}

	// Create a cancellable context for this job
	jobCtx, jobCancel := context.WithCancel(ctx)
	defer jobCancel()

	// Start a goroutine to monitor for cancellation
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-jobCtx.Done():
				log.Printf("DEBUG: Job %s monitoring stopped - context done", job.ID)
				return
			case <-ticker.C:
				// Check current job status in database
				currentJob, err := w.svc.Get(jobCtx, job.ID)
				if err != nil {
					log.Printf("DEBUG: Job %s - failed to get status: %v", job.ID, err)
					continue
				}

				log.Printf("DEBUG: Job %s status check - current status: %s", job.ID, currentJob.Status)

				// Stop monitoring if job has completed (any final status)
				if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled ||
					currentJob.Status == web.StatusOK || currentJob.Status == web.StatusFailed {
					log.Printf("DEBUG: Job %s final status detected (%s), stopping monitoring", job.ID, currentJob.Status)

					// Only cancel execution for user-initiated cancellation
					if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled {
						log.Printf("DEBUG: Job %s user cancellation detected, stopping execution", job.ID)
						log.Printf("DEBUG: Job %s calling jobCancel() to stop mate.Start()", job.ID)
						jobCancel()
					}

					log.Printf("DEBUG: Job %s monitoring goroutine exiting after final status", job.ID)
					return
				}
			}
		}
	}()

	job.Status = web.StatusWorking

	err := w.svc.Update(jobCtx, job)
	if err != nil {
		return err
	}

	if len(job.Data.Keywords) == 0 {
		job.Status = web.StatusFailed
		job.FailureReason = "no keywords provided"
		return err
	}

	outpath := filepath.Join(w.cfg.DataFolder, job.ID+".csv")

	outfile, err := os.Create(outpath)
	if err != nil {
		return err
	}

	defer func() {
		_ = outfile.Close()
	}()

	// Initialize deduper and exitMonitor before use
	dedup := deduper.New()
	exitMonitor := exiter.New()

	mate, err := w.setupMate(jobCtx, outfile, job, exitMonitor)
	if err != nil {
		job.Status = web.StatusFailed
		job.FailureReason = fmt.Sprintf("setupMate failed: %v", err)
		return err
	}

	defer mate.Close()

	var coords string
	if job.Data.Lat != "" && job.Data.Lon != "" {
		coords = job.Data.Lat + "," + job.Data.Lon
	}

	seedJobs, err := runner.CreateSeedJobs(
		job.Data.FastMode,
		job.Data.Lang,
		strings.NewReader(strings.Join(job.Data.Keywords, "\n")),
		job.Data.Depth,
		job.Data.Email,
		job.Data.Images,
		w.cfg.Debug,
		job.Data.ReviewsMax, // Pass the actual review count
		coords,
		job.Data.Zoom,
		func() float64 {
			if job.Data.Radius <= 0 {
				return 10000 // 10 km
			}

			return float64(job.Data.Radius)
		}(),
		dedup,
		exitMonitor,
		job.Data.ReviewsMax > 0, // Keep extraReviews for backward compatibility
		job.Data.MaxResults,     // Pass max results limit
	)
	if err != nil {
		job.Status = web.StatusFailed
		job.FailureReason = fmt.Sprintf("CreateSeedJobs failed: %v", err)
		return err
	}

	jobSuccess := false

	if len(seedJobs) > 0 {
		exitMonitor.SetSeedCount(len(seedJobs))

		allowedSeconds := max(60, len(seedJobs)*10*job.Data.Depth/50+120)

		if job.Data.MaxTime > 0 {
			if job.Data.MaxTime.Seconds() < 180 {
				allowedSeconds = 180
			} else {
				allowedSeconds = int(job.Data.MaxTime.Seconds())
			}
		}

		log.Printf("running job %s with %d seed jobs, %d allowed seconds, max results: %d", job.ID, len(seedJobs), allowedSeconds, job.Data.MaxResults)

		mateCtx, cancel := context.WithTimeout(jobCtx, time.Duration(allowedSeconds)*time.Second)
		defer cancel()

		// Set up exit monitor with max results tracking
		if job.Data.MaxResults > 0 {
			exitMonitor.SetMaxResults(job.Data.MaxResults)
			log.Printf("DEBUG: Job %s - Set max results limit to %d", job.ID, job.Data.MaxResults)
		} else {
			log.Printf("DEBUG: Job %s - No max results limit (unlimited)", job.ID)
		}

		// Channel to monitor exit monitor completion - only trigger forced completion
		// if exit monitor actually detected completion (not just timeout)
		exitMonitorCompleted := make(chan struct{})

		// Create a wrapper cancel function that signals exit monitor completion
		wrapperCancel := func() {
			log.Printf("DEBUG: Job %s - Exit monitor detected completion, signaling forced completion monitor", job.ID)
			select {
			case exitMonitorCompleted <- struct{}{}:
			default:
				// Channel already closed or full, ignore
			}
			cancel() // Call the original cancel function
		}
		exitMonitor.SetCancelFunc(wrapperCancel)
		log.Printf("DEBUG: Job %s - Starting exit monitor", job.ID)

		go exitMonitor.Run(mateCtx)
		log.Printf("DEBUG: Job %s - About to call mate.Start() with %d seed jobs", job.ID, len(seedJobs))

		// Add a backup timeout mechanism to prevent jobs from hanging
		// when max results are reached but mate.Start() doesn't return
		var mateErr error
		done := make(chan struct{})

		go func() {
			defer close(done)
			mateErr = mate.Start(mateCtx, seedJobs...)
			log.Printf("DEBUG: Job %s - mate.Start goroutine completed with error: %v", job.ID, mateErr)
		}()

		// Wait for mate.Start to complete or for a backup timeout
		backupTimeout := time.NewTimer(time.Duration(allowedSeconds+60) * time.Second) // Increased buffer
		defer backupTimeout.Stop()

		// Add a longer forced completion timeout specifically for exit monitor completion
		forcedCompletionTimeout := time.NewTimer(24 * time.Hour) // Start disabled
		defer forcedCompletionTimeout.Stop()

		go func() {
			select {
			case <-exitMonitorCompleted:
				log.Printf("DEBUG: Job %s - Exit monitor completion detected, starting forced completion timer (30s)", job.ID)
				forcedCompletionTimeout.Reset(30 * time.Second)
			case <-mateCtx.Done():
				// Context cancelled but not by exit monitor completion (probably timeout)
				log.Printf("DEBUG: Job %s - Context cancelled but not by exit monitor completion", job.ID)
			}
		}()

		select {
		case <-done:
			// mate.Start completed normally
			err = mateErr
			log.Printf("DEBUG: Job %s - mate.Start completed normally with error: %v", job.ID, err)
		case <-backupTimeout.C:
			// Backup timeout - force completion
			log.Printf("DEBUG: Job %s - Backup timeout triggered, forcing completion", job.ID)
			cancel() // Cancel the mate context
			<-done   // Wait for mate.Start to actually finish
			err = mateErr
			log.Printf("DEBUG: Job %s - Forced completion with error: %v", job.ID, err)
		case <-forcedCompletionTimeout.C:
			// Exit monitor triggered cancellation, but mate.Start is taking too long to respond
			log.Printf("DEBUG: Job %s - Forced completion timeout after exit monitor cancellation", job.ID)
			cancel() // Ensure mate context is cancelled

			// Wait up to 15 more seconds for mate.Start to finish gracefully
			finalWait := time.NewTimer(15 * time.Second) // Increased from 5s
			select {
			case <-done:
				err = mateErr
				log.Printf("DEBUG: Job %s - mate.Start finished after forced completion with error: %v", job.ID, err)
			case <-finalWait.C:
				// mate.Start is completely stuck, proceed with job completion
				log.Printf("DEBUG: Job %s - mate.Start completely unresponsive, proceeding with job completion", job.ID)
				err = context.Canceled // Treat as successful cancellation

				// Force close mate to ensure resources are cleaned up
				go func() {
					log.Printf("DEBUG: Job %s - Force closing mate due to unresponsive mate.Start()", job.ID)
					mate.Close()
				}()
			}
			finalWait.Stop()
		}

		log.Printf("DEBUG: Job %s - Context after mate.Start - Done: %v", job.ID, mateCtx.Err())

		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.Printf("DEBUG: Job %s - Context canceled (checking reason)", job.ID)

				// Check if it was user cancellation
				currentJob, getErr := w.svc.Get(context.Background(), job.ID)
				if getErr != nil {
					log.Printf("DEBUG: Job %s - Failed to get current status after cancellation: %v", job.ID, getErr)
					// Assume it was cancelled if we can't get status
					job.Status = web.StatusCancelled
					jobSuccess = false
				} else {
					log.Printf("DEBUG: Job %s - Current status after context cancellation: %s", job.ID, currentJob.Status)

					if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled {
						job.Status = web.StatusCancelled
						log.Printf("DEBUG: Job %s - Marked as cancelled (user initiated)", job.ID)
						jobSuccess = false // Explicitly mark as not successful for user cancellation
					} else {
						// Check if we actually produced results before marking as successful
						var resultCount int
						if w.db != nil {
							if err := w.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); err != nil {
								log.Printf("DEBUG: Job %s - Failed to count results after cancellation: %v", job.ID, err)
								resultCount = 0
							}
						}

						if resultCount > 0 {
							log.Printf("DEBUG: Job %s - Context cancelled but produced %d results, treating as successful completion", job.ID, resultCount)
							jobSuccess = true
						} else {
							log.Printf("DEBUG: Job %s - Context cancelled with 0 results, treating as failed completion", job.ID)
							job.FailureReason = "scrapemate inactivity timeout / context canceled with 0 results"
							jobSuccess = false
						}
					}
				}
			} else if errors.Is(err, context.DeadlineExceeded) {
				// Check if we actually produced results before marking as successful
				var resultCount int
				if w.db != nil {
					if err := w.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); err != nil {
						log.Printf("DEBUG: Job %s - Failed to count results after timeout: %v", job.ID, err)
						resultCount = 0
					}
				}

				if resultCount > 0 {
					log.Printf("DEBUG: Job %s - Context deadline exceeded but produced %d results, treating as successful", job.ID, resultCount)
					jobSuccess = true
				} else {
					log.Printf("DEBUG: Job %s - Context deadline exceeded with 0 results, treating as failed", job.ID)
					job.FailureReason = "job timed out with 0 results"
					jobSuccess = false
				}
			} else {
				// This is a real error
				log.Printf("DEBUG: Job %s - Real error occurred: %v", job.ID, err)
				cancel()

				job.Status = web.StatusFailed
				job.FailureReason = fmt.Sprintf("runtime error: %v", err)

				return err
			}
		} else {
			log.Printf("DEBUG: Job %s - No error, normal completion", job.ID)
			jobSuccess = true
		}

		// Post-run sanity checks: ensure seeds completed and results were produced
		seedCompleted, seedTotal := exitMonitor.GetSeedProgress()
		resultsWritten := exitMonitor.GetResultsWritten()
		if seedTotal > 0 && seedCompleted < seedTotal {
			log.Printf("DEBUG: Job %s - Seeds incomplete (%d/%d), treating as failed", job.ID, seedCompleted, seedTotal)
			if job.FailureReason == "" {
				job.FailureReason = fmt.Sprintf("seeds incomplete %d/%d", seedCompleted, seedTotal)
			}
			jobSuccess = false
		}
		if resultsWritten == 0 {
			log.Printf("DEBUG: Job %s - 0 results written, treating as failed", job.ID)
			if job.FailureReason == "" {
				job.FailureReason = "0 results written"
			}
			jobSuccess = false
		}

		log.Printf("DEBUG: Job %s - BILLING SECTION: jobSuccess=%v, status=%s, cancelled=%v", job.ID, jobSuccess, job.Status, job.Status == web.StatusCancelled)

		if jobSuccess && job.Status != web.StatusCancelled {
			log.Printf("DEBUG: Job %s - Billing condition passed, checking result count", job.ID)
			var resultCount int
			if w.db != nil {
				if err := w.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); err != nil {
					log.Printf("ERROR: Job %s - Failed to count results before charging places: %v", job.ID, err)
					resultCount = 0
				} else {
					log.Printf("DEBUG: Job %s - Database query successful, resultCount=%d", job.ID, resultCount)
				}
			} else {
				log.Printf("ERROR: Job %s - Database connection is nil, cannot count results", job.ID)
			}

			log.Printf("DEBUG: Job %s - billingSvc nil? %v, resultCount=%d", job.ID, w.billingSvc == nil, resultCount)

			if w.billingSvc != nil && resultCount > 0 {
				log.Printf("INFO: Job %s - Attempting to charge %d places for user %s", job.ID, resultCount, job.UserID)
				if err := w.billingSvc.ChargePlaces(context.Background(), job.UserID, job.ID, resultCount); err != nil {
					// Billing failure should fail the job with clear reason
					log.Printf("ERROR: billing: place_scraped charge failed for job %s (%d places): %v", job.ID, resultCount, err)
					jobSuccess = false
					job.Status = web.StatusFailed
					job.FailureReason = "insufficient credits to charge places"
				} else {
					log.Printf("SUCCESS: billing: successfully charged %d places for job %s (user: %s)", resultCount, job.ID, job.UserID)
				}
			} else {
				if w.billingSvc == nil {
					log.Printf("WARNING: Job %s - Billing service is nil, skipping place charges", job.ID)
				}
				if resultCount == 0 {
					log.Printf("WARNING: Job %s - Result count is 0, skipping place charges", job.ID)
				}
			}
		} else {
			log.Printf("DEBUG: Job %s - Skipping billing: jobSuccess=%v, status=%s", job.ID, jobSuccess, job.Status)
		}

		cancel()
	}

	mate.Close()

	// Determine final job status
	log.Printf("DEBUG: Job %s - Determining final status: jobSuccess=%v, current status=%s", job.ID, jobSuccess, job.Status)

	if job.Status == web.StatusCancelled {
		log.Printf("DEBUG: Job %s - Keeping cancelled status", job.ID)
		// Keep the cancelled status
	} else if jobSuccess {
		job.Status = web.StatusOK
		log.Printf("DEBUG: Job %s - Setting status to OK (successful completion)", job.ID)
	} else {
		job.Status = web.StatusFailed
		log.Printf("DEBUG: Job %s - Setting status to FAILED", job.ID)
	}

	// Charging of places is attempted before marking success above; no charge here

	return nil
}

func (w *webrunner) setupMate(_ context.Context, writer io.Writer, job *web.Job, exitMonitor exiter.Exiter) (*scrapemateapp.ScrapemateApp, error) {
	// Calculate per-job concurrency based on total concurrency and max concurrent jobs
	// This ensures we don't overwhelm the system when running multiple jobs simultaneously
	maxConcurrentJobs := 1
	if w.cfg.Concurrency > 1 {
		maxConcurrentJobs = w.cfg.Concurrency / 2
		if maxConcurrentJobs < 1 {
			maxConcurrentJobs = 1
		}
	}

	// Adjust per-job concurrency to be resource-friendly when running multiple jobs
	perJobConcurrency := w.cfg.Concurrency
	if maxConcurrentJobs > 1 {
		// When running multiple jobs, reduce per-job concurrency to avoid resource contention
		perJobConcurrency = w.cfg.Concurrency / maxConcurrentJobs
		if perJobConcurrency < 1 {
			perJobConcurrency = 1
		}
	}

	log.Printf("job %s configured with per-job concurrency: %d (total system concurrency: %d, max concurrent jobs: %d)",
		job.ID, perJobConcurrency, w.cfg.Concurrency, maxConcurrentJobs)

	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(perJobConcurrency), // Use calculated per-job concurrency
		scrapemateapp.WithExitOnInactivity(0),            // Disable inactivity timeout to allow deep scrolling
	}

	// Always use stealth mode for Google Maps to avoid detection
	if !job.Data.FastMode {
		if w.cfg.Debug {
			opts = append(opts,
				scrapemateapp.WithStealth("firefox"),
				scrapemateapp.WithJS(
					scrapemateapp.Headfull(), // Headful browser for visual debugging
					scrapemateapp.DisableImages(),
				),
			)
		} else {
			opts = append(opts,
				scrapemateapp.WithStealth("firefox"), // Enable stealth for better compatibility
				scrapemateapp.WithJS(scrapemateapp.DisableImages()),
			)
		}
	} else {
		if w.cfg.Debug {
			opts = append(opts,
				scrapemateapp.WithStealth("firefox"),
				scrapemateapp.WithJS(scrapemateapp.Headfull()),
			)
		} else {
			opts = append(opts,
				scrapemateapp.WithStealth("firefox"),
			)
		}
	}

	// Handle proxy configuration
	if w.proxyPool != nil {
		log.Printf("DEBUG: Job %s - requesting proxy from pool", job.ID)
		// Get a dedicated proxy server for this job
		proxySrv, err := w.proxyPool.GetServerForJob(job.ID)
		if err != nil {
			log.Printf("❌ Job %s - failed to get proxy server: %v", job.ID, err)
			// Continue without proxy
		} else {
			localProxyURL := proxySrv.GetLocalURL()
			currentProxy := proxySrv.GetCurrentProxy()
			log.Printf("🎯 Job %s - assigned proxy %s:%s on %s", job.ID, currentProxy.Address, currentProxy.Port, localProxyURL)
			opts = append(opts, scrapemateapp.WithProxies([]string{localProxyURL}))
			log.Printf("DEBUG: Job %s - dedicated proxy server attached to scrapemate config", job.ID)
		}
	} else if len(job.Data.Proxies) > 0 {
		// For job-level proxies, we need to start a separate proxy server
		// This is more complex, so for now we'll log a warning
		log.Printf("DEBUG: Job %s - job-level proxies detected (%d) but proxy pool not available", job.ID, len(job.Data.Proxies))
		log.Printf("WARNING: Job-level proxies are not yet supported with the new proxy system")
	} else {
		log.Printf("DEBUG: Job %s - no proxies configured", job.ID)
	}

	if !w.cfg.DisablePageReuse {
		opts = append(opts,
			scrapemateapp.WithPageReuseLimit(2),
			scrapemateapp.WithPageReuseLimit(200),
		)
	}

	// log.Printf("job %s configured with stealth mode and proxy: %v", job.ID, hasProxy)

	// Create list of writers - CSV and PostgreSQL
	csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(writer))
	writers := []scrapemate.ResultWriter{csvWriter}

	// Add PostgreSQL writer if database is available
	if w.db != nil {
		// Use enhanced result writer with exiter to count actual results
		pgWriter := postgres.NewEnhancedResultWriterWithExiter(w.db, job.UserID, job.ID, exitMonitor)
		writers = append(writers, pgWriter)
		log.Printf("Added PostgreSQL enhanced result writer with exiter for job %s (user: %s)", job.ID, job.UserID)
	} else {
		log.Printf("Warning: No database connection available for job %s - results will only be saved to CSV", job.ID)
	}

	matecfg, err := scrapemateapp.NewConfig(
		writers,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	return scrapemateapp.NewScrapeMateApp(matecfg)
}
