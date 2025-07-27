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

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/postgres"
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
	srv          *web.Server
	svc          *web.Service
	cfg          *runner.Config
	db           *sql.DB // Add database connection for usage tracking
	usageTracker *postgres.UsageTracker
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

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Ping the database to verify connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	log.Println("Running database migrations...")

	migrationRunner := postgres.NewMigrationRunner(cfg.Dsn)

	if err := migrationRunner.RunMigrations(); err != nil {
		log.Printf("Warning: failed to run migrations: %v", err)
		//TODO: handle migration error
	} else {
		log.Println("Database migrations completed.")
	}

	// Create PostgreSQL repository
	repo, err = postgres.NewRepository(db)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL repository: %w", err)
	}

	svc := web.NewService(repo, cfg.DataFolder)

	// Initialize server configuration
	serverCfg := web.ServerConfig{
		Service: svc,
		Addr:    cfg.Addr,
	}
	// Load application config - handle if LoadConfig doesn't exist
	var clerkAPIKey string
	// Try to load config, but don't fail if it doesn't exist
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Warning: LoadConfig not available, continuing without Clerk: %v", r)
		}
	}()

	// Check if LoadConfig exists by checking environment variable directly
	clerkAPIKey = os.Getenv("CLERK_API_KEY")
	stripeAPIKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")

	// Add PostgreSQL and authentication if available
	if cfg.Dsn != "" {
		// If we're using PostgreSQL, add user repository and usage limiter
		userRepo := postgres.NewUserRepository(db)
		usageLimiter := postgres.NewUsageLimiter(db, 10000) // 50 jobs per day limit for development

		serverCfg.PgDB = db
		serverCfg.UserRepo = userRepo
		serverCfg.UsageLimiter = usageLimiter

		// Use Clerk API key from environment
		if clerkAPIKey != "" {
			serverCfg.ClerkAPIKey = clerkAPIKey
			log.Println("Authentication enabled with Clerk")
		}

		// Use Stripe API key and webhook secret from environment
		if stripeAPIKey != "" {
			serverCfg.StripeAPIKey = stripeAPIKey
			serverCfg.StripeWebhookSecret = stripeWebhookSecret
			log.Println("Stripe subscription system enabled")
		}
	}

	// Create web server
	srv, err := web.New(serverCfg)
	if err != nil {
		return nil, err
	}

	// Initialize usage tracker
	usageTracker := postgres.NewUsageTracker(db)

	ans := webrunner{
		srv:          srv,
		svc:          svc,
		cfg:          cfg,
		db:           db,
		usageTracker: usageTracker,
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
	if w.db != nil {
		w.db.Close()
	}
	return nil
}

func (w *webrunner) work(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

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
				default:
					t0 := time.Now().UTC()
					if err := w.scrapeJob(ctx, &jobs[i]); err != nil {
						params := map[string]any{
							"job_count": len(jobs[i].Data.Keywords),
							"duration":  time.Now().UTC().Sub(t0).String(),
							"error":     err.Error(),
						}

						evt := tlmt.NewEvent("web_runner", params)

						_ = runner.Telemetry().Send(ctx, evt)

						log.Printf("error scraping job %s: %v", jobs[i].ID, err)
					} else {
						params := map[string]any{
							"job_count": len(jobs[i].Data.Keywords),
							"duration":  time.Now().UTC().Sub(t0).String(),
						}

						_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("web_runner", params))

						log.Printf("job %s scraped successfully", jobs[i].ID)
					}
				}
			}
		}
	}
}

