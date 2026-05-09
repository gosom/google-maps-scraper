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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/internal/crypto/aesutil"
	"github.com/gosom/google-maps-scraper/models"
	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/pkg/metrics"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/runner/webrunner/writers"
	"github.com/gosom/google-maps-scraper/s3uploader"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/google-maps-scraper/web"
	webservices "github.com/gosom/google-maps-scraper/web/services"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	"golang.org/x/sync/errgroup"
)

// mateResult carries the error from a mate.Start goroutine.
// The channel doubles as a completion signal, replacing a separate done channel.
type mateResult struct{ err error }

// maxLeakedMates is the upper bound on tracked leaked mate channels.
// When this limit is reached, the oldest entry is dropped to prevent
// unbounded slice growth.
const maxLeakedMates = 100

// leakTracker tracks leaked mate.Start goroutines so they can be joined
// (with a timeout) during Close(). All fields are goroutine-safe.
type leakTracker struct {
	// mu protects mates from concurrent access.
	mu sync.Mutex
	// mates tracks result channels of abandoned mate.Start goroutines.
	mates []<-chan mateResult
	// count tracks the total number of leaked mate goroutines for observability.
	// Incremented atomically each time a mate.Start goroutine cannot be joined.
	count atomic.Int64
}

// track registers the result channel of an abandoned mate.Start goroutine.
func (lt *leakTracker) track(resultCh <-chan mateResult, jobID string, logger *slog.Logger) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if len(lt.mates) >= maxLeakedMates {
		logger.Warn("leaked_mates_cap_reached",
			slog.String("job_id", jobID),
			slog.Int("cap", maxLeakedMates),
			slog.String("action", "dropping_oldest"),
		)
		copy(lt.mates, lt.mates[1:])
		lt.mates[len(lt.mates)-1] = nil
		lt.mates = lt.mates[:len(lt.mates)-1]
	}

	lt.mates = append(lt.mates, resultCh)
	total := lt.count.Add(1)
	logger.Warn("leaked_mate_tracked", slog.String("job_id", jobID), slog.Int("total_leaked", len(lt.mates)), slog.Int64("lifetime_leaked", total))
}

// drain joins all tracked leaked goroutines with a timeout.
func (lt *leakTracker) drain(timeout time.Duration, logger *slog.Logger) {
	lt.mu.Lock()
	leaked := lt.mates
	lt.mates = nil
	lt.mu.Unlock()

	if len(leaked) == 0 {
		return
	}

	logger.Info("draining_leaked_mates", slog.Int("count", len(leaked)))
	deadline := time.After(timeout)
	for i, ch := range leaked {
		select {
		case <-ch:
			logger.Debug("leaked_mate_joined", slog.Int("index", i))
		case <-deadline:
			logger.Warn("leaked_mate_drain_timeout", slog.Int("joined", i), slog.Int("remaining", len(leaked)-i))
			return
		}
	}
}

// lifecycle manages background goroutine tracking for graceful shutdown.
type lifecycle struct {
	// bgWg tracks background goroutines (stuck job reaper, webhook cleanup) so
	// they can be joined during graceful shutdown.
	bgWg sync.WaitGroup
}

type webrunner struct {
	srv                 *web.Server
	svc                 *web.Service
	cfg                 *runner.Config
	appCfg              *pkgconfig.Config
	db                  *sql.DB
	billingSvc          *billing.Service
	proxyURLs           []string     // upstream proxy URLs with creds; round-robin via proxyIndex
	proxyIndex          atomic.Int64 // round-robin counter, increments per job
	s3Uploader          *s3uploader.Uploader
	s3Bucket            string
	jobFileRepo         models.JobFileRepository
	webhookConfigRepo   models.WebhookConfigRepository
	webhookDeliveryRepo models.JobWebhookDeliveryRepository
	serverSecret        []byte // for deriving webhook KEK
	logger              *slog.Logger

	leaks leakTracker
	lc    lifecycle
}

// buildServerConfig constructs the web.ServerConfig from the typed *pkgconfig.Config
// and the runner's CLI-flag Config. Production validation was already performed by
// pkgconfig.Load() → Validate(), so no duplicate checks are needed here.
func buildServerConfig(cfg *runner.Config, db *sql.DB, svc *web.Service, appCfg *pkgconfig.Config, logger *slog.Logger) (web.ServerConfig, error) {
	stripeWebhookSecrets := appCfg.Stripe.WebhookSecrets()
	stripeWebhookAllowedCIDRs := appCfg.Stripe.WebhookAllowedCIDRs

	userRepo := postgres.NewUserRepository(db)
	apiKeyRepo := postgres.NewAPIKeyRepository(db)
	webhookConfigRepo := postgres.NewWebhookConfigRepository(db)
	webhookDeliveryRepo := postgres.NewJobWebhookDeliveryRepository(db)

	// Validate API_KEY_SERVER_SECRET: when API key auth is enabled (apiKeyRepo != nil),
	// an empty or short secret silently disables API key authentication in the auth
	// middleware (which checks len(serverSecret) > 0). Require ≥ 32 bytes to prevent
	// this silent misconfiguration trap (TOCTOU between "repo exists" and "secret exists").
	if apiKeyRepo != nil && len(appCfg.APIKeyServerSecret) < 32 {
		return web.ServerConfig{}, fmt.Errorf("API_KEY_SERVER_SECRET must be at least 32 bytes when API key auth is enabled (got %d bytes)", len(appCfg.APIKeyServerSecret))
	}

	serverCfg := web.ServerConfig{
		Service:                    svc,
		Addr:                       cfg.Addr,
		PgDB:                       db,
		UserRepo:                   userRepo,
		APIKeyRepo:                 apiKeyRepo,
		WebhookConfigRepo:          webhookConfigRepo,
		WebhookDeliveryRepo:        webhookDeliveryRepo,
		ServerSecret:               appCfg.APIKeyServerSecret,
		ClerkSecretKey:             appCfg.ClerkSecretKey,
		ClerkWebhookSigningSecrets: appCfg.ClerkWebhookSecrets(),
		StripeAPIKey:               appCfg.Stripe.SecretKey,
		StripeWebhookSecrets:       stripeWebhookSecrets,
		StripeWebhookAllowedCIDRs:  stripeWebhookAllowedCIDRs,
		Version:                    cfg.Version,
		InternalAddr:               appCfg.InternalAddr,
		ResendAPIKey:               appCfg.ResendAPIKey,
		Environment:                appCfg.AppEnv,
		Logger:                     logger,
		GoogleConfig:               appCfg.Google,
		EncryptionKey:              appCfg.EncryptionKey,
		AllowedOrigins:             appCfg.AllowedOrigins,
	}

	slog.Info("auth_enabled", slog.String("provider", "clerk"))
	if appCfg.Stripe.SecretKey != "" {
		slog.Info("payment_enabled", slog.String("provider", "stripe"))
	}
	if appCfg.Stripe.SecretKey != "" && len(stripeWebhookAllowedCIDRs) == 0 {
		slog.Warn("stripe_webhook_ip_allowlist_not_configured",
			slog.String("detail", "Configure STRIPE_WEBHOOK_ALLOWED_CIDRS and enforce the same Stripe CIDRs at the edge for defense in depth"),
		)
	}

	slog.Info("startup_config_summary",
		slog.Bool("stripe_enabled", appCfg.Stripe.SecretKey != ""),
		slog.Bool("stripe_webhook_ip_allowlist_configured", len(stripeWebhookAllowedCIDRs) > 0),
		slog.Bool("production_mode", appCfg.AppEnv.IsProduction()),
		slog.Bool("resend_enabled", serverCfg.ResendAPIKey != ""),
	)

	return serverCfg, nil
}

