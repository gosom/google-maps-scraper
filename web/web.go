package web

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/internal/crypto/aesutil"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/appenv"
	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
	"github.com/gosom/google-maps-scraper/pkg/encryption"
	"github.com/gosom/google-maps-scraper/pkg/googlesheets"
	"github.com/gosom/google-maps-scraper/pkg/notify"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/web/auth"
	webhandlers "github.com/gosom/google-maps-scraper/web/handlers"
	webmiddleware "github.com/gosom/google-maps-scraper/web/middleware"
	webservices "github.com/gosom/google-maps-scraper/web/services"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

//go:embed static
var static embed.FS

type Server struct {
	tmpl           map[string]*template.Template
	srv            *http.Server
	internalSrv    *http.Server
	svc            *Service
	authMiddleware *auth.AuthMiddleware
	userRepo       postgres.UserRepository
	billingSvc     *billing.Service
	db             *sql.DB
	logger         *slog.Logger
}

type ServerConfig struct {
	Service             *Service
	Addr                string
	PgDB                *sql.DB // Optional PostgreSQL connection
	UserRepo            postgres.UserRepository
	APIKeyRepo          models.APIKeyRepository             // Optional; enables API key auth when set
	WebhookConfigRepo   models.WebhookConfigRepository      // Optional; enables webhook config management
	WebhookDeliveryRepo models.JobWebhookDeliveryRepository // Optional; enables webhook delivery tracking
	ServerSecret        []byte                              // HMAC secret for API key HMAC (from API_KEY_SERVER_SECRET env)
	ClerkSecretKey      string                              // Clerk server-side secret key for authentication
	// ClerkWebhookSigningSecrets holds the Svix signing secrets for the Clerk
	// Dashboard webhook endpoint. The active secret must be first; any previous
	// secrets (during rotation) follow. All are tried in order. An empty slice
	// disables /webhooks/clerk (route not mounted). Mirrors StripeWebhookSecrets.
	ClerkWebhookSigningSecrets []string
	StripeAPIKey               string   // Optional Stripe API key for subscriptions
	StripeWebhookSecrets       []string // Active first, then previous webhook secrets during rotation
	// StripeWebhookAllowedCIDRs is an optional defense-in-depth allowlist for
	// the Stripe webhook receiver. This should complement, not replace, edge
	// firewall allowlisting because reverse proxies may mask the original peer IP.
	StripeWebhookAllowedCIDRs []string
	// Version is the Git SHA injected at build time via ldflags.
	// It is returned by the /health endpoint as the "version" field.
	Version string
	// InternalAddr is the listen address for the internal HTTP server that
	// serves /metrics and /health. Keep this off the public interface to
	// avoid exposing Prometheus metrics to unauthenticated clients (CWE-200).
	// Example: ":9090". If empty, no internal listener is created.
	InternalAddr string
	ResendAPIKey string // Optional; if empty, support requests are logged instead of emailed
	// Environment is parsed once from APP_ENV at startup (see pkg/appenv)
	// and propagated through Dependencies into handlers. Replaces the
	// previous APP_ENV / IS_PRODUCTION env-read pattern at handler layer.
	Environment appenv.Environment
	// Logger is the root structured logger injected from main via pkglogger.New.
	// New() derives a per-component child via logger.With("component", "api").
	Logger *slog.Logger
	// GoogleConfig holds Google OAuth credentials read once at startup.
	// Passed through to IntegrationHandler so per-request os.Getenv is eliminated.
	GoogleConfig pkgconfig.GoogleConfig
	// EncryptionKey is read once from pkg/config at startup. Eliminates
	// the direct os.Getenv("ENCRYPTION_KEY") call inside web.New.
	EncryptionKey string
	// AllowedOrigins is read once from pkg/config at startup. Eliminates
	// the direct os.Getenv("ALLOWED_ORIGINS") call inside web.New.
	AllowedOrigins []string
	// InternalHandlers is an extension point for the internal listener.
	// Callers populate this with diagnostic endpoints they want exposed on
	// 9090 (alongside /health and /metrics). The webrunner registers
	// /internal/proxy/stats through here so web.go doesn't need to import
	// proxypool — keeps web/ free of scraping-internal coupling.
	//
	// Paths MUST be unique across all callers. Internally we use
	// http.ServeMux.Handle which panics on duplicate registration; map
	// iteration order is non-deterministic so a duplicate path would
	// also have undefined "which one wins" semantics. Document and
	// enforce uniqueness at the caller boundary if multiple sources of
	// handlers ever need to compose. Currently registered paths (V1):
	//   /internal/proxy/stats — proxypool snapshot, from webrunner.
	InternalHandlers map[string]http.Handler
}

