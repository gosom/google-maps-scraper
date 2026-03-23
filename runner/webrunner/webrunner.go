package webrunner

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/proxy"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/runner/webrunner/writers"
	"github.com/gosom/google-maps-scraper/s3uploader"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	"golang.org/x/sync/errgroup"
)

type webrunner struct {
	srv         *web.Server
	svc         *web.Service
	cfg         *runner.Config
	db          *sql.DB
	billingSvc  *billing.Service
	proxyPool   *proxy.Pool
	s3Uploader  *s3uploader.Uploader
	s3Bucket    string
	jobFileRepo models.JobFileRepository
	logger      *slog.Logger

	// leakedMu protects leakedMates from concurrent access.
	leakedMu sync.Mutex
	// leakedMates tracks done channels of abandoned mate.Start goroutines
	// so they can be joined (with a timeout) during Close().
	leakedMates []<-chan struct{}
}

// buildServerConfig loads integration settings from environment, enforces required
// dependencies (Clerk), and constructs the web.ServerConfig in a single place.
// Stripe settings are optional; if present, they are applied.
func buildServerConfig(cfg *runner.Config, db *sql.DB, svc *web.Service) (web.ServerConfig, error) {
	clerkSecretKey := os.Getenv("CLERK_SECRET_KEY")
	stripeAPIKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")

	if clerkSecretKey == "" {
		slog.Error("clerk_secret_key_missing", slog.String("detail", "CLERK_SECRET_KEY is required but missing"))
		return web.ServerConfig{}, fmt.Errorf("CLERK_SECRET_KEY environment variable is required")
	}

	isProduction := strings.TrimSpace(os.Getenv("APP_ENV")) == "production"

	if isProduction {
		var missing []string
		if stripeAPIKey == "" {
			missing = append(missing, "STRIPE_SECRET_KEY")
		}
		if stripeWebhookSecret == "" {
			missing = append(missing, "STRIPE_WEBHOOK_SECRET")
		}
		if os.Getenv("ALLOWED_ORIGINS") == "" {
			missing = append(missing, "ALLOWED_ORIGINS")
		}
		if len(missing) > 0 {
			return web.ServerConfig{}, fmt.Errorf("production mode requires these environment variables: %s", strings.Join(missing, ", "))
		}
	}

	userRepo := postgres.NewUserRepository(db)
	apiKeyRepo := postgres.NewAPIKeyRepository(db)
	webhookConfigRepo := postgres.NewWebhookConfigRepository(db)
	webhookDeliveryRepo := postgres.NewJobWebhookDeliveryRepository(db)
	apiKeyServerSecret := []byte(os.Getenv("API_KEY_SERVER_SECRET"))

	// Validate API_KEY_SERVER_SECRET: when API key auth is enabled (apiKeyRepo != nil),
	// an empty or short secret silently disables API key authentication in the auth
	// middleware (which checks len(serverSecret) > 0). Require ≥ 32 bytes to prevent
	// this silent misconfiguration trap (TOCTOU between "repo exists" and "secret exists").
	if apiKeyRepo != nil && len(apiKeyServerSecret) < 32 {
		return web.ServerConfig{}, fmt.Errorf("API_KEY_SERVER_SECRET must be at least 32 bytes when API key auth is enabled (got %d bytes)", len(apiKeyServerSecret))
	}

	serverCfg := web.ServerConfig{
		Service:             svc,
		Addr:                cfg.Addr,
		PgDB:                db,
		UserRepo:            userRepo,
		APIKeyRepo:          apiKeyRepo,
		WebhookConfigRepo:   webhookConfigRepo,
		WebhookDeliveryRepo: webhookDeliveryRepo,
		ServerSecret:        apiKeyServerSecret,
		ClerkSecretKey:      clerkSecretKey,
		StripeAPIKey:        stripeAPIKey,
		StripeWebhookSecret: stripeWebhookSecret,
		Version:             cfg.Version,
	}

	slog.Info("auth_enabled", slog.String("provider", "clerk"))
	if stripeAPIKey != "" {
		slog.Info("payment_enabled", slog.String("provider", "stripe"))
	}

	slog.Info("startup_config_summary",
		slog.Bool("stripe_enabled", stripeAPIKey != ""),
		slog.Bool("production_mode", isProduction),
	)

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

	// connection pool settings — configurable via env vars
	maxOpen := envInt("DB_MAX_OPEN_CONNS", 25)
	if maxOpen == 0 {
		slog.Error("db_pool_misconfigured", "msg", "DB_MAX_OPEN_CONNS=0 creates unbounded pool — refusing to start")
		os.Exit(1)
	}
	maxIdle := envInt("DB_MAX_IDLE_CONNS", 10)
	connMaxLifetime := envDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute)
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(2 * time.Minute)

	// Startup validation: verify DB connectivity with a 10-second timeout before
	// the HTTP server starts accepting traffic. This ensures the container/process
	// fails fast and exits with code 1 rather than serving unhealthy requests.
	{
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pingCancel()
		if err := db.PingContext(pingCtx); err != nil {
			slog.Error("startup_db_ping_failed",
				slog.Any("error", err),
				slog.String("detail", "cannot reach PostgreSQL within 10s; aborting startup"),
			)
			os.Exit(1)
		}
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
		slog.Info("billing_service_initialized")
	} else {
		slog.Warn("billing_service_nil", slog.String("detail", "charges will not be applied"))
	}

	// Initialize proxy pool if proxies are configured
	var proxyPool *proxy.Pool
	if len(cfg.Proxies) > 0 {
		slog.Debug("creating_proxy_pool", slog.Int("proxy_count", len(cfg.Proxies)))
		slog.Debug("proxy_list", slog.Any("proxies", cfg.Proxies))

		// Create proxy pool with port range 8888-9998 (1000 ports)
		proxyPool, err = proxy.NewPool(cfg.Proxies, 8888, 9998, slog.Default())
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy pool: %w", err)
		}
		slog.Info("proxy_pool_started", slog.Int("proxy_count", len(cfg.Proxies)))
	}

	// Initialize S3 uploader if AWS credentials are configured
	var s3Upload *s3uploader.Uploader
	var s3BucketName string

	awsAccessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	awsSecretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	awsRegion := os.Getenv("AWS_REGION")
	s3BucketName = os.Getenv("S3_BUCKET_NAME")

	if awsAccessKey != "" && awsSecretKey != "" && awsRegion != "" && s3BucketName != "" {
		var s3Err error
		s3Upload, s3Err = s3uploader.New(awsAccessKey, awsSecretKey, awsRegion)
		if s3Err != nil {
			slog.Warn("s3_uploader_init_failed", slog.String("detail", "files will only be stored locally"), slog.Any("error", s3Err))
		} else {
			slog.Info("s3_uploader_initialized", slog.String("bucket", s3BucketName), slog.String("region", awsRegion))
		}
	} else {
		slog.Info("s3_not_configured", slog.String("detail", "files will only be stored locally"))
		if awsAccessKey == "" {
			slog.Info("s3_missing_env", slog.String("var", "AWS_ACCESS_KEY_ID"))
		}
		if awsSecretKey == "" {
			slog.Info("s3_missing_env", slog.String("var", "AWS_SECRET_ACCESS_KEY"))
		}
		if awsRegion == "" {
			slog.Info("s3_missing_env", slog.String("var", "AWS_REGION"))
		}
		if s3BucketName == "" {
			slog.Info("s3_missing_env", slog.String("var", "S3_BUCKET_NAME"))
		}
	}

	if strings.TrimSpace(os.Getenv("APP_ENV")) == "production" && s3Upload == nil {
		slog.Error("s3_required_in_production", slog.String("detail", "S3 credentials are required when APP_ENV=production"))
		os.Exit(1)
	}

	slog.Info("startup_feature_summary",
		slog.Bool("s3_enabled", s3Upload != nil),
		slog.String("app_env", os.Getenv("APP_ENV")),
	)

	// Initialize job file repository
	jobFileRepo, err := postgres.NewJobFileRepository(db)
	if err != nil {
		return nil, fmt.Errorf("failed to create job file repository: %w", err)
	}

	// Configure S3 on the service if S3 is available
	if s3Upload != nil && s3BucketName != "" && jobFileRepo != nil {
		svc.SetS3Config(jobFileRepo, s3Upload, s3BucketName)
		slog.Info("s3_download_configured", slog.String("bucket", s3BucketName))
	}

	// Initialize Google cookies for authenticated scraping (reviews access)
	cookiesFile := cfg.CookiesFile
	if cookiesFile == "" {
		cookiesFile = os.Getenv("GOOGLE_COOKIES_FILE")
	}
	if cookiesFile != "" {
		gmaps.SetCookiesFile(cookiesFile)
		slog.Info("google_cookies_configured", slog.String("file", cookiesFile))
	} else {
		slog.Info("google_cookies_not_configured", slog.String("detail", "reviews may be restricted without authentication"))
	}

	ans := webrunner{
		srv:         srv,
		svc:         svc,
		cfg:         cfg,
		db:          db,
		billingSvc:  billSvc,
		proxyPool:   proxyPool,
		s3Uploader:  s3Upload,
		s3Bucket:    s3BucketName,
		jobFileRepo: jobFileRepo,
		logger:      pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "webrunner"),
	}

	return &ans, nil
}