func New(cfg *runner.Config, appCfg *pkgconfig.Config, logger *slog.Logger) (runner.Runner, error) {
	if appCfg.DataFolder == "" {
		return nil, fmt.Errorf("data folder is required")
	}
	if cfg.Dsn == "" {
		return nil, fmt.Errorf("PostgreSQL DSN is required")
	}

	if err := os.MkdirAll(appCfg.DataFolder, os.ModePerm); err != nil {
		return nil, err
	}

	if !strings.Contains(cfg.Dsn, "sslmode=disable") {
		if strings.Contains(cfg.Dsn, "sslmode=require") && !strings.Contains(cfg.Dsn, "sslmode=verify") {
			logger.Warn("db_tls_not_verified",
				slog.String("detail", "sslmode=require encrypts but does not verify the server certificate — vulnerable to MITM. Use sslmode=verify-full with sslrootcert for production."),
			)
		}
	}

	var repo web.JobRepository
	db, err := sql.Open("pgx", cfg.Dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	// connection pool settings — read from typed config (defaults applied by pkg/config)
	if appCfg.DB.MaxOpenConns == 0 {
		return nil, fmt.Errorf("DB_MAX_OPEN_CONNS=0 creates unbounded pool — refusing to start")
	}
	db.SetMaxOpenConns(appCfg.DB.MaxOpenConns)
	db.SetMaxIdleConns(appCfg.DB.MaxIdleConns)
	db.SetConnMaxLifetime(appCfg.DB.ConnMaxLifetime)
	db.SetConnMaxIdleTime(appCfg.DB.ConnMaxIdleTime)

	metrics.RegisterDBPoolCollector(db, nil)

	// Startup validation: verify DB connectivity with a 10-second timeout before
	// the HTTP server starts accepting traffic. This ensures the container/process
	// fails fast and exits with code 1 rather than serving unhealthy requests.
	{
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pingCancel()
		if err := db.PingContext(pingCtx); err != nil {
			return nil, fmt.Errorf("startup DB ping failed (cannot reach PostgreSQL within 10s): %w", err)
		}
	}

	// Run database migrations using the direct PostgreSQL connection (not through
	// PgBouncer). Migrations use pg_advisory_lock which is session-level and
	// incompatible with PgBouncer Transaction mode.
	//
	// Production DSN format (DigitalOcean):
	//   DSN (app, via PgBouncer):      ...host:25061/pool?sslmode=verify-full&sslrootcert=/etc/brezel/secrets/do-ca.crt&default_query_exec_mode=simple_protocol
	//   MIGRATION_DSN (direct to PG):  ...host:25060/db?sslmode=verify-full&sslrootcert=/etc/brezel/secrets/do-ca.crt
	migrationDSN := appCfg.MigrationDSN
	if migrationDSN == "" {
		migrationDSN = cfg.Dsn
	}
	mr := postgres.NewMigrationRunner(migrationDSN, logger)
	if err := mr.RunMigrations(); err != nil {
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	repo, err = postgres.NewRepository(db, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL repository: %w", err)
	}

	svc := web.NewService(repo, appCfg.DataFolder)

	// Initialize server configuration (built below by buildServerConfig)

	// Build server config (enforces Clerk, applies Stripe if present)

	serverCfg, err := buildServerConfig(cfg, db, svc, appCfg, logger)
	if err != nil {
		return nil, err
	}

	// Create web server
	srv, err := web.New(serverCfg)
	if err != nil {
		return nil, err
	}

	// Initialize billing service for event charging (no Stripe required here).
	// userRepo is required by billing.New (S-C3) so checkout-path operations
	// can resolve users.stripe_customer_id; this background charging service
	// does not call CreateCheckoutSession but the constructor signature is
	// shared, so we wire a real repo rather than nil.
	cfgSvc := config.New(db)
	billUserRepo := postgres.NewUserRepository(db)
	billSvc := billing.New(db, cfgSvc, "", nil, billUserRepo, logger)
	if billSvc != nil {
		slog.Info("billing_service_initialized")
	} else {
		slog.Warn("billing_service_nil", slog.String("detail", "charges will not be applied"))
	}

	// Proxy URLs (round-robin per job). scrapemate v0.9.6+ runs its own
	// internal auth-handling local proxy, so we just pass the upstream URL
	// (with creds inline) and let scrapemate handle the playwright auth bug.
	if len(cfg.Proxy.Proxies) > 0 {
		slog.Info("proxies_configured", slog.Int("proxy_count", len(cfg.Proxy.Proxies)))
	}

	// Initialize S3 uploader if AWS credentials are configured
	var s3Upload *s3uploader.Uploader
	var s3BucketName string

	s3BucketName = appCfg.S3BucketName

	if appCfg.AWS.AccessKeyID != "" && appCfg.AWS.SecretAccessKey != "" && appCfg.AWS.Region != "" && s3BucketName != "" {
		var s3Err error
		s3Upload, s3Err = s3uploader.New(
			s3uploader.WithCredentials(appCfg.AWS.AccessKeyID, appCfg.AWS.SecretAccessKey),
			s3uploader.WithRegion(appCfg.AWS.Region),
			s3uploader.WithEndpoint(appCfg.AWS.Endpoint),
			s3uploader.WithForcePathStyle(appCfg.AWS.ForcePathStyle),
			s3uploader.WithServerSideEncryption(appCfg.AWS.SSEEnabled),
			s3uploader.WithChecksumMode(s3uploader.ParseChecksumMode(appCfg.AWS.ChecksumMode)),
			s3uploader.WithLogger(logger),
			// Mirrors pkg/metrics/billing.go callers: nil registerer →
			// prometheus.DefaultRegisterer; AlreadyRegisteredError is
			// tolerated so repeated construction is safe.
			s3uploader.WithMetrics(s3uploader.NewMetrics(nil)),
		)
		if s3Err != nil {
			slog.Warn("s3_uploader_init_failed", slog.String("detail", "files will only be stored locally"), slog.Any("error", s3Err))
		} else {
			slog.Info("s3_uploader_initialized", slog.String("bucket", s3BucketName), slog.String("region", appCfg.AWS.Region))
		}
	} else {
		slog.Info("s3_not_configured", slog.String("detail", "files will only be stored locally"))
		if appCfg.AWS.AccessKeyID == "" {
			slog.Info("s3_missing_env", slog.String("var", "AWS_ACCESS_KEY_ID"))
		}
		if appCfg.AWS.SecretAccessKey == "" {
			slog.Info("s3_missing_env", slog.String("var", "AWS_SECRET_ACCESS_KEY"))
		}
		if appCfg.AWS.Region == "" {
			slog.Info("s3_missing_env", slog.String("var", "AWS_REGION"))
		}
		if s3BucketName == "" {
			slog.Info("s3_missing_env", slog.String("var", "S3_BUCKET_NAME"))
		}
	}

	if appCfg.AppEnv.IsProduction() && s3Upload == nil {
		return nil, fmt.Errorf("S3 credentials are required when APP_ENV=production")
	}

	// HeadBucket preflight: fail fast on bad creds, typo'd bucket, or wrong
	// endpoint. In production this is fatal; in dev/staging we warn so local
	// runs without working S3 don't fail-loop on startup.
	if s3Upload != nil && s3BucketName != "" {
		preflightCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s3Upload.VerifyBucket(preflightCtx, s3BucketName); err != nil {
			if appCfg.AppEnv.IsProduction() {
				return nil, fmt.Errorf("S3 preflight failed: %w", err)
			}
			slog.Warn("s3_preflight_failed_dev_continuing", slog.Any("error", err))
		} else {
			slog.Info("s3_preflight_ok", slog.String("bucket", s3BucketName))
		}
	}

	slog.Info("startup_feature_summary",
		slog.Bool("s3_enabled", s3Upload != nil),
		slog.String("app_env", appCfg.AppEnv.String()),
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
		cookiesFile = appCfg.Google.CookiesFile
	}
	if cookiesFile != "" {
		gmaps.SetCookiesFile(cookiesFile)
		slog.Info("google_cookies_configured", slog.String("file", cookiesFile))
	} else {
		slog.Info("google_cookies_not_configured", slog.String("detail", "reviews may be restricted without authentication"))
	}

	ans := webrunner{
		srv:                 srv,
		svc:                 svc,
		cfg:                 cfg,
		appCfg:              appCfg,
		db:                  db,
		billingSvc:          billSvc,
		proxyURLs:           cfg.Proxy.Proxies,
		s3Uploader:          s3Upload,
		s3Bucket:            s3BucketName,
		jobFileRepo:         jobFileRepo,
		webhookConfigRepo:   serverCfg.WebhookConfigRepo,
		webhookDeliveryRepo: serverCfg.WebhookDeliveryRepo,
		serverSecret:        serverCfg.ServerSecret,
		logger:              logger,
	}

	return &ans, nil
}

func (w *webrunner) Run(ctx context.Context) error {
	egroup, ctx := errgroup.WithContext(ctx)

	// Start stuck-job reaper in its own goroutine.
	checkInterval := time.Duration(w.appCfg.StuckJobCheckIntervalMinutes) * time.Minute
	stuckTimeoutHours := w.appCfg.StuckJobTimeoutHours
	w.lc.bgWg.Add(1)
	go func() {
		defer w.lc.bgWg.Done()
		postgres.RunStuckJobReaper(ctx, w.db, w.logger, checkInterval, stuckTimeoutHours)
	}()

	// Start webhook delivery worker goroutine: polls for pending webhook
	// deliveries and sends HTTP callbacks to user-registered endpoints.
	if w.webhookDeliveryRepo != nil && w.webhookConfigRepo != nil {
		jobRepo, repoErr := postgres.NewRepository(w.db, w.logger)
		if repoErr != nil {
			w.logger.Error("webhook_worker_repo_init_failed", slog.Any("error", repoErr))
		} else {
			webhookKEK := aesutil.DeriveKey(w.serverSecret, "webhook-signing-key-encryption")
			w.lc.bgWg.Add(1)
			go func() {
				defer w.lc.bgWg.Done()
				worker := webservices.NewWebhookDeliveryWorker(
					w.webhookDeliveryRepo,
					w.webhookConfigRepo,
					jobRepo,
					webhookKEK,
					w.logger,
				)
				if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					w.logger.Error("webhook_delivery_worker_failed", slog.Any("error", err))
				}
			}()
		}
	}

	// Webhook event cleanup goroutine: removes processed_webhook_events older than
	// WebhookEventRetentionDays in daily batches.
	if w.billingSvc != nil {
		retentionDays := w.appCfg.WebhookEventRetentionDays
		w.lc.bgWg.Add(1)
		go func() {
			defer w.lc.bgWg.Done()
			w.billingSvc.StartWebhookEventCleanup(ctx, retentionDays)
		}()
	}

	// Log DB connection pool stats every 60s for monitoring.
	w.lc.bgWg.Add(1)
	go func() {
		defer w.lc.bgWg.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := w.db.Stats()
				w.logger.Info("db_pool_stats",
					slog.Int("open_connections", stats.OpenConnections),
					slog.Int("in_use", stats.InUse),
					slog.Int("idle", stats.Idle),
					slog.Int64("wait_count", stats.WaitCount),
					slog.Duration("wait_duration", stats.WaitDuration),
				)
			}
		}
	}()

	egroup.Go(func() error {
		return w.work(ctx)
	})

	egroup.Go(func() error {
		return w.srv.Start(ctx)
	})

	return egroup.Wait()
}