func New(cfg ServerConfig) (*Server, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ans := Server{
		svc:      cfg.Service,
		tmpl:     make(map[string]*template.Template),
		db:       cfg.PgDB,
		userRepo: cfg.UserRepo,
		// "module" rather than "component" so we don't overwrite the
		// runner-level component tag (set in main.go). Pre-2026-05-10
		// every log record had two `component` fields — the runner's
		// and this one — and most aggregators silently keep only the
		// last, so dashboards filtering on component=webrunner went blind.
		logger: logger.With(slog.String("module", "api")),
		srv: &http.Server{
			Addr:              cfg.Addr,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
	}

	// Initialize billing service first so the user-provisioning service can
	// receive it as a dependency for lazy Stripe Customer creation. billingSvc
	// may remain nil when no Stripe API key is configured; UserProvisioning
	// tolerates nil and skips Stripe Customer creation (the next checkout will
	// lazy-create via CreateCheckoutSession instead).
	if cfg.StripeAPIKey != "" && cfg.PgDB != nil {
		cfgSvc := config.New(cfg.PgDB)
		ans.billingSvc = billing.New(cfg.PgDB, cfgSvc, cfg.StripeAPIKey, cfg.StripeWebhookSecrets, cfg.UserRepo, ans.logger)
	}

	// Initialize the user-provisioning service if we have the DB + user repo.
	// Used by both the auth-middleware lazy fallback and the Clerk webhook
	// handler — kept at function scope so both surfaces share one instance.
	var provisioningSvc *webservices.UserProvisioning
	if cfg.PgDB != nil && cfg.UserRepo != nil {
		provisioningSvc = webservices.NewUserProvisioning(cfg.PgDB, cfg.UserRepo, ans.billingSvc, ans.logger)
	}

	// Initialize authentication middleware if Clerk secret key is provided.
	if cfg.ClerkSecretKey != "" && cfg.UserRepo != nil {
		var err error
		ans.authMiddleware, err = auth.NewAuthMiddleware(cfg.ClerkSecretKey, cfg.UserRepo, cfg.APIKeyRepo, cfg.ServerSecret, provisioningSvc, ans.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize authentication: %w", err)
		}
	}

	staticFS, err := fs.Sub(static, "static")
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(staticFS))
	router := mux.NewRouter()

	// Initialize encryption once at startup (key injected via ServerConfig, not os.Getenv)
	enc, err := encryption.New(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("encryption init failed: %w", err)
	}
	if enc == nil {
		slog.Warn("encryption_key_missing", slog.String("detail", "integration credentials will be stored in plaintext"))
	}

	// Initialize modular handler group (incremental migration)
	deps := webhandlers.Dependencies{
		Logger:              ans.logger,
		DB:                  ans.db,
		BillingSvc:          ans.billingSvc,
		Templates:           ans.tmpl,
		Auth:                ans.authMiddleware,
		App:                 ans.svc,
		UserRepo:            ans.userRepo,
		APIKeyRepo:          cfg.APIKeyRepo,
		WebhookConfigRepo:   cfg.WebhookConfigRepo,
		WebhookDeliveryRepo: cfg.WebhookDeliveryRepo,
		ServerSecret:        cfg.ServerSecret,
		WebhookKEK:          aesutil.DeriveKey(cfg.ServerSecret, "webhook-signing-key-encryption"),
		PricingRuleRepo:     postgres.NewPricingRuleRepository(ans.db),
		ResultsSvc:          webservices.NewResultsService(ans.db, ans.logger),
		Encryptor:           enc,
		IntegrationRepo:     postgres.NewIntegrationRepository(ans.db, enc),
		GoogleSheetsSvc:     googlesheets.NewService(),
		Version:             cfg.Version,
		Environment:         cfg.Environment,
		GoogleConfig:        cfg.GoogleConfig,
	}
	if ans.db != nil {
		deps.ConcurrentLimitSvc = webservices.NewConcurrentLimitService(ans.db)
	}

	// Support email sender: Resend if configured, log fallback otherwise
	var supportSender notify.Sender
	if cfg.ResendAPIKey != "" {
		supportSender = notify.NewResendSender(
			cfg.ResendAPIKey,
			"BrezelScraper Support <noreply@brezel.ai>",
			"support@brezel.ai",
		)
	} else {
		supportSender = &notify.LogSender{Logger: ans.logger}
	}
	deps.Sender = supportSender

	hg := webhandlers.NewHandlerGroup(deps)

	// Health check endpoint (no authentication needed)
	router.HandleFunc("/health", hg.Web.HealthCheck).Methods(http.MethodGet)

	// Prometheus metrics and a secondary health check are served on a separate
	// internal listener (InternalAddr) so they are never exposed on the public
	// port. See Start() for the goroutine that runs the internal server.
	if cfg.InternalAddr != "" {
		internalMux := http.NewServeMux()
		internalMux.Handle("/metrics", promhttp.Handler())
		internalMux.HandleFunc("/health", hg.Web.HealthCheck)
		// Register caller-supplied diagnostic handlers (e.g. proxy pool
		// stats from webrunner). Skips empty/nil entries so a nil map or
		// zero-value Config still works.
		for path, h := range cfg.InternalHandlers {
			if path == "" || h == nil {
				continue
			}
			internalMux.Handle(path, h)
		}
		ans.internalSrv = &http.Server{
			Addr:              cfg.InternalAddr,
			Handler:           internalMux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
		}
	}

	// Version endpoint (no authentication needed, for monitoring and debugging)
	router.HandleFunc("/api/version", hg.Version.GetVersion).Methods(http.MethodGet)
	router.HandleFunc("/api/v1/version", hg.Version.GetVersion).Methods(http.MethodGet)

	// Static files
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fileServer))

	// Web UI routes
	router.HandleFunc("/", hg.Web.Index).Methods(http.MethodGet)

	// API documentation (public access)
	router.HandleFunc("/api/docs", hg.Web.Redoc).Methods(http.MethodGet)

	// Public API routes (no authentication required)
	publicAPIRouter := router.PathPrefix("/api/v1").Subrouter()
	publicAPIRouter.Use(
		webmiddleware.MaxBodySize(1<<20),                // 1 MB max body (CWE-400)
		webmiddleware.PerIPRateLimit(rate.Limit(3), 10), // 3 req/s per IP, burst 10 (CWE-307)
		webmiddleware.RequestID,
		webmiddleware.InjectLogger(ans.logger),
		webmiddleware.RequestLogger(ans.logger),
	)

	// API routes with authentication if available
	apiRouter := router.PathPrefix("/api/v1").Subrouter()

	// Apply authentication middleware if available
	if ans.authMiddleware != nil {
		apiRouter.Use(ans.authMiddleware.Authenticate)
	}

	// Apply request ID, logger injection, and request logger after authentication
	// so user_id is available in context
	apiRouter.Use(
		webmiddleware.MaxBodySize(1<<20), // 1 MB max body (CWE-400)
		// API key: free=2 req/s burst 5, paid=10 req/s burst 30; session fallback=5 req/s burst 20 (CWE-307)
		webmiddleware.PerAPIKeyRateLimit(rate.Limit(2), 5, rate.Limit(10), 30, rate.Limit(5), 20),
		// Write IETF + legacy RateLimit-* response headers on the success
		// path. Must sit AFTER the limiter so the snapshot is in context;
		// the 429 path writes the same header set inline from the
		// dispatcher (see middleware.rateLimitJSON).
		webmiddleware.RateLimitHeaders(),
		webmiddleware.RequestID,
		webmiddleware.InjectLogger(ans.logger),
		webmiddleware.RequestLogger(ans.logger),
	)

	// API endpoints (these are protected by middleware if enabled).
	//
	// Per-endpoint rate limit on POST /api/v1/jobs (Task 3.6):
	// the global PerAPIKeyRateLimit on apiRouter caps every endpoint at
	// the same rate (free 2/s burst 5, paid 10/s burst 30, session
	// fallback 5/s burst 20). For job CREATION specifically — billable,
	// takes a SELECT ... FOR UPDATE row lock, queues a scraping worker —
	// that ceiling is too lenient. A single authenticated user could fire
	// 30 paid-tier requests per second and exhaust their credit balance,
	// flood the worker queue, or hold the user-row lock under contention.
	//
	// This tighter limiter wraps ONLY the POST /jobs route on top of the
	// existing middleware chain. Burst 3 lets a human-driven UI submit
	// a small batch of jobs without hitting the limit; the 1 req/s
	// refill keeps any sustained automation honest.
	jobCreateLimiter := webmiddleware.PerUserRateLimit(rate.Limit(1), 3)
	// Idempotency middleware (Task 7.2): wraps POST /api/v1/jobs only.
	// Implements the Stripe two-phase pattern — concurrent network
	// retries carrying the same Idempotency-Key header are deduplicated
	// at the storage layer (UNIQUE (user_id, key) is the lock), so a
	// double-click or a flaky-network retry cannot double-bill the
	// user. The middleware is opt-in per-request: requests without the
	// header pass through unchanged. See web/middleware/idempotency.go
	// for the design rationale and the concurrency test that pins it.
	jobIdempotency := webmiddleware.Idempotency(postgres.NewIdempotencyRepository(ans.db), ans.logger)
	apiRouter.HandleFunc("/jobs", hg.API.ListJobs).Methods(http.MethodGet)
	apiRouter.Handle("/jobs", jobIdempotency(jobCreateLimiter(http.HandlerFunc(hg.API.Scrape)))).Methods(http.MethodPost)
	apiRouter.HandleFunc("/jobs/{id}", hg.API.GetJob).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}", hg.API.DeleteJob).Methods(http.MethodDelete)
	apiRouter.HandleFunc("/jobs/{id}/cancel", hg.API.CancelJob).Methods(http.MethodPost)
	apiRouter.HandleFunc("/jobs/{id}/download", hg.Web.Download).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}/download-url", hg.Web.DownloadURL).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}/results", hg.API.GetJobResults).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}/costs", hg.API.GetJobCosts).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/costs/batch", hg.API.GetBatchJobCosts).Methods(http.MethodPost)
	apiRouter.HandleFunc("/jobs/estimates", hg.API.EstimateJobCost).Methods(http.MethodPost)
	apiRouter.HandleFunc("/results", hg.API.GetUserResults).Methods(http.MethodGet)
	apiRouter.HandleFunc("/dashboard", hg.API.GetDashboard).Methods(http.MethodGet)

	// API key management endpoints
	if cfg.APIKeyRepo != nil {
		apiRouter.HandleFunc("/api-keys", hg.APIKey.ListAPIKeys).Methods(http.MethodGet)
		apiRouter.HandleFunc("/api-keys", hg.APIKey.CreateAPIKey).Methods(http.MethodPost)
		apiRouter.HandleFunc("/api-keys/{id}", hg.APIKey.RevokeAPIKey).Methods(http.MethodDelete)
	}

	// Webhook config management endpoints
	if cfg.WebhookConfigRepo != nil {
		apiRouter.HandleFunc("/webhooks", hg.Webhook.ListWebhooks).Methods(http.MethodGet)
		apiRouter.HandleFunc("/webhooks", hg.Webhook.CreateWebhook).Methods(http.MethodPost)
		apiRouter.HandleFunc("/webhooks/{id}", hg.Webhook.UpdateWebhook).Methods(http.MethodPatch)
		apiRouter.HandleFunc("/webhooks/{id}", hg.Webhook.RevokeWebhook).Methods(http.MethodDelete)
	}

	// Integration endpoints (authenticated). The Google OAuth callback is
	// terminated on the Next.js frontend (so Clerk's host-only __session
	// cookie remains in scope); the frontend then POSTs {code} here with
	// the user's Clerk JWT as a Bearer token.
	apiRouter.HandleFunc("/integrations/google/callback", hg.Integration.HandleGoogleCallback).Methods(http.MethodPost)
	apiRouter.HandleFunc("/integrations/google/status", hg.Integration.HandleGetStatus).Methods(http.MethodGet)
	apiRouter.HandleFunc("/integrations/config", hg.Integration.HandleGetConfig).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}/export/google-sheets", hg.Integration.HandleExportJob).Methods(http.MethodPost)

	// Credit endpoints (if billing service is available)
	if ans.billingSvc != nil {
		apiRouter.HandleFunc("/credits/balance", hg.Billing.GetCreditBalance).Methods(http.MethodGet)
		apiRouter.HandleFunc("/credits/history", hg.Billing.GetBillingHistory).Methods(http.MethodGet)
		apiRouter.HandleFunc("/credits/checkout-session", hg.Billing.CreateCheckoutSession).Methods(http.MethodPost)
		apiRouter.HandleFunc("/credits/reconcile", hg.Billing.Reconcile).Methods(http.MethodPost)
	}

	// Support endpoint: per-user rate limit of 1 req/min, burst 2.
	// Prevents support-form spam while allowing a quick retry after a typo fix.
	supportLimiter := webmiddleware.PerUserRateLimit(rate.Limit(1.0/60.0), 2)
	apiRouter.Handle("/support",
		supportLimiter(http.HandlerFunc(hg.Support.SubmitSupportRequest)),
	).Methods(http.MethodPost)

	// ─── Admin routes ────────────────────────────────────────────────────
	// Admin routes are isolated in their own namespace and protected by
	// RequireRole middleware. API key auth is explicitly rejected in each
	// handler as defense-in-depth.
	adminRouter := apiRouter.PathPrefix("/admin").Subrouter()
	adminRouter.Use(webmiddleware.RequireRole(models.RoleAdmin))

	adminRouter.HandleFunc("/jobs", hg.Admin.CreateJob).Methods(http.MethodPost)
	adminRouter.HandleFunc("/jobs", hg.Admin.GetJobs).Methods(http.MethodGet)
	adminRouter.HandleFunc("/jobs/{id}/cancel", hg.Admin.CancelJob).Methods(http.MethodPost)

	// Webhook endpoints are public provider callbacks, not customer API routes.
	// Keep them out of the /api/v1 customer namespace and give them a dedicated
	// middleware chain rather than inheriting generic public API rate limits.
	//
	// H3: Per-IP rate limit applied here to both Clerk and Stripe webhooks.
	// Svix retries at most ~1/min/event; Stripe bursts are brief and per-IP.
	// 20 req/s with burst 50 per IP is far above legitimate delivery rates
	// and well below flood throughput. Because limits are per-IP, Clerk and
	// Stripe IPs each have independent buckets.
	baseWebhookMws := []func(http.Handler) http.Handler{
		webmiddleware.PerIPRateLimit(rate.Limit(20), 50),
		webmiddleware.RequestID,
		webmiddleware.InjectLogger(ans.logger),
		webmiddleware.RequestLogger(ans.logger),
		webmiddleware.MaxBodySize(64 << 10), // Webhook payloads are small; cap memory use.
	}

	// Build the Clerk webhook handler from the base middleware chain. Mounted
	// only when signing secrets are configured AND the provisioning service is
	// available. The handler verifies Svix signatures itself and is intentionally
	// NOT behind any CIDR allowlist (Clerk uses Cloudflare, not fixed CIDRs).
	// H4: accepts a slice so the previous secret stays valid during rotation.
	switch {
	case len(cfg.ClerkWebhookSigningSecrets) > 0 && provisioningSvc != nil:
		clerkHandler, err := webhandlers.NewClerkWebhookHandler(cfg.PgDB, cfg.ClerkWebhookSigningSecrets, provisioningSvc, ans.logger)
		if err != nil {
			return nil, fmt.Errorf("clerk webhook handler init: %w", err)
		}
		clerkWebhookHandler := webmiddleware.Chain(clerkHandler, baseWebhookMws...)
		router.Handle("/webhooks/clerk", clerkWebhookHandler).Methods(http.MethodPost)
	case len(cfg.ClerkWebhookSigningSecrets) > 0:
		// Secrets configured but provisioning unavailable (DB or user repo
		// nil) — surface this loudly so operators don't silently lose
		// webhook deliveries waiting for a route that was never mounted.
		ans.logger.Warn("clerk_webhook_route_not_mounted",
			slog.String("reason", "provisioning service unavailable (db or user repo is nil)"))
	}

	// Stripe webhook chain layers an optional CIDR allowlist on top of the
	// base. Use an explicit copy (NOT slice-header aliasing) so a future
	// append into stripeWebhookMws cannot bleed into baseWebhookMws if the
	// base ever grows past its initial capacity.
	stripeWebhookMws := append([]func(http.Handler) http.Handler{}, baseWebhookMws...)
	if len(cfg.StripeWebhookAllowedCIDRs) > 0 {
		cidrMW, err := webmiddleware.AllowCIDRs(cfg.StripeWebhookAllowedCIDRs)
		if err != nil {
			return nil, fmt.Errorf("invalid Stripe webhook CIDR allowlist: %w", err)
		}
		stripeWebhookMws = append(stripeWebhookMws, cidrMW)
	}
	webhookHandler := webmiddleware.Chain(http.HandlerFunc(hg.Billing.HandleStripeWebhook), stripeWebhookMws...)
	// goneHandler is returned on retired paths to surface Stripe dashboard
	// misconfiguration quickly.
	goneHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "This endpoint has been retired. Configure your Stripe dashboard to POST to /webhooks/stripe", http.StatusGone)
	})
	if ans.billingSvc != nil {
		// Canonical Stripe callback path. This route is intentionally separate
		// from the documented customer API surface.
		router.Handle("/webhooks/stripe", webhookHandler).Methods(http.MethodPost)

		// Retired paths — respond 410 Gone so any stale provider configuration
		// surfaces as an obvious delivery failure instead of silently drifting.
		router.Handle("/api/v1/billing/webhook", goneHandler).Methods(http.MethodPost)
		router.Handle("/api/stripe/webhook", goneHandler).Methods(http.MethodPost)
	}

	// Apply security headers and CORS to all routes via middleware chain.
	// AllowedOrigins is injected via ServerConfig (parsed once at startup from
	// ALLOWED_ORIGINS by pkg/config). If empty, only localhost origins are allowed
	// (safe development default).
	handler := webmiddleware.Chain(router, webmiddleware.Recovery(ans.logger), webmiddleware.SecurityHeaders, webmiddleware.NewCORS(cfg.AllowedOrigins))
	ans.srv.Handler = handler

	tmplsKeys := []string{
		"static/templates/index.html",
		"static/templates/job_rows.html",
		"static/templates/job_row.html",
		"static/templates/redoc.html",
	}

	for _, key := range tmplsKeys {
		tmp, err := template.ParseFS(static, key)
		if err != nil {
			return nil, err
		}

		ans.tmpl[key] = tmp
	}

	return &ans, nil
}