func (w *webrunner) Run(ctx context.Context) error {
	egroup, ctx := errgroup.WithContext(ctx)

	// Start stuck-job reaper in its own goroutine. Reads interval and timeout
	// from env vars STUCK_JOB_CHECK_INTERVAL_MINUTES (default 10) and
	// STUCK_JOB_TIMEOUT_HOURS (default 4).
	stuckCheckMins := envInt("STUCK_JOB_CHECK_INTERVAL_MINUTES", 10)
	stuckTimeoutHours := envInt("STUCK_JOB_TIMEOUT_HOURS", 4)
	checkInterval := time.Duration(stuckCheckMins) * time.Minute
	go postgres.RunStuckJobReaper(ctx, w.db, w.logger, checkInterval, stuckTimeoutHours)

	// Webhook event cleanup goroutine: removes processed_webhook_events older than
	// WEBHOOK_EVENT_RETENTION_DAYS (default 90) in daily batches.
	if w.billingSvc != nil {
		retentionDays := envInt("WEBHOOK_EVENT_RETENTION_DAYS", 90)
		go w.billingSvc.StartWebhookEventCleanup(ctx, retentionDays)
	}

	egroup.Go(func() error {
		return w.work(ctx)
	})

	egroup.Go(func() error {
		return w.srv.Start(ctx)
	})

	return egroup.Wait()
}