// trackLeakedMate registers the result channel of an abandoned mate.Start
// goroutine so it can be joined during Close().
func (w *webrunner) trackLeakedMate(resultCh <-chan mateResult, jobID string) {
	w.leaks.track(resultCh, jobID, w.logger)
}

// shutdownMate cancels the mate context, closes the mate with a timeout, and
// waits for the mate.Start goroutine to finish. Returns the error from
// mate.Start and whether the goroutine leaked.
func (w *webrunner) shutdownMate(jobID string, cancel context.CancelFunc, closeMate func(), resultCh <-chan mateResult) (error, bool) {
	cancel()

	closeComplete := make(chan struct{})
	go func() {
		defer close(closeComplete)
		closeMate()
	}()

	closeTimer := time.NewTimer(15 * time.Second)
	select {
	case <-closeComplete:
	case <-closeTimer.C:
		w.logger.Warn("mate_close_timeout", slog.String("job_id", jobID))
	}
	closeTimer.Stop()

	finalWait := time.NewTimer(5 * time.Second)
	select {
	case res := <-resultCh:
		finalWait.Stop()
		return res.err, false // not leaked
	case <-finalWait.C:
		w.trackLeakedMate(resultCh, jobID)
		return fmt.Errorf("mate goroutine leaked: timeout exceeded"), true
	}
}