func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()

		// Shut down the internal server first so Prometheus stops scraping
		// before the main server begins draining connections.
		if s.internalSrv != nil {
			if err := s.internalSrv.Shutdown(shutdownCtx); err != nil {
				s.logger.Error("internal_server_shutdown_error", slog.Any("error", err))
			} else {
				s.logger.Info("internal_server_stopped")
			}
		}

		err := s.srv.Shutdown(shutdownCtx)
		if err != nil {
			s.logger.Error("server_shutdown_error", slog.Any("error", err))
			return
		}

		s.logger.Info("server_stopped")
	}()

	// Launch the idempotency-keys cleanup goroutine (Task 7.2 step 5).
	// Sweeps every 5 minutes; reaps completed rows past the 24h TTL and
	// 'started' rows older than 15 minutes (rows whose handler crashed
	// or was killed before Complete fired). The goroutine exits when
	// ctx is cancelled by the shutdown sequence above. We require a non-nil
	// db here — without it the middleware would have no place to write
	// reservations either, so this is the same precondition as POST /jobs
	// itself.
	if s.db != nil {
		go webmiddleware.RunIdempotencyCleanup(
			ctx,
			postgres.NewIdempotencyRepository(s.db),
			5*time.Minute,
			15*time.Minute,
			s.logger,
		)
	}

	// Launch internal server (metrics + health) in a background goroutine.
	if s.internalSrv != nil {
		go func() {
			s.logger.Info("internal_server_started", slog.String("addr", s.internalSrv.Addr))

			if err := s.internalSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Error("internal_server_error", slog.Any("error", err))
			}
		}()
	}

	s.logger.Info("server_started", slog.String("addr", s.srv.Addr))

	err := s.srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}