// trackLeakedMate registers the done channel of an abandoned mate.Start
// goroutine so it can be joined during Close().
func (w *webrunner) trackLeakedMate(done <-chan struct{}, jobID string) {
	w.leakedMu.Lock()
	defer w.leakedMu.Unlock()
	w.leakedMates = append(w.leakedMates, done)
	w.logger.Warn("leaked_mate_tracked", slog.String("job_id", jobID), slog.Int("total_leaked", len(w.leakedMates)))
}

func (w *webrunner) Close(context.Context) error {
	// Drain all leaked mate.Start goroutines with a timeout
	w.leakedMu.Lock()
	leaked := w.leakedMates
	w.leakedMates = nil
	w.leakedMu.Unlock()

	if len(leaked) > 0 {
		w.logger.Info("draining_leaked_mates", slog.Int("count", len(leaked)))
		deadline := time.After(30 * time.Second)
		for i, done := range leaked {
			select {
			case <-done:
				w.logger.Debug("leaked_mate_joined", slog.Int("index", i))
			case <-deadline:
				w.logger.Warn("leaked_mate_drain_timeout", slog.Int("joined", i), slog.Int("remaining", len(leaked)-i))
				goto drained
			}
		}
	}
drained:

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

	w.logger.Info("job_worker_starting", slog.Int("max_concurrent_jobs", maxConcurrentJobs), slog.Int("total_concurrency", w.cfg.Concurrency))

	// Use buffered channel as semaphore
	jobSemaphore := make(chan struct{}, maxConcurrentJobs)

	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Skip DB query if all semaphore slots are occupied — no capacity to process results
			if len(jobSemaphore) >= cap(jobSemaphore) {
				continue
			}

			jobs, err := w.svc.SelectPending(ctx)
			if err != nil {
				consecutiveErrors++
				w.logger.Error("select_pending_failed", slog.Any("error", err), slog.Int("consecutive", consecutiveErrors))
				if consecutiveErrors >= 10 {
					return fmt.Errorf("too many consecutive SelectPending failures: %w", err)
				}
				continue
			}
			consecutiveErrors = 0

			for i := range jobs {
				select {
				case <-ctx.Done():
					return nil
				case jobSemaphore <- struct{}{}: // Acquire semaphore
					// Launch job in goroutine for concurrent execution
					go func(job web.Job) {
						defer func() { <-jobSemaphore }() // Release semaphore when done
						defer func() {
							if r := recover(); r != nil {
								w.logger.Error("job_worker_panic_recovered",
									slog.String("job_id", job.ID),
									slog.Any("panic", r))
							}
						}()

						t0 := time.Now().UTC()
						if err := w.scrapeJob(ctx, &job); err != nil {
							params := map[string]any{
								"job_count": len(job.Data.Keywords),
								"duration":  time.Now().UTC().Sub(t0).String(),
								"error":     err.Error(),
							}

							evt := tlmt.NewEvent("web_runner", params)
							_ = runner.Telemetry().Send(ctx, evt)

							w.logger.Error("job_scrape_failed", slog.String("job_id", job.ID), slog.Any("error", err))
						} else {
							params := map[string]any{
								"job_count": len(job.Data.Keywords),
								"duration":  time.Now().UTC().Sub(t0).String(),
							}

							_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("web_runner", params))

							w.logger.Info("job_scrape_succeeded", slog.String("job_id", job.ID))
						}
					}(jobs[i]) // Pass by value to avoid race condition
				default:
					// Semaphore full, skip this job for now (will be picked up in next tick)
					w.logger.Info("job_skipped_max_concurrent", slog.String("job_id", jobs[i].ID), slog.Int("max_concurrent_jobs", maxConcurrentJobs))
				}
			}
		}
	}
}