func (w *webrunner) Close(context.Context) error {
	// Drain all leaked mate.Start goroutines with a timeout.
	w.leaks.drain(30*time.Second, w.logger)

	if w.db != nil {
		w.db.Close()
	}
	return nil
}

func (w *webrunner) work(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var wg sync.WaitGroup
	defer func() {
		drainDone := make(chan struct{})
		go func() { wg.Wait(); close(drainDone) }()
		select {
		case <-drainDone:
			w.logger.Info("in_flight_jobs_drained")
		case <-time.After(10 * time.Second):
			w.logger.Warn("in_flight_jobs_drain_timeout")
		}
	}()

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
					wg.Add(1)
					go func(job web.Job) {
						defer wg.Done()
						defer func() { <-jobSemaphore }() // Release semaphore when done
						defer func() {
							if r := recover(); r != nil {
								w.logger.Error("job_worker_panic_recovered",
									slog.String("job_id", job.ID),
									slog.Any("panic", r))
							}
						}()

						w.logger.Info("job_picked_up", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))

						t0 := time.Now().UTC()
						outcome := w.scrapeJob(ctx, &job)
						duration := time.Since(t0)

						params := map[string]any{
							"job_count": len(job.Data.Keywords),
							"duration":  duration.String(),
							"cause":     string(outcome.Cause),
						}
						if outcome.Err() != nil {
							params["error"] = outcome.Err().Error()
						}
						_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("web_runner", params))

						switch outcome.Status {
						case web.StatusCompleted:
							w.logger.Info("job_scrape_succeeded",
								slog.String("job_id", job.ID),
								slog.String("user_id", job.UserID),
								slog.Int("result_count", outcome.ResultCount),
								slog.String("cause", string(outcome.Cause)),
								slog.Duration("duration", duration),
							)
						case web.StatusCancelled:
							w.logger.Info("job_scrape_cancelled",
								slog.String("job_id", job.ID),
								slog.String("user_id", job.UserID),
								slog.Duration("duration", duration),
							)
						case web.StatusFailed:
							w.logger.Error("job_scrape_failed",
								slog.String("job_id", job.ID),
								slog.String("user_id", job.UserID),
								slog.String("cause", string(outcome.Cause)),
								slog.String("failure_reason", outcome.FailureReason),
								slog.Any("error", outcome.Err()),
								slog.Duration("duration", duration),
							)
						default:
							w.logger.Error("job_scrape_unknown_status",
								slog.String("job_id", job.ID),
								slog.String("status", string(outcome.Status)),
							)
						}
					}(jobs[i]) // Pass by value to avoid race condition
				default:
					// Semaphore full, skip this job for now (will be picked up in next tick)
					w.logger.Debug("job_skipped_max_concurrent", slog.String("job_id", jobs[i].ID), slog.Int("max_concurrent_jobs", maxConcurrentJobs))
				}
			}
		}
	}
}

