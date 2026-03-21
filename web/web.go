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
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/googlesheets"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
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
	StripeAPIKey        string                              // Optional Stripe API key for subscriptions
	StripeWebhookSecret string                              // Optional Stripe webhook secret
	// Version is the Git SHA injected at build time via ldflags.
	// It is returned by the /health endpoint as the "version" field.
	Version string
}

func New(cfg ServerConfig) (*Server, error) {
	ans := Server{
		svc:      cfg.Service,
		tmpl:     make(map[string]*template.Template),
		db:       cfg.PgDB,
		userRepo: cfg.UserRepo,
		logger:   pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "api"),
		srv: &http.Server{
			Addr:              cfg.Addr,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
	}

	// Initialize authentication middleware if Clerk secret key is provided
	if cfg.ClerkSecretKey != "" && cfg.UserRepo != nil {
		var err error
		ans.authMiddleware, err = auth.NewAuthMiddleware(cfg.ClerkSecretKey, cfg.PgDB, cfg.UserRepo, cfg.APIKeyRepo, cfg.ServerSecret, ans.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize authentication: %w", err)
		}
	}

	// Initialize billing service if Stripe API key is provided
	if cfg.StripeAPIKey != "" && cfg.PgDB != nil {
		cfgSvc := config.New(cfg.PgDB)
		ans.billingSvc = billing.New(cfg.PgDB, cfgSvc, cfg.StripeAPIKey, cfg.StripeWebhookSecret)
	}

	staticFS, err := fs.Sub(static, "static")
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(staticFS))
	router := mux.NewRouter()

	// Initialize modular handler group (incremental migration)
	deps := webhandlers.Dependencies{
		Logger:              ans.logger,
		DB:                  ans.db,
		BillingSvc:          ans.billingSvc,
		Templates:           ans.tmpl,
		Auth:                ans.authMiddleware,
		App:                 ans.svc,
		APIKeyRepo:          cfg.APIKeyRepo,
		WebhookConfigRepo:   cfg.WebhookConfigRepo,
		WebhookDeliveryRepo: cfg.WebhookDeliveryRepo,
		ServerSecret:        cfg.ServerSecret,
		PricingRuleRepo:     postgres.NewPricingRuleRepository(ans.db),
		ResultsSvc:          webservices.NewResultsService(ans.db),
		IntegrationRepo:     postgres.NewIntegrationRepository(ans.db),
		GoogleSheetsSvc:     googlesheets.NewService(),
		Version:             cfg.Version,
	}
	if ans.db != nil {
		deps.ConcurrentLimitSvc = webservices.NewConcurrentLimitService(ans.db)
	}
	hg := webhandlers.NewHandlerGroup(deps)

	// Health check endpoint (no authentication needed)
	router.HandleFunc("/health", hg.Web.HealthCheck).Methods(http.MethodGet)

	// Prometheus metrics endpoint — scrape from Grafana or any Prometheus-compatible tool.
	// No auth: bind the API server to 127.0.0.1 to prevent external exposure.
	router.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)

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

	// OAuth auth endpoint (public - initiates OAuth flow)
	// User clicks "Connect" in the webapp and is redirected here
	publicAPIRouter.HandleFunc("/integrations/google/auth", hg.Integration.HandleGoogleAuth).Methods(http.MethodGet)

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
		webmiddleware.RequestID,
		webmiddleware.InjectLogger(ans.logger),
		webmiddleware.RequestLogger(ans.logger),
	)

	// API endpoints (these are protected by middleware if enabled)
	apiRouter.HandleFunc("/jobs", hg.API.GetJobs).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/user", hg.API.GetUserJobs).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs", hg.API.Scrape).Methods(http.MethodPost)
	apiRouter.HandleFunc("/jobs/{id}", hg.API.GetJob).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}", hg.API.DeleteJob).Methods(http.MethodDelete)
	apiRouter.HandleFunc("/jobs/{id}/cancel", hg.API.CancelJob).Methods(http.MethodPost)
	apiRouter.HandleFunc("/jobs/{id}/download", hg.Web.Download).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}/results", hg.API.GetJobResults).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}/costs", hg.API.GetJobCosts).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/estimate", hg.API.EstimateJobCost).Methods(http.MethodPost)
	apiRouter.HandleFunc("/results", hg.API.GetUserResults).Methods(http.MethodGet)

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

	// Protected integration endpoints (require authentication)
	// Callback is protected because:
	// 1. User must be logged in to initiate OAuth
	// 2. Browser automatically sends __session cookie with the redirect (SameSite=Lax)
	// 3. Auth middleware verifies the session cookie
	// 4. User ID is available in context via auth.GetUserID()
	apiRouter.HandleFunc("/integrations/google/callback", hg.Integration.HandleGoogleCallback).Methods(http.MethodGet)
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

	// Webhook endpoints (public access, no authentication)
	// Apply a 64 KB body limit — Stripe payloads are small; this prevents OOM from
	// oversized requests (CWE-400).
	webhookHandler := webmiddleware.MaxBodySize(64 << 10)(http.HandlerFunc(hg.Billing.HandleStripeWebhook))
	// goneHandler is returned on retired legacy webhook paths to surface any
	// Stripe dashboard misconfiguration quickly.
	goneHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "This endpoint has been retired. Configure your Stripe dashboard to POST to /api/v1/billing/webhook", http.StatusGone)
	})
	if ans.billingSvc != nil {
		// Canonical webhook path — configure this URL in the Stripe dashboard.
		// Stripe does not mandate a specific path; they simply require a single,
		// consistent HTTPS endpoint that responds 2xx quickly. Our versioned API
		// namespace (/api/v1/billing/webhook) keeps the route consistent with the
		// rest of the billing surface and makes WAF/firewall rules easier to manage.
		publicAPIRouter.Handle("/billing/webhook", webhookHandler).Methods(http.MethodPost)

		// Retired legacy paths — respond 410 Gone so any misconfigured Stripe
		// dashboard endpoint surfaces as an obvious delivery failure rather than
		// a silent auth error. Update the Stripe dashboard webhook URL before
		// deploying this change or webhook delivery will fail on these paths.
		router.Handle("/webhooks/stripe", goneHandler).Methods(http.MethodPost)
		router.Handle("/api/stripe/webhook", goneHandler).Methods(http.MethodPost)
	}

	// Apply security headers and CORS to all routes via middleware chain.
	// ALLOWED_ORIGINS is a comma-separated list of permitted origins (e.g.
	// "https://brezel.ai,https://www.brezel.ai"). If unset, only localhost
	// origins are allowed (safe development default).
	var allowedOrigins []string
	if raw := os.Getenv("ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				allowedOrigins = append(allowedOrigins, trimmed)
			}
		}
	}
	handler := webmiddleware.Chain(router, webmiddleware.Recovery(ans.logger), webmiddleware.SecurityHeaders, webmiddleware.NewCORS(allowedOrigins))
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

		err := s.srv.Shutdown(shutdownCtx)
		if err != nil {
			s.logger.Error("server_shutdown_error", slog.Any("error", err))
			return
		}

		s.logger.Info("server_stopped")
	}()

	s.logger.Info("server_started", slog.String("addr", s.srv.Addr))

	err := s.srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}