func (w *webrunner) scrapeJob(ctx context.Context, job *web.Job) error {
	// Always persist the final job status on exit
	defer func() {
		deferCtx, deferCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer deferCancel()
		if err := w.svc.Update(deferCtx, job); err != nil {
			w.logger.Error("job_status_persist_failed", slog.String("job_id", job.ID), slog.String("operation", "Update"), slog.Any("error", err))
			return
		}
		w.logger.Debug("job_status_persisted", slog.String("job_id", job.ID), slog.String("status", string(job.Status)))
	}()

	// Charge actor_start at job start (requires sufficient balance)
	if w.billingSvc != nil {
		w.logger.Info("actor_start_charge_attempting", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
		actorStartCtx, actorStartCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer actorStartCancel()
		if err := w.billingSvc.ChargeActorStart(actorStartCtx, job.UserID, job.ID); err != nil {
			w.logger.Error("actor_start_charge_failed", slog.String("job_id", job.ID), slog.Any("error", err))
			job.Status = web.StatusFailed
			job.FailureReason = "insufficient credit balance to start job"
			return err
		}
		w.logger.Info("actor_start_charge_succeeded", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
	} else {
		w.logger.Warn("billing_service_nil", slog.String("job_id", job.ID), slog.String("detail", "skipping actor_start charge"))
	}

	// Check if job has been cancelled before starting
	if job.Status == web.StatusCancelled || job.Status == web.StatusAborting {
		w.logger.Debug("job_already_cancelled", slog.String("job_id", job.ID))
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
				w.logger.Debug("job_monitoring_stopped", slog.String("job_id", job.ID), slog.String("reason", "context_done"))
				return
			case <-ticker.C:
				// Check current job status in database (admin bypass - internal monitoring)
				currentJob, err := w.svc.Get(jobCtx, job.ID, "")
				if err != nil {
					w.logger.Debug("job_status_check_failed", slog.String("job_id", job.ID), slog.Any("error", err))
					continue
				}

				w.logger.Debug("job_status_check", slog.String("job_id", job.ID), slog.String("status", string(currentJob.Status)))

				// Stop monitoring if job has completed (any final status)
				if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled ||
					currentJob.Status == web.StatusOK || currentJob.Status == web.StatusFailed {
					w.logger.Debug("job_final_status_detected", slog.String("job_id", job.ID), slog.String("status", string(currentJob.Status)))

					// Only cancel execution for user-initiated cancellation
					if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled {
						w.logger.Debug("job_user_cancellation_detected", slog.String("job_id", job.ID))
						w.logger.Debug("job_cancel_invoked", slog.String("job_id", job.ID))
						jobCancel()
					}

					w.logger.Debug("job_monitoring_exiting", slog.String("job_id", job.ID))
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

	// Write UTF-8 BOM to ensure proper encoding detection in Excel and other programs
	// BOM = 0xEF, 0xBB, 0xBF
	if _, err := outfile.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		outfile.Close()
		return fmt.Errorf("failed to write UTF-8 BOM: %w", err)
	}
	w.logger.Debug("utf8_bom_written", slog.String("job_id", job.ID))

	// Track whether file was closed to avoid double-close in defer
	fileClosed := false
	defer func() {
		if !fileClosed {
			if err := outfile.Close(); err != nil {
				w.logger.Error("csv_file_close_failed_defer", slog.String("job_id", job.ID), slog.Any("error", err))
			}
		}
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

		// Base timeout: generous default since we can't predict how many places Google returns
		// Old formula was too aggressive: 1 seed * 10 * depth / 50 + 120 = ~300s
		// Reality: a single search can return 100+ places, each taking 10-25s with images
		// New default: 1 hour for any job with depth > 0 (place detail scraping)
		allowedSeconds := max(300, len(seedJobs)*10*job.Data.Depth/50+120)
		if job.Data.Depth > 0 {
			allowedSeconds = max(allowedSeconds, 3600) // 1 hour minimum for place scraping
		}

		if job.Data.MaxTime > 0 {
			userMaxTime := int(job.Data.MaxTime.Seconds())
			if userMaxTime < 180 {
				userMaxTime = 180
			}
			// Ensure user-specified max_time doesn't override the 1-hour minimum for deep scrapes
			if job.Data.Depth > 0 && userMaxTime < 3600 {
				slog.Info("max_time_overridden_for_deep_scrape",
					slog.Int("user_max_time", userMaxTime),
					slog.Int("enforced_min", 3600),
				)
				userMaxTime = 3600
			}
			allowedSeconds = userMaxTime
		}

		w.logger.Info("job_running", slog.String("job_id", job.ID), slog.Int("seed_jobs", len(seedJobs)), slog.Int("allowed_seconds", allowedSeconds), slog.Int("max_results", job.Data.MaxResults))

		mateCtx, cancel := context.WithTimeout(jobCtx, time.Duration(allowedSeconds)*time.Second)
		defer cancel()

		// Set up exit monitor with max results tracking
		if job.Data.MaxResults > 0 {
			exitMonitor.SetMaxResults(job.Data.MaxResults)
			w.logger.Debug("max_results_set", slog.String("job_id", job.ID), slog.Int("max_results", job.Data.MaxResults))
		} else {
			w.logger.Debug("max_results_unlimited", slog.String("job_id", job.ID))
		}

		// Channel to monitor exit monitor completion - only trigger forced completion
		// if exit monitor actually detected completion (not just timeout)
		exitMonitorCompleted := make(chan struct{})

		// Create a wrapper cancel function that signals exit monitor completion
		wrapperCancel := func() {
			w.logger.Debug("exit_monitor_completion_signaled", slog.String("job_id", job.ID))
			select {
			case exitMonitorCompleted <- struct{}{}:
			default:
				// Channel already closed or full, ignore
			}
			cancel() // Call the original cancel function
		}
		exitMonitor.SetCancelFunc(wrapperCancel)
		w.logger.Debug("exit_monitor_starting", slog.String("job_id", job.ID))

		go exitMonitor.Run(mateCtx)
		w.logger.Debug("mate_start_invoking", slog.String("job_id", job.ID), slog.Int("seed_jobs", len(seedJobs)))

		// Add a backup timeout mechanism to prevent jobs from hanging
		// when max results are reached but mate.Start() doesn't return
		var mateErr error
		done := make(chan struct{})

		go func() {
			defer close(done)
			mateErr = mate.Start(mateCtx, seedJobs...)
			w.logger.Debug("mate_start_goroutine_completed", slog.String("job_id", job.ID), slog.Any("error", mateErr))
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
				w.logger.Debug("exit_monitor_completion_detected", slog.String("job_id", job.ID), slog.Int("forced_completion_timer_seconds", 30))
				forcedCompletionTimeout.Reset(30 * time.Second)
			case <-mateCtx.Done():
				// Context cancelled but not by exit monitor completion (probably timeout)
				w.logger.Debug("context_cancelled_not_exit_monitor", slog.String("job_id", job.ID))
			}
		}()

		select {
		case <-done:
			// mate.Start completed normally
			err = mateErr
			w.logger.Debug("mate_start_completed_normally", slog.String("job_id", job.ID), slog.Any("error", err))
		case <-backupTimeout.C:
			// Backup timeout - force completion
			w.logger.Debug("backup_timeout_triggered", slog.String("job_id", job.ID))
			cancel() // Cancel the mate context

			// Wait for mate.Start with a timeout - workers may be stuck in long extraction cycles
			backupWait := time.NewTimer(30 * time.Second)
			select {
			case <-done:
				w.logger.Debug("mate_finished_after_backup_cancel", slog.String("job_id", job.ID))
			case <-backupWait.C:
				w.logger.Warn("mate_stuck_after_backup_timeout", slog.String("job_id", job.ID))
				// Force close mate to kill stuck Playwright workers
				go func() {
					w.logger.Debug("force_closing_mate_backup", slog.String("job_id", job.ID))
					mate.Close()
				}()
				// Give it one more chance
				finalWait := time.NewTimer(15 * time.Second)
				select {
				case <-done:
					w.logger.Debug("mate_finished_after_force_close", slog.String("job_id", job.ID))
				case <-finalWait.C:
					w.logger.Warn("mate_unresponsive_proceeding", slog.String("job_id", job.ID))
					// Track the leaked goroutine so Close() can join it
					w.trackLeakedMate(done, job.ID)
				}
				finalWait.Stop()
			}
			backupWait.Stop()

			err = mateErr
			w.logger.Debug("forced_completion", slog.String("job_id", job.ID), slog.Any("error", err))
		case <-forcedCompletionTimeout.C:
			// Exit monitor triggered cancellation, but mate.Start is taking too long to respond
			w.logger.Debug("forced_completion_timeout", slog.String("job_id", job.ID))
			cancel() // Ensure mate context is cancelled

			// Wait up to 15 more seconds for mate.Start to finish gracefully
			finalWait := time.NewTimer(15 * time.Second) // Increased from 5s
			select {
			case <-done:
				err = mateErr
				w.logger.Debug("mate_start_finished_after_forced", slog.String("job_id", job.ID), slog.Any("error", err))
			case <-finalWait.C:
				// mate.Start is completely stuck, proceed with job completion
				w.logger.Debug("mate_start_unresponsive", slog.String("job_id", job.ID))
				err = context.Canceled // Treat as successful cancellation

				// Force close mate to ensure resources are cleaned up
				go func() {
					w.logger.Debug("force_closing_mate", slog.String("job_id", job.ID))
					mate.Close()
				}()
				// Track the leaked goroutine so Close() can join it
				w.trackLeakedMate(done, job.ID)
			}
			finalWait.Stop()
		}

		w.logger.Debug("context_after_mate_start", slog.String("job_id", job.ID), slog.Any("context_err", mateCtx.Err()))

		if err != nil {
			if errors.Is(err, context.Canceled) {
				w.logger.Debug("context_canceled_checking_reason", slog.String("job_id", job.ID))

				// Check if it was user cancellation (admin bypass - internal runner)
				cancelCheckCtx, cancelCheckCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancelCheckCancel()
				currentJob, getErr := w.svc.Get(cancelCheckCtx, job.ID, "")
				if getErr != nil {
					w.logger.Debug("status_check_after_cancel_failed", slog.String("job_id", job.ID), slog.Any("error", getErr))
					// Assume it was cancelled if we can't get status
					job.Status = web.StatusCancelled
					jobSuccess = false
				} else {
					w.logger.Debug("status_after_context_cancellation", slog.String("job_id", job.ID), slog.String("status", string(currentJob.Status)))

					if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled {
						job.Status = web.StatusCancelled
						w.logger.Debug("job_marked_cancelled_user_initiated", slog.String("job_id", job.ID))
						jobSuccess = false // Explicitly mark as not successful for user cancellation
					} else {
						// Check if we actually produced results before marking as successful
						var resultCount int
						if w.db != nil {
							cancelCountCtx, cancelCountCancel := context.WithTimeout(context.Background(), 10*time.Second)
							defer cancelCountCancel()
							if err := w.db.QueryRowContext(cancelCountCtx, `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); err != nil {
								w.logger.Debug("result_count_after_cancel_failed", slog.String("job_id", job.ID), slog.Any("error", err))
								resultCount = 0
							}
						}

						if resultCount > 0 {
							w.logger.Debug("cancelled_with_results_treating_success", slog.String("job_id", job.ID), slog.Int("result_count", resultCount))
							jobSuccess = true
						} else {
							w.logger.Debug("cancelled_with_zero_results_treating_failed", slog.String("job_id", job.ID))
							job.FailureReason = "scrapemate inactivity timeout / context canceled with 0 results"
							jobSuccess = false
						}
					}
				}
			} else if errors.Is(err, context.DeadlineExceeded) {
				// Check if we actually produced results before marking as successful
				var resultCount int
				if w.db != nil {
					deadlineCountCtx, deadlineCountCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer deadlineCountCancel()
					if err := w.db.QueryRowContext(deadlineCountCtx, `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); err != nil {
						w.logger.Debug("result_count_after_timeout_failed", slog.String("job_id", job.ID), slog.Any("error", err))
						resultCount = 0
					}
				}

				if resultCount > 0 {
					w.logger.Debug("deadline_exceeded_with_results", slog.String("job_id", job.ID), slog.Int("result_count", resultCount))
					jobSuccess = true
				} else {
					w.logger.Debug("deadline_exceeded_zero_results", slog.String("job_id", job.ID))
					job.FailureReason = "job timed out with 0 results"
					jobSuccess = false
				}
			} else {
				// This is a real error
				w.logger.Debug("real_error_occurred", slog.String("job_id", job.ID), slog.Any("error", err))
				cancel()

				job.Status = web.StatusFailed
				job.FailureReason = fmt.Sprintf("runtime error: %v", err)

				return err
			}
		} else {
			w.logger.Debug("job_normal_completion", slog.String("job_id", job.ID))
			jobSuccess = true
		}

		// Post-run sanity checks: ensure seeds completed and results were produced
		seedCompleted, seedTotal := exitMonitor.GetSeedProgress()
		resultsWritten := exitMonitor.GetResultsWritten()
		if seedTotal > 0 && seedCompleted < seedTotal {
			w.logger.Debug("seeds_incomplete", slog.String("job_id", job.ID), slog.Int("completed", seedCompleted), slog.Int("total", seedTotal))
			if job.FailureReason == "" {
				job.FailureReason = fmt.Sprintf("seeds incomplete %d/%d", seedCompleted, seedTotal)
			}
			jobSuccess = false
		}
		if resultsWritten == 0 {
			w.logger.Debug("zero_results_written", slog.String("job_id", job.ID))
			if job.FailureReason == "" {
				job.FailureReason = "0 results written"
			}
			jobSuccess = false
		}

		w.logger.Debug("billing_section_entry", slog.String("job_id", job.ID), slog.Bool("job_success", jobSuccess), slog.String("status", string(job.Status)), slog.Bool("cancelled", job.Status == web.StatusCancelled))

		if jobSuccess && job.Status != web.StatusCancelled {
			w.logger.Debug("billing_condition_passed", slog.String("job_id", job.ID))
			var resultCount int
			if w.db != nil {
				billingCountCtx, billingCountCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer billingCountCancel()
				if err := w.db.QueryRowContext(billingCountCtx, `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); err != nil {
					w.logger.Error("result_count_query_failed", slog.String("job_id", job.ID), slog.Any("error", err))
					resultCount = 0
				} else {
					w.logger.Debug("result_count_query_succeeded", slog.String("job_id", job.ID), slog.Int("result_count", resultCount))
				}
			} else {
				w.logger.Error("database_connection_nil", slog.String("job_id", job.ID))
			}

			w.logger.Debug("billing_check", slog.String("job_id", job.ID), slog.Bool("billing_svc_nil", w.billingSvc == nil), slog.Int("result_count", resultCount))

			if w.billingSvc != nil && resultCount > 0 {
				// Charge ALL events in a single atomic transaction
				// This includes: places, reviews, images, and contact details
				// If any charge fails, ALL charges are rolled back (all-or-nothing)
				w.logger.Info("billing_charge_attempting", slog.String("job_id", job.ID), slog.Int("result_count", resultCount), slog.String("user_id", job.UserID))

				chargeAllCtx, chargeAllCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer chargeAllCancel()
				if err := w.billingSvc.ChargeAllJobEvents(chargeAllCtx, job.UserID, job.ID, resultCount); err != nil {
					w.logger.Error("billing_atomic_charge_failed", slog.String("job_id", job.ID), slog.Any("error", err))
					jobSuccess = false
					job.Status = web.StatusFailed
					job.FailureReason = fmt.Sprintf("billing failed: %v", err)
					// Return the error so caller knows the job failed
					return fmt.Errorf("billing failed: %w", err)
				} else {
					w.logger.Info("billing_charge_succeeded", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
				}
			} else {
				if w.billingSvc == nil {
					w.logger.Warn("billing_service_nil_skipping_charges", slog.String("job_id", job.ID))
				}
				if resultCount == 0 {
					w.logger.Warn("result_count_zero_skipping_charges", slog.String("job_id", job.ID))
				}
			}
		} else {
			w.logger.Debug("billing_skipped", slog.String("job_id", job.ID), slog.Bool("job_success", jobSuccess), slog.String("status", string(job.Status)))
		}

		cancel()
	}

	mate.Close()

	// CRITICAL: Close the CSV file handle before S3 upload or any file operations
	// This ensures all buffered data is flushed to disk and the file is fully written
	// For writable files, we must explicitly check Close() errors as they can indicate data loss
	w.logger.Debug("csv_file_closing", slog.String("job_id", job.ID))
	if err := outfile.Close(); err != nil {
		w.logger.Error("csv_file_close_failed", slog.String("job_id", job.ID), slog.Any("error", err))
		// File close errors can indicate I/O errors (EIO) meaning data was lost
		// This should fail the job to ensure data integrity
		job.Status = web.StatusFailed
		job.FailureReason = fmt.Sprintf("failed to close CSV file: %v", err)
		return fmt.Errorf("failed to close CSV file: %w", err)
	}
	fileClosed = true
	w.logger.Debug("csv_file_closed", slog.String("job_id", job.ID))

	// Determine final job status
	w.logger.Debug("determining_final_status", slog.String("job_id", job.ID), slog.Bool("job_success", jobSuccess), slog.String("current_status", string(job.Status)))

	if job.Status == web.StatusCancelled {
		w.logger.Debug("keeping_cancelled_status", slog.String("job_id", job.ID))
		// Keep the cancelled status
	} else if jobSuccess {
		job.Status = web.StatusOK
		w.logger.Debug("status_set_ok", slog.String("job_id", job.ID))

		// Upload CSV to S3 and save metadata if S3 is configured
		// File is now fully closed and flushed to disk, safe to upload
		if err := w.uploadToS3AndSaveMetadata(ctx, job, outpath); err != nil {
			w.logger.Error("s3_upload_failed", slog.String("job_id", job.ID), slog.Any("error", err), slog.String("detail", "job still marked as successful"))
			// Don't fail the job due to S3 upload failure - the scraping was successful
			// The CSV file will remain on local storage
		}
	} else {
		job.Status = web.StatusFailed
		w.logger.Debug("status_set_failed", slog.String("job_id", job.ID))
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

	w.logger.Info("job_concurrency_configured", slog.String("job_id", job.ID), slog.Int("per_job_concurrency", perJobConcurrency), slog.Int("total_concurrency", w.cfg.Concurrency), slog.Int("max_concurrent_jobs", maxConcurrentJobs))

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
		w.logger.Debug("proxy_requesting_from_pool", slog.String("job_id", job.ID))
		// Get a dedicated proxy server for this job
		proxySrv, err := w.proxyPool.GetServerForJob(job.ID)
		if err != nil {
			w.logger.Error("proxy_server_get_failed", slog.String("job_id", job.ID), slog.Any("error", err))
			// Continue without proxy
		} else {
			localProxyURL := proxySrv.GetLocalURL()
			currentProxy := proxySrv.GetCurrentProxy()
			w.logger.Info("proxy_assigned", slog.String("job_id", job.ID), slog.String("address", currentProxy.Address), slog.String("port", currentProxy.Port), slog.String("local_url", localProxyURL))
			opts = append(opts, scrapemateapp.WithProxies([]string{localProxyURL}))
			w.logger.Debug("proxy_server_attached", slog.String("job_id", job.ID))
		}
	} else if len(job.Data.Proxies) > 0 {
		// User-supplied proxies (job.Data.Proxies) are intentionally NOT forwarded to the scraper.
		// The production proxy system uses admin-configured proxyPool only. Passing user-supplied
		// proxy URLs to the scraper without validation would be a CWE-918 (SSRF) risk — do not
		// implement this without strict scheme enforcement (HTTPS only) and RFC1918 blocking.
		w.logger.Debug("job_level_proxies_detected", slog.String("job_id", job.ID), slog.Int("proxy_count", len(job.Data.Proxies)))
		w.logger.Warn("job_level_proxies_unsupported", slog.String("detail", "not yet supported with the new proxy system"))
	} else {
		w.logger.Debug("no_proxies_configured", slog.String("job_id", job.ID))
	}

	if !w.cfg.DisablePageReuse {
		opts = append(opts,
			scrapemateapp.WithPageReuseLimit(2),
			scrapemateapp.WithBrowserReuseLimit(200),
		)
	}

	// Create list of writers
	// CRITICAL: Use a SINGLE synchronized writer that writes to both PostgreSQL and CSV atomically
	// This ensures perfect 1:1 correspondence between the two destinations
	var writersList []scrapemate.ResultWriter

	// Add synchronized dual writer if database is available
	if w.db != nil {
		// Create the CSV writer
		csvWriterInstance := csv.NewWriter(writer)

		// Create synchronized dual writer that writes to BOTH destinations
		syncWriter := writers.NewSynchronizedDualWriter(w.db, csvWriterInstance, job.UserID, job.ID, exitMonitor)

		writersList = []scrapemate.ResultWriter{syncWriter}
		w.logger.Info("sync_dual_writer_added", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
	} else {
		// No database, use plain CSV writer
		csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(writer))
		writersList = []scrapemate.ResultWriter{csvWriter}
		w.logger.Warn("no_database_connection", slog.String("job_id", job.ID), slog.String("detail", "results will only be saved to CSV"))
	}
	matecfg, err := scrapemateapp.NewConfig(
		writersList,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	return scrapemateapp.NewScrapeMateApp(matecfg)
}

// uploadToS3AndSaveMetadata uploads a CSV file to S3 and saves metadata to the database
// Database record is only created AFTER successful S3 upload to avoid orphaned "uploading" records
func (w *webrunner) uploadToS3AndSaveMetadata(ctx context.Context, job *web.Job, csvFilePath string) error {
	// Skip if S3 is not configured
	if w.s3Uploader == nil || w.s3Bucket == "" {
		w.logger.Info("s3_not_configured_skipping_upload", slog.String("job_id", job.ID), slog.String("file_path", csvFilePath))
		return nil
	}

	w.logger.Info("s3_upload_starting", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))

	// Open the CSV file
	file, err := os.Open(csvFilePath)
	if err != nil {
		return fmt.Errorf("failed to open CSV file for upload: %w", err)
	}
	defer file.Close()

	// Get file size
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	fileSize := fileInfo.Size()

	// Construct S3 object key: users/{user_id}/jobs/{job_id}.csv
	objectKey := fmt.Sprintf("users/%s/jobs/%s.csv", job.UserID, job.ID)

	// Upload to S3 with proper Content-Type (including charset) and capture response
	// CRITICAL: Upload FIRST, then create database record only if upload succeeds
	result, err := w.s3Uploader.Upload(ctx, w.s3Bucket, objectKey, file, "text/csv; charset=utf-8")
	if err != nil {
		w.logger.Error("s3_upload_failed", slog.String("job_id", job.ID), slog.Any("error", err))
		return fmt.Errorf("S3 upload failed: %w", err)
	}

	// Capture ETag from S3 response
	w.logger.Info("s3_upload_successful", slog.String("job_id", job.ID), slog.String("bucket", w.s3Bucket), slog.String("key", objectKey), slog.Int64("size_bytes", fileSize), slog.String("etag", result.ETag))

	// Only NOW create database record after confirmed S3 upload success
	// This prevents orphaned "uploading" records if upload fails
	now := time.Now().UTC()
	jobFile := &models.JobFile{
		JobID:      job.ID,
		UserID:     job.UserID,
		FileType:   models.JobFileTypeCSV,
		BucketName: w.s3Bucket,
		ObjectKey:  objectKey,
		SizeBytes:  fileSize,
		MimeType:   "text/csv",
		Status:     models.JobFileStatusAvailable, // Directly available since upload succeeded
		ETag:       result.ETag,                   // Actual S3 ETag for integrity
		VersionID:  result.VersionID,              // S3 version if bucket versioning enabled
		CreatedAt:  now,
		UploadedAt: &now, // Upload just completed
	}

	// Save record to database with "available" status
	if err := w.jobFileRepo.Create(ctx, jobFile); err != nil {
		w.logger.Error("s3_db_record_creation_failed", slog.String("job_id", job.ID), slog.Any("error", err), slog.String("detail", "S3 upload succeeded but database record creation failed"))
		w.logger.Error("s3_orphaned_file", slog.String("job_id", job.ID), slog.String("s3_path", fmt.Sprintf("s3://%s/%s", w.s3Bucket, objectKey)), slog.String("etag", result.ETag))
		return fmt.Errorf("failed to create job file record after successful S3 upload: %w", err)
	}

	w.logger.Info("job_file_record_created", slog.String("job_id", job.ID), slog.String("etag", result.ETag))

	// Delete local CSV file after successful upload and database save
	if err := os.Remove(csvFilePath); err != nil {
		w.logger.Warn("local_csv_delete_failed", slog.String("job_id", job.ID), slog.Any("error", err))
		// Don't return error - upload and database save were successful, cleanup is not critical
	} else {
		w.logger.Info("local_csv_deleted", slog.String("job_id", job.ID))
	}

	return nil
}

// envInt reads an integer env var, returning defaultVal if unset or invalid.
func envInt(key string, defaultVal int) int {
	if s := os.Getenv(key); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return defaultVal
}

// envDuration reads a time.Duration env var (e.g. "5m", "30s"), returning
// defaultVal if unset or invalid.
func envDuration(key string, defaultVal time.Duration) time.Duration {
	if s := os.Getenv(key); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return defaultVal
}