func (w *webrunner) scrapeJob(ctx context.Context, job *web.Job) JobOutcome {
	// outcome holds the final result; defaults to a runtime error so any
	// unexpected early return surfaces as a failure rather than a silent success.
	outcome := OutcomeFailed(CauseRuntimeError, "job ended unexpectedly",
		errors.New("scrapeJob exited without classification"))
	job.Status = outcome.Status
	job.FailureReason = outcome.FailureReason
	// Always persist the final job status on exit
	defer func() {
		deferCtx, deferCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer deferCancel()
		if err := w.svc.Update(deferCtx, job); err != nil {
			w.logger.Error("job_status_persist_failed", slog.String("job_id", job.ID), slog.String("operation", "Update"), slog.Any("error", err))
			return
		}
		w.logger.Debug("job_status_persisted", slog.String("job_id", job.ID), slog.String("status", string(job.Status)))

		// Create webhook delivery rows for all active webhooks when job reaches a terminal status
		if job.Status == web.StatusCompleted || job.Status == web.StatusFailed || job.Status == web.StatusCancelled {
			if w.webhookConfigRepo != nil && w.webhookDeliveryRepo != nil {
				configs, whErr := w.webhookConfigRepo.ListActiveByUserID(deferCtx, job.UserID)
				if whErr != nil {
					w.logger.Error("webhook_list_configs_failed", slog.String("job_id", job.ID), slog.Any("error", whErr))
				} else if len(configs) > 0 {
					deliveries := make([]*models.JobWebhookDelivery, 0, len(configs))
					for _, cfg := range configs {
						deliveries = append(deliveries, &models.JobWebhookDelivery{
							JobID:           job.ID,
							WebhookConfigID: cfg.ID,
							Status:          models.DeliveryStatusPending,
						})
					}
					if whErr := w.webhookDeliveryRepo.CreateBatch(deferCtx, deliveries); whErr != nil {
						w.logger.Error("webhook_create_deliveries_failed", slog.String("job_id", job.ID), slog.Any("error", whErr))
					} else {
						w.logger.Info("webhook_deliveries_created", slog.String("job_id", job.ID), slog.Int("count", len(deliveries)))
					}
				}
			}
		}
	}()

	// Reset review circuit breaker for each new job
	gmaps.ResetReviewCircuitBreaker()

	// Charge job_start at job start (requires sufficient balance).
	// Admin jobs bypass billing entirely — they are internal operations.
	if job.Source == models.SourceAdmin {
		w.logger.Info("job_start_charge_skipped_admin_job",
			slog.String("job_id", job.ID),
			slog.String("user_id", job.UserID),
		)
	} else if w.billingSvc != nil {
		w.logger.Debug("job_start_charge_attempting", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
		jobStartCtx, jobStartCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := w.billingSvc.ChargeJobStart(jobStartCtx, job.UserID, job.ID)
		jobStartCancel() // release resources immediately
		if err != nil {
			outcome = OutcomeFailed(CauseRuntimeError, "insufficient credit balance to start job", err)
			job.Status = outcome.Status
			job.FailureReason = outcome.FailureReason
			w.logger.Error("job_start_charge_failed",
				slog.String("job_id", job.ID),
				slog.String("user_id", job.UserID),
				slog.String("job_name", job.Name),
				slog.String("failure_reason", job.FailureReason),
				slog.Any("error", err),
			)
			return outcome
		}
		w.logger.Info("job_start_charge_succeeded", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
	} else {
		w.logger.Warn("billing_service_nil", slog.String("job_id", job.ID), slog.String("detail", "skipping job_start charge"))
	}

	// Check if job has been cancelled before starting
	if job.Status == web.StatusCancelled || job.Status == web.StatusAborting {
		w.logger.Debug("job_already_cancelled", slog.String("job_id", job.ID))
		outcome = OutcomeUserCancelled()
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		return outcome
	}

	// Create a cancellable context for this job
	jobCtx, jobCancel := context.WithCancel(ctx)
	defer jobCancel()

	// Start a goroutine to monitor for cancellation
	go func() {
		defer func() {
			if r := recover(); r != nil {
				w.logger.Error("scrape_job_panic", slog.Any("panic", r), slog.String("job_id", job.ID))
			}
		}()
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

				// Stop monitoring if job has completed (any final status)
				if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled ||
					currentJob.Status == web.StatusCompleted || currentJob.Status == web.StatusFailed {
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

	job.Status = web.StatusRunning

	err := w.svc.Update(jobCtx, job)
	if err != nil {
		outcome = OutcomeFailed(CauseRuntimeError, "job initialization failed", err)
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		return outcome
	}

	if len(job.Data.Keywords) == 0 {
		outcome = OutcomeFailed(CauseRuntimeError, "no keywords provided", nil)
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		return outcome
	}

	outpath := filepath.Join(w.appCfg.DataFolder, job.ID+".csv")

	outfile, err := os.Create(outpath)
	if err != nil {
		outcome = OutcomeFailed(CauseRuntimeError, "failed to create output file", err)
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		return outcome
	}

	// Write UTF-8 BOM to ensure proper encoding detection in Excel and other programs
	// BOM = 0xEF, 0xBB, 0xBF
	if _, err := outfile.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		outfile.Close()
		outcome = OutcomeFailed(CauseRuntimeError, "failed to write output file header", fmt.Errorf("failed to write UTF-8 BOM: %w", err))
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		return outcome
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

	// Reset the review circuit breaker for this new job
	gmaps.ResetReviewCircuitBreaker()

	// Initialize deduper and exitMonitor before use
	dedup := deduper.New()
	exitMonitor := exiter.New()

	mate, err := w.setupMate(jobCtx, outfile, job, exitMonitor)
	if err != nil {
		outcome = OutcomeFailed(CauseRuntimeError, "job initialization failed", err)
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		w.logger.Error("setup_mate_failed",
			slog.String("job_id", job.ID),
			slog.String("user_id", job.UserID),
			slog.String("job_name", job.Name),
			slog.String("failure_reason", job.FailureReason),
			slog.Any("error", err),
		)
		return outcome
	}

	var closeOnce sync.Once
	closeMate := func() { closeOnce.Do(func() { mate.Close() }) }
	defer closeMate()

	var coords string
	if job.Data.Lat != "" && job.Data.Lon != "" {
		coords = job.Data.Lat + "," + job.Data.Lon
	}

	// Per-job total image budget. When MaxImages > 0, every PlaceJob in
	// this scrape job shares this counter; once exhausted, image extraction
	// is skipped for the remaining places (place metadata, reviews, and
	// contact details continue to scrape — only image extraction stops).
	// See gmaps.PlaceJob.extractImages for the enforcement logic.
	var imageBudget *atomic.Int64
	if job.Data.MaxImages > 0 {
		imageBudget = &atomic.Int64{}
		imageBudget.Store(int64(job.Data.MaxImages))
	}

	seedJobs, err := runner.CreateSeedJobs(runner.SeedJobConfig{
		FastMode:       job.Data.FastMode,
		LangCode:       job.Data.Language,
		Input:          strings.NewReader(strings.Join(job.Data.Keywords, "\n")),
		MaxDepth:       job.Data.Depth,
		IncludeEmails:  job.Data.IncludeEmails,
		Images:         job.Data.MaxImages > 0,
		ImageBudget:    imageBudget,
		Debug:          w.cfg.Debug,
		ReviewsMax:     job.Data.MaxReviews,
		GeoCoordinates: coords,
		Zoom:           job.Data.Zoom,
		Radius: func() float64 {
			if job.Data.Radius <= 0 {
				return 10000 // 10 km
			}
			return float64(job.Data.Radius)
		}(),
		Dedup:        dedup,
		ExitMonitor:  exitMonitor,
		ExtraReviews: job.Data.MaxReviews > 0,
		MaxResults:   job.Data.MaxResults,
	})
	if err != nil {
		outcome = OutcomeFailed(CauseRuntimeError, "job configuration failed", err)
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		w.logger.Error("create_seed_jobs_failed",
			slog.String("job_id", job.ID),
			slog.String("user_id", job.UserID),
			slog.String("job_name", job.Name),
			slog.String("failure_reason", job.FailureReason),
			slog.Any("error", err),
		)
		return outcome
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
			userMaxTime := int(job.Data.MaxTime.Duration().Seconds())
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

		// Inject our structured slog logger into scrapemate's context so all
		// scraper-level logs (page loads, retries, browser events) flow through
		// our slog pipeline with job_id correlation for Grafana/Loki.
		jobLogger := w.logger.With(slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
		mateCtx = scrapemate.ContextWithLogger(mateCtx, pkglogger.NewSlogAdapter(jobLogger))
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

		// Channel carries both the completion signal and the error from mate.Start,
		// eliminating the shared mateErr variable and the separate done channel.
		resultCh := make(chan mateResult, 1)

		go func() {
			defer func() {
				if r := recover(); r != nil {
					w.logger.Error("scrape_job_panic", slog.Any("panic", r), slog.String("job_id", job.ID))
					resultCh <- mateResult{err: fmt.Errorf("panic in mate.Start: %v", r)}
				}
			}()
			resultCh <- mateResult{err: mate.Start(mateCtx, seedJobs...)}
			w.logger.Debug("mate_start_goroutine_completed", slog.String("job_id", job.ID))
		}()

		// Wait for mate.Start to complete or for a backup timeout
		backupTimeout := time.NewTimer(time.Duration(allowedSeconds+60) * time.Second) // Increased buffer
		defer backupTimeout.Stop()

		// forcedCompletionCh fires 30s after the exit monitor signals completion.
		// This gives mate.Start a grace period to finish in-flight work before we force-kill it.
		forcedCompletionCh := make(chan struct{}, 1)

		go func() {
			defer func() {
				if r := recover(); r != nil {
					w.logger.Error("scrape_job_panic", slog.Any("panic", r), slog.String("job_id", job.ID))
				}
			}()
			select {
			case <-exitMonitorCompleted:
				w.logger.Debug("exit_monitor_completion_detected", slog.String("job_id", job.ID), slog.Int("forced_completion_timer_seconds", 30))
				// Wait 30s unconditionally — do NOT select on mateCtx.Done() here because
				// wrapperCancel already cancelled mateCtx, so it would exit immediately.
				time.Sleep(30 * time.Second)
				w.logger.Debug("forced_completion_timer_fired", slog.String("job_id", job.ID))
				select {
				case forcedCompletionCh <- struct{}{}:
				default:
				}
			case <-mateCtx.Done():
				// Context cancelled but not by exit monitor (e.g. timeout or parent cancel).
				// Still give mate a chance to finish, but shorter grace.
				w.logger.Debug("context_cancelled_not_exit_monitor", slog.String("job_id", job.ID))
			}
		}()

		select {
		case res := <-resultCh:
			// mate.Start completed normally
			err = res.err
			w.logger.Debug("mate_start_completed_normally", slog.String("job_id", job.ID), slog.Any("error", err))
		case <-backupTimeout.C:
			// Backup timeout - force completion
			w.logger.Debug("backup_timeout_triggered", slog.String("job_id", job.ID))
			err, _ = w.shutdownMate(job.ID, cancel, closeMate, resultCh)
			w.logger.Debug("forced_completion", slog.String("job_id", job.ID), slog.Any("error", err))
		case <-forcedCompletionCh:
			w.logger.Warn("mate_exit_monitor_forced_shutdown",
				slog.String("job_id", job.ID),
				slog.String("detail", "exit monitor detected completion, 30s grace elapsed, forcing shutdown"),
			)
			mateErr, leaked := w.shutdownMate(job.ID, cancel, closeMate, resultCh)
			w.logger.Info("shutdown_mate_result",
				slog.String("job_id", job.ID),
				slog.Bool("leaked", leaked),
				slog.Any("error", mateErr),
			)
			if leaked {
				var resultCount int
				if w.db != nil {
					countCtx, countCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if dbErr := w.db.QueryRowContext(countCtx, `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); dbErr != nil {
						w.logger.Error("result_count_after_leak_failed", slog.String("job_id", job.ID), slog.Any("error", dbErr))
					}
					countCancel()
					w.logger.Info("result_count_after_leak_check",
						slog.String("job_id", job.ID),
						slog.Int("result_count", resultCount),
						slog.Bool("will_treat_as_success", resultCount > 0),
					)
				} else {
					w.logger.Warn("db_nil_cannot_check_results", slog.String("job_id", job.ID))
				}
				if resultCount > 0 {
					w.logger.Info("goroutine_leaked_but_results_exist",
						slog.String("job_id", job.ID),
						slog.String("user_id", job.UserID),
						slog.Int("result_count", resultCount),
					)
					err = nil
				} else {
					err = mateErr
				}
			} else {
				err = mateErr
			}
		}

		w.logger.Debug("context_after_mate_start", slog.String("job_id", job.ID), slog.Any("context_err", mateCtx.Err()))

		// Detect user-initiated cancellation by reading the persisted job status.
		// classifyOutcome only consults this flag when mateErr is context.Canceled,
		// but we always populate it so the input to classification is well-defined.
		userInitiatedCancel := false
		if errors.Is(err, context.Canceled) {
			cancelCheckCtx, cancelCheckCancel := context.WithTimeout(context.Background(), 10*time.Second)
			currentJob, getErr := w.svc.Get(cancelCheckCtx, job.ID, "")
			cancelCheckCancel()
			if getErr != nil {
				// On lookup failure, conservatively assume user cancellation —
				// preserves the previous behavior of marking the job cancelled
				// rather than failed when we can't determine the cause.
				w.logger.Debug("status_check_after_cancel_failed", slog.String("job_id", job.ID), slog.Any("error", getErr))
				userInitiatedCancel = true
			} else {
				w.logger.Debug("status_after_context_cancellation", slog.String("job_id", job.ID), slog.String("status", string(currentJob.Status)))
				if currentJob.Status == web.StatusAborting || currentJob.Status == web.StatusCancelled {
					userInitiatedCancel = true
				}
			}
		}

		// Single result-count query feeds classification, sanity checks, and billing.
		// Replaces three separate QueryRowContext calls scattered through the old
		// err-tree; missing row count silently degrades to 0 (best-effort billing).
		resultCount := 0
		if w.db != nil {
			countCtx, countCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if dbErr := w.db.QueryRowContext(countCtx, `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount); dbErr != nil {
				w.logger.Debug("result_count_query_after_mate_failed", slog.String("job_id", job.ID), slog.Any("error", dbErr))
				resultCount = 0
			}
			countCancel()
		} else {
			w.logger.Error("database_connection_nil", slog.String("job_id", job.ID))
		}

		outcome = classifyOutcome(err, userInitiatedCancel, resultCount, exitMonitor.LastSeedError())
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		job.ResultCount = outcome.ResultCount
		jobSuccess = outcome.Status == web.StatusCompleted

		// Operational logging that the old err-tree emitted; preserved here so
		// Loki queries continue to work.
		switch {
		case err == nil:
			w.logger.Debug("job_normal_completion", slog.String("job_id", job.ID))
		case outcome.Status == web.StatusCancelled:
			w.logger.Debug("job_marked_cancelled_user_initiated", slog.String("job_id", job.ID))
		case outcome.Status == web.StatusFailed && outcome.Cause == CauseSeedExhausted:
			if seedErr := exitMonitor.LastSeedError(); seedErr != nil {
				w.logger.Error("job_failed_seed_error",
					slog.String("job_id", job.ID),
					slog.String("user_id", job.UserID),
					slog.String("user_facing_reason", job.FailureReason),
					slog.Any("raw_error", seedErr),
				)
			}
		case outcome.Status == web.StatusFailed && outcome.Cause == CauseRuntimeError:
			cancel()
			w.logger.Error("job_runtime_error",
				slog.String("job_id", job.ID),
				slog.String("user_id", job.UserID),
				slog.String("job_name", job.Name),
				slog.String("failure_reason", job.FailureReason),
				slog.Any("error", err),
			)
			return outcome
		}

		// Post-run sanity checks: ensure seeds completed and results were produced.
		// These can downgrade a "success" classification when scrapemate returned
		// nil but the seed/results bookkeeping disagrees. When that happens, we
		// rewrite outcome so the caller sees a coherent failure.
		seedCompleted, seedTotal := exitMonitor.GetSeedProgress()
		resultsWritten := exitMonitor.GetResultsWritten()
		if seedTotal > 0 && seedCompleted < seedTotal {
			w.logger.Debug("seeds_incomplete", slog.String("job_id", job.ID), slog.Int("completed", seedCompleted), slog.Int("total", seedTotal))
			if jobSuccess {
				reason := fmt.Sprintf("job partially completed (%d/%d searches finished)", seedCompleted, seedTotal)
				outcome = OutcomeFailed(CauseRuntimeError, reason, nil)
				job.Status = outcome.Status
				job.FailureReason = outcome.FailureReason
				jobSuccess = false
			}
		}
		if resultsWritten == 0 {
			w.logger.Warn("zero_results_written", slog.String("job_id", job.ID))
			if jobSuccess {
				outcome = OutcomeFailed(CauseRuntimeError, "0 results written", nil)
				job.Status = outcome.Status
				job.FailureReason = outcome.FailureReason
				jobSuccess = false
			}
		}

		w.logger.Debug("billing_section_entry", slog.String("job_id", job.ID), slog.Bool("job_success", jobSuccess), slog.String("status", string(job.Status)), slog.Bool("cancelled", job.Status == web.StatusCancelled))

		if jobSuccess && job.Status != web.StatusCancelled {
			w.logger.Debug("billing_condition_passed", slog.String("job_id", job.ID))
			w.logger.Debug("billing_check", slog.String("job_id", job.ID), slog.Bool("billing_svc_nil", w.billingSvc == nil), slog.Int("result_count", resultCount))

			if w.billingSvc != nil && resultCount > 0 && job.Source != models.SourceAdmin {
				// Charge ALL events in a single atomic transaction
				// This includes: places, reviews, images, and contact details
				// If any charge fails, ALL charges are rolled back (all-or-nothing)
				w.logger.Debug("billing_charge_attempting", slog.String("job_id", job.ID), slog.Int("result_count", resultCount), slog.String("user_id", job.UserID))

				chargeAllCtx, chargeAllCancel := context.WithTimeout(context.Background(), 30*time.Second)
				billingErr := w.billingSvc.ChargeAllJobEvents(chargeAllCtx, job.UserID, job.ID, resultCount)
				chargeAllCancel() // release resources immediately
				if billingErr != nil {
					w.logger.Error("billing_atomic_charge_failed",
						slog.String("job_id", job.ID),
						slog.String("user_id", job.UserID),
						slog.String("job_name", job.Name),
						slog.String("failure_reason", "billing processing failed"),
						slog.Any("error", billingErr),
					)
					outcome = OutcomeFailed(CauseRuntimeError, "billing processing failed", fmt.Errorf("billing failed: %w", billingErr))
					job.Status = outcome.Status
					job.FailureReason = outcome.FailureReason
					return outcome
				} else {
					w.logger.Info("billing_charge_succeeded", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
				}
			} else {
				if job.Source == models.SourceAdmin {
					w.logger.Info("billing_skipped_admin_job",
						slog.String("job_id", job.ID),
						slog.String("user_id", job.UserID),
						slog.Int("result_count", resultCount),
					)
				}
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

	closeMate()

	// CRITICAL: Close the CSV file handle before S3 upload or any file operations
	// This ensures all buffered data is flushed to disk and the file is fully written
	// For writable files, we must explicitly check Close() errors as they can indicate data loss
	w.logger.Debug("csv_file_closing", slog.String("job_id", job.ID))
	if err := outfile.Close(); err != nil {
		// File close errors can indicate I/O errors (EIO) meaning data was lost
		// This should fail the job to ensure data integrity
		outcome = OutcomeFailed(CauseRuntimeError, "failed to save results file", fmt.Errorf("failed to close CSV file: %w", err))
		job.Status = outcome.Status
		job.FailureReason = outcome.FailureReason
		w.logger.Error("csv_file_close_failed",
			slog.String("job_id", job.ID),
			slog.String("user_id", job.UserID),
			slog.String("job_name", job.Name),
			slog.String("failure_reason", job.FailureReason),
			slog.Any("error", err),
		)
		return outcome
	}
	fileClosed = true
	w.logger.Debug("csv_file_closed", slog.String("job_id", job.ID))

	// Determine final job status
	w.logger.Debug("determining_final_status", slog.String("job_id", job.ID), slog.Bool("job_success", jobSuccess), slog.String("current_status", string(job.Status)))

	if job.Status == web.StatusCancelled {
		w.logger.Debug("keeping_cancelled_status", slog.String("job_id", job.ID))
		// Keep the cancelled status
	} else if jobSuccess {
		job.Status = web.StatusCompleted
		w.logger.Debug("status_set_ok", slog.String("job_id", job.ID))

		// Upload CSV to S3 and save metadata if S3 is configured.
		// File is now fully closed and flushed to disk, safe to upload.
		// uploadToS3AndSaveMetadata logs the specific failure mode
		// (s3_upload_failed / s3_db_record_creation_failed) at Error with
		// bucket+object_key context. Per the single-handling-rule we do
		// NOT re-log the wrapped error here — emit a Warn audit line so
		// operators can still query "jobs kept successful despite S3
		// failure" without duplicating the Error.
		if err := w.uploadToS3AndSaveMetadata(ctx, job, outpath); err != nil {
			_ = err // already logged at Error inside uploadToS3AndSaveMetadata
			w.logger.Warn("s3_upload_skipped_job_kept_successful", slog.String("job_id", job.ID))
			// Don't fail the job due to S3 upload failure - the scraping was successful
			// The CSV file will remain on local storage
		}
	} else {
		job.Status = web.StatusFailed
		w.logger.Debug("status_set_failed", slog.String("job_id", job.ID))
	}

	// Charging of places is attempted before marking success above; no charge here

	return outcome
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

	w.logger.Debug("job_concurrency_configured", slog.String("job_id", job.ID), slog.Int("per_job_concurrency", perJobConcurrency), slog.Int("total_concurrency", w.cfg.Concurrency), slog.Int("max_concurrent_jobs", maxConcurrentJobs))

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

	// Handle proxy configuration: round-robin assign one upstream proxy per
	// job. scrapemate v0.9.6+ runs an internal local proxy that handles
	// playwright's authenticated-proxy bug, so passing the upstream URL
	// directly (with creds inline) is the correct shape.
	if n := len(w.proxyURLs); n > 0 {
		idx := int(w.proxyIndex.Add(1)-1) % n
		opts = append(opts, scrapemateapp.WithProxies([]string{w.proxyURLs[idx]}))
		w.logger.Debug("proxy_assigned", slog.String("job_id", job.ID), slog.Int("index", idx+1), slog.Int("of", n))
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

	if !w.cfg.Scraping.DisablePageReuse {
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
		w.logger.Debug("sync_dual_writer_added", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
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

	w.logger.Debug("s3_upload_starting", slog.String("job_id", job.ID), slog.String("user_id", job.UserID))

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

	// Upload to S3 with proper Content-Type (including charset) and capture response.
	// CRITICAL: Upload FIRST, then create database record only if upload succeeds.
	// 2-minute per-call timeout caps the worst case for hung S3 connections so a
	// single bad request can't tie up a goroutine indefinitely (CSVs are MB-scale).
	uploadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := w.s3Uploader.Upload(uploadCtx, w.s3Bucket, objectKey, file, "text/csv; charset=utf-8")
	if err != nil {
		w.logger.Error("s3_upload_failed",
			slog.String("job_id", job.ID),
			slog.String("bucket", w.s3Bucket),
			slog.String("object_key", objectKey),
			slog.Any("error", err))
		return fmt.Errorf("S3 upload failed: %w", err)
	}

	// Capture ETag from S3 response. Debug-level: s3uploader already logs at
	// debug, this carries job_id for correlation. Net: zero Info per success.
	w.logger.Debug("s3_upload_successful", slog.String("job_id", job.ID), slog.String("bucket", w.s3Bucket), slog.String("key", objectKey), slog.Int64("size_bytes", fileSize))

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
		w.logger.Error("s3_orphaned_file", slog.String("job_id", job.ID), slog.String("s3_path", fmt.Sprintf("s3://%s/%s", w.s3Bucket, objectKey)))
		return fmt.Errorf("failed to create job file record after successful S3 upload: %w", err)
	}

	w.logger.Debug("job_file_record_created", slog.String("job_id", job.ID))

	// Delete local CSV file after successful upload and database save
	if err := os.Remove(csvFilePath); err != nil {
		w.logger.Warn("local_csv_delete_failed", slog.String("job_id", job.ID), slog.Any("error", err))
		// Don't return error - upload and database save were successful, cleanup is not critical
	} else {
		w.logger.Debug("local_csv_deleted", slog.String("job_id", job.ID))
	}

	return nil
}