func (w *webrunner) scrapeJob(ctx context.Context, job *web.Job) error {
	startTime := time.Now()

	// Check if job has been cancelled before starting
	if job.Status == web.StatusCancelled || job.Status == web.StatusAborting {
		log.Printf("DEBUG: Job %s already cancelled/aborting, skipping execution", job.ID)
		return nil
	}

	// Start usage tracking when job begins
	if w.usageTracker != nil && job.UserID != "" {
		err := w.usageTracker.StartJobTracking(ctx, job.ID, job.UserID, job.Data.Email, job.Data.FastMode)
		if err != nil {
			log.Printf("Failed to start usage tracking for job %s: %v", job.ID, err)
			// Continue with job execution even if tracking fails
		}
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

				// If job was cancelled, cancel the context
				if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled {
					log.Printf("DEBUG: Job %s cancellation detected, stopping execution", job.ID)
					jobCancel()
					return
				}
			}
		}
	}()

	job.Status = web.StatusWorking

	err := w.svc.Update(jobCtx, job)
	if err != nil {
		// Complete tracking for failed job
		if w.usageTracker != nil && job.UserID != "" {
			duration := time.Since(startTime)
			w.usageTracker.CompleteJobTracking(jobCtx, job.ID, 0, 0, duration, false)
		}
		return err
	}

	if len(job.Data.Keywords) == 0 {
		job.Status = web.StatusFailed

		// Complete tracking for failed job
		if w.usageTracker != nil && job.UserID != "" {
			duration := time.Since(startTime)
			w.usageTracker.CompleteJobTracking(jobCtx, job.ID, 0, 0, duration, false)
		}

		return w.svc.Update(jobCtx, job)
	}

	outpath := filepath.Join(w.cfg.DataFolder, job.ID+".csv")

	outfile, err := os.Create(outpath)
	if err != nil {
		// Complete tracking for failed job
		if w.usageTracker != nil && job.UserID != "" {
			duration := time.Since(startTime)
			w.usageTracker.CompleteJobTracking(context.Background(), job.ID, 0, 0, duration, false)
		}
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

		err2 := w.svc.Update(context.Background(), job)
		if err2 != nil {
			log.Printf("failed to update job status: %v", err2)
		}

		// Complete tracking for failed job
		if w.usageTracker != nil && job.UserID != "" {
			duration := time.Since(startTime)
			w.usageTracker.CompleteJobTracking(context.Background(), job.ID, 0, 0, duration, false)
		}

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

		err2 := w.svc.Update(context.Background(), job)
		if err2 != nil {
			log.Printf("failed to update job status: %v", err2)
		}

		// Complete tracking for failed job
		if w.usageTracker != nil && job.UserID != "" {
			duration := time.Since(startTime)
			w.usageTracker.CompleteJobTracking(context.Background(), job.ID, 0, 0, duration, false)
		}

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
		exitMonitor.SetCancelFunc(cancel)
		log.Printf("DEBUG: Job %s - Starting exit monitor", job.ID)

		go exitMonitor.Run(mateCtx)

		err = mate.Start(mateCtx, seedJobs...)
		log.Printf("DEBUG: Job %s - mate.Start completed with error: %v", job.ID, err)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.Printf("DEBUG: Job %s - Context canceled (likely cancelled or max results reached)", job.ID)
				// Check if it was actually cancelled
				currentJob, getErr := w.svc.Get(context.Background(), job.ID)
				if getErr == nil && (currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled) {
					job.Status = web.StatusCancelled
					log.Printf("DEBUG: Job %s - Marked as cancelled (user initiated)", job.ID)
				} else {
					jobSuccess = true
					log.Printf("DEBUG: Job %s - Context cancelled but not by user (likely max results or normal completion)", job.ID)
				}
			} else if errors.Is(err, context.DeadlineExceeded) {
				log.Printf("DEBUG: Job %s - Context deadline exceeded (timeout)", job.ID)
				jobSuccess = true
			} else {
				// This is a real error
				log.Printf("DEBUG: Job %s - Real error occurred: %v", job.ID, err)
				cancel()

				job.Status = web.StatusFailed
				err2 := w.svc.Update(context.Background(), job)
				if err2 != nil {
					log.Printf("failed to update job status: %v", err2)
				}

				// Complete tracking for failed job
				if w.usageTracker != nil && job.UserID != "" {
					duration := time.Since(startTime)
					w.usageTracker.CompleteJobTracking(context.Background(), job.ID, 0, 0, duration, false)
				}

				return err
			}
		} else {
			log.Printf("DEBUG: Job %s - No error, normal completion", job.ID)
			jobSuccess = true
		}

		cancel()
	}

	mate.Close()

	// Count results from CSV file after job completion
	locationsFound := 0
	emailsFound := 0

	if jobSuccess && w.usageTracker != nil {
		locations, emails, err := w.usageTracker.CountResultsFromCSV(outpath, job.Data.Email)
		if err != nil {
			log.Printf("Failed to count results from CSV %s: %v", outpath, err)
		} else {
			locationsFound = locations
			emailsFound = emails
		}
	}

	if jobSuccess {
		job.Status = web.StatusOK
	} else {
		job.Status = web.StatusFailed
	}

	// Update job status
	err = w.svc.Update(context.Background(), job)
	if err != nil {
		log.Printf("failed to update job status: %v", err)
	}

	// Complete usage tracking with final results
	if w.usageTracker != nil && job.UserID != "" {
		duration := time.Since(startTime)
		err = w.usageTracker.CompleteJobTracking(context.Background(), job.ID, locationsFound, emailsFound, duration, jobSuccess)
		if err != nil {
			log.Printf("Failed to complete usage tracking for job %s: %v", job.ID, err)
		} else {
			log.Printf("Usage tracking completed for job %s: %d locations, %d emails, duration: %v",
				job.ID, locationsFound, emailsFound, duration)
		}
	}

	return nil
}

func (w *webrunner) setupMate(_ context.Context, writer io.Writer, job *web.Job, exitMonitor exiter.Exiter) (*scrapemateapp.ScrapemateApp, error) {
	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(w.cfg.Concurrency),
		scrapemateapp.WithExitOnInactivity(time.Minute * 3),
	}

	if !job.Data.FastMode {
		opts = append(opts,
			scrapemateapp.WithJS(scrapemateapp.DisableImages()),
		)
	} else {
		opts = append(opts,
			scrapemateapp.WithStealth("firefox"),
		)
	}

	hasProxy := false

	if len(w.cfg.Proxies) > 0 {
		opts = append(opts, scrapemateapp.WithProxies(w.cfg.Proxies))
		hasProxy = true
	} else if len(job.Data.Proxies) > 0 {
		opts = append(opts,
			scrapemateapp.WithProxies(job.Data.Proxies),
		)
		hasProxy = true
	}

	if !w.cfg.DisablePageReuse {
		opts = append(opts,
			scrapemateapp.WithPageReuseLimit(2),
			scrapemateapp.WithPageReuseLimit(200),
		)
	}

	log.Printf("job %s has proxy: %v", job.ID, hasProxy)

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
