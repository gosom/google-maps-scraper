package web

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/pkg/googlesheets"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/web/auth"
	webhandlers "github.com/gosom/google-maps-scraper/web/handlers"
	webmiddleware "github.com/gosom/google-maps-scraper/web/middleware"
	webservices "github.com/gosom/google-maps-scraper/web/services"
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
	logger         *log.Logger
}

type ServerConfig struct {
	Service             *Service
	Addr                string
	PgDB                *sql.DB // Optional PostgreSQL connection
	UserRepo            postgres.UserRepository
	ClerkAPIKey         string // Optional Clerk API key for authentication
	StripeAPIKey        string // Optional Stripe API key for subscriptions
	StripeWebhookSecret string // Optional Stripe webhook secret
}

func New(cfg ServerConfig) (*Server, error) {
	ans := Server{
		svc:      cfg.Service,
		tmpl:     make(map[string]*template.Template),
		db:       cfg.PgDB,
		userRepo: cfg.UserRepo,
		logger:   log.New(os.Stdout, "[API] ", log.LstdFlags),
		srv: &http.Server{
			Addr:              cfg.Addr,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
	}

	// Initialize authentication middleware if Clerk API key is provided
	if cfg.ClerkAPIKey != "" && cfg.UserRepo != nil {
		var err error
		ans.authMiddleware, err = auth.NewAuthMiddleware(cfg.ClerkAPIKey, cfg.UserRepo)
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
		Logger:          ans.logger,
		DB:              ans.db,
		BillingSvc:      ans.billingSvc,
		Templates:       ans.tmpl,
		Auth:            ans.authMiddleware,
		App:             ans.svc,
		ResultsSvc:      webservices.NewResultsService(ans.db),
		IntegrationRepo: postgres.NewIntegrationRepository(ans.db),
		GoogleSheetsSvc: googlesheets.NewService(),
	}
	hg := webhandlers.NewHandlerGroup(deps)

	// Health check endpoint (no authentication needed)
	router.HandleFunc("/health", hg.Web.HealthCheck).Methods(http.MethodGet)

	// Static files
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fileServer))

	// Web UI routes
	router.HandleFunc("/", hg.Web.Index).Methods(http.MethodGet)
	router.HandleFunc("/jobs", hg.Web.Jobs).Methods(http.MethodGet)
	router.HandleFunc("/scrape", hg.Web.Scrape).Methods(http.MethodPost)
	router.HandleFunc("/delete", hg.Web.Delete).Methods(http.MethodDelete)
	router.HandleFunc("/download", hg.Web.Download).Methods(http.MethodGet)

	// API documentation (public access)
	router.HandleFunc("/api/docs", hg.Web.Redoc).Methods(http.MethodGet)

	// Public API routes (no authentication required)
	publicAPIRouter := router.PathPrefix("/api/v1").Subrouter()
	publicAPIRouter.Use(func(next http.Handler) http.Handler {
		return webmiddleware.RequestLogger(next)
	})

	// OAuth auth endpoint (public - initiates OAuth flow)
	// User clicks "Connect" in the webapp and is redirected here
	publicAPIRouter.HandleFunc("/integrations/google/auth", hg.Integration.HandleGoogleAuth).Methods(http.MethodGet)

	// API routes with authentication if available
	apiRouter := router.PathPrefix("/api/v1").Subrouter()

	// Apply authentication middleware if available
	if ans.authMiddleware != nil {
		apiRouter.Use(ans.authMiddleware.Authenticate)
	}

	// Apply request logger after authentication so user_id is available in context
	apiRouter.Use(func(next http.Handler) http.Handler {
		return webmiddleware.RequestLogger(next)
	})

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
		apiRouter.HandleFunc("/credits/checkout-session", hg.Billing.CreateCheckoutSession).Methods(http.MethodPost)
		apiRouter.HandleFunc("/credits/reconcile", hg.Billing.Reconcile).Methods(http.MethodPost)
	}

	// Webhook endpoints (public access, no authentication)
	if ans.billingSvc != nil {
		// Primary webhook path
		router.HandleFunc("/webhooks/stripe", hg.Billing.HandleStripeWebhook).Methods(http.MethodPost)
		// Backward-compatible alias used by some Stripe CLI setups
		router.HandleFunc("/api/stripe/webhook", hg.Billing.HandleStripeWebhook).Methods(http.MethodPost)
	}

	// Apply security headers and CORS to all routes via middleware chain
	// Note: RequestLogger is applied separately to API routes after authentication
	handler := webmiddleware.Chain(router, webmiddleware.SecurityHeaders, webmiddleware.CORS)
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

// --- Billing (credits) HTTP handlers ---

type creditBalanceResponse struct {
	UserID                string `json:"user_id"`
	CreditBalance         string `json:"credit_balance"`
	TotalCreditsPurchased string `json:"total_credits_purchased"`
}

func (s *Server) apiGetCreditBalance(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		renderJSON(w, http.StatusServiceUnavailable, apiError{Code: http.StatusServiceUnavailable, Message: "database not available"})
		return
	}
	if s.authMiddleware == nil {
		renderJSON(w, http.StatusUnauthorized, apiError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, apiError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	const q = `SELECT id, credit_balance::text, total_credits_purchased::text FROM users WHERE id=$1`
	var resp creditBalanceResponse
	if err := s.db.QueryRowContext(r.Context(), q, userID).Scan(&resp.UserID, &resp.CreditBalance, &resp.TotalCreditsPurchased); err != nil {
		// If no row, return zero balance for authenticated user
		resp = creditBalanceResponse{UserID: userID, CreditBalance: "0", TotalCreditsPurchased: "0"}
	}
	renderJSON(w, http.StatusOK, resp)
}

type checkoutSessionRequest struct {
	Credits  string `json:"credits"`
	Currency string `json:"currency"`
}

func (s *Server) apiCreateCheckoutSession(w http.ResponseWriter, r *http.Request) {
	if s.billingSvc == nil {
		renderJSON(w, http.StatusServiceUnavailable, apiError{Code: http.StatusServiceUnavailable, Message: "billing not configured"})
		return
	}
	if s.authMiddleware == nil {
		renderJSON(w, http.StatusUnauthorized, apiError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, apiError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	var req checkoutSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, apiError{Code: http.StatusUnprocessableEntity, Message: "invalid payload"})
		return
	}
	out, err := s.billingSvc.CreateCheckoutSession(r.Context(), billing.CheckoutRequest{UserID: userID, Credits: req.Credits, Currency: req.Currency})
	if err != nil {
		renderJSON(w, http.StatusBadRequest, apiError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, out)
}

type reconcileRequest struct {
	SessionID string `json:"session_id"`
}

func (s *Server) apiReconcile(w http.ResponseWriter, r *http.Request) {
	if s.billingSvc == nil {
		renderJSON(w, http.StatusServiceUnavailable, apiError{Code: http.StatusServiceUnavailable, Message: "billing not configured"})
		return
	}
	var req reconcileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, apiError{Code: http.StatusUnprocessableEntity, Message: "invalid payload"})
		return
	}
	if err := s.billingSvc.ReconcileSession(r.Context(), req.SessionID); err != nil {
		renderJSON(w, http.StatusBadRequest, apiError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if s.billingSvc == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	code, _ := s.billingSvc.HandleWebhook(r.Context(), payload, sig)
	w.WriteHeader(code)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Allow localhost origins for development
		if origin == "http://localhost:3000" || origin == "http://localhost:3001" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			// For production, you should set specific allowed origins
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()

		err := s.srv.Shutdown(context.Background())
		if err != nil {
			s.logger.Println(err)
			return
		}

		s.logger.Println("server stopped")
	}()

	s.logger.Printf("\033[32mGo server started at http://localhost%s...\033[0m\n", s.srv.Addr)
	fmt.Println("◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤◢◣◥◤")
	fmt.Println("	")

	err := s.srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

type formData struct {
	Name       string
	MaxTime    string
	Keywords   []string
	Language   string
	Zoom       int
	FastMode   bool
	Radius     int
	Lat        string
	Lon        string
	Depth      int
	Email      bool
	Images     bool
	ReviewsMax int
	Proxies    []string
}

type ctxKey string

const idCtxKey ctxKey = "id"

func requestWithID(r *http.Request) *http.Request {
	var id string
	if vars := mux.Vars(r); vars != nil {
		id = vars["id"]
	}
	if id == "" {
		id = r.URL.Query().Get("id")
	}

	parsed, err := uuid.Parse(id)
	if err == nil {
		r = r.WithContext(context.WithValue(r.Context(), idCtxKey, parsed))
	}

	return r
}

func getIDFromRequest(r *http.Request) (uuid.UUID, bool) {
	id, ok := r.Context().Value(idCtxKey).(uuid.UUID)

	return id, ok
}

//nolint:gocritic // this is used in template
func (f formData) ProxiesString() string {
	return strings.Join(f.Proxies, "\n")
}

//nolint:gocritic // this is used in template
func (f formData) KeywordsString() string {
	return strings.Join(f.Keywords, "\n")
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		s.logger.Printf("Method not allowed: %s %s", r.Method, r.URL.Path)
		return
	}

	tmpl, ok := s.tmpl["static/templates/index.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		s.logger.Printf("Missing template: index.html")
		return
	}

	data := formData{
		Name:       "",
		MaxTime:    "10m",
		Keywords:   []string{},
		Language:   "en",
		Zoom:       15,
		FastMode:   false,
		Radius:     10000,
		Lat:        "0",
		Lon:        "0",
		Depth:      10,
		Email:      false,
		Images:     false,
		ReviewsMax: 1,
	}

	_ = tmpl.Execute(w, data)
	s.logger.Printf("Rendered index page")
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		s.logger.Printf("Method not allowed: %s %s", r.Method, r.URL.Path)
		return
	}

	// Get all jobs (no user filtering for web UI)
	jobs, err := s.svc.All(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		s.logger.Printf("Failed to get jobs: %v", err)
		return
	}

	tmpl, ok := s.tmpl["static/templates/job_rows.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		s.logger.Printf("Missing template: job_rows.html")
		return
	}

	_ = tmpl.Execute(w, jobs)
	s.logger.Printf("Rendered %d jobs", len(jobs))
}

func (s *Server) scrape(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("POST %s", r.URL.Path)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		s.logger.Printf("Method not allowed: %s %s", r.Method, r.URL.Path)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		s.logger.Printf("Error parsing form: %v", err)
		return
	}

	newJob := Job{
		ID:     uuid.New().String(),
		UserID: "default_user_id", // Set default user for web UI
		Name:   r.Form.Get("name"),
		Date:   time.Now().UTC(),
		Status: StatusPending,
		Data:   JobData{},
	}

	// Ensure default user exists for web UI
	if s.userRepo != nil {
		_, err := s.userRepo.GetByID(r.Context(), "default_user_id")
		if err != nil {
			// Create default user
			defaultUser := postgres.User{
				ID:    "default_user_id",
				Email: "webui@example.com",
			}
			err = s.userRepo.Create(r.Context(), &defaultUser)
			if err != nil {
				http.Error(w, "Failed to create default user: "+err.Error(), http.StatusInternalServerError)
				s.logger.Printf("Failed to create default user for web UI: %v", err)
				return
			}
			s.logger.Printf("Created default user for web UI")
		}
	}

	maxTimeStr := r.Form.Get("maxtime")

	maxTime, err := time.ParseDuration(maxTimeStr)
	if err != nil {
		http.Error(w, "invalid max time", http.StatusUnprocessableEntity)
		s.logger.Printf("Invalid max time: %s", maxTimeStr)
		return
	}

	if maxTime < time.Minute*3 {
		http.Error(w, "max time must be more than 3m", http.StatusUnprocessableEntity)
		s.logger.Printf("Max time too short: %s", maxTimeStr)
		return
	}

	newJob.Data.MaxTime = maxTime

	keywordsStr, ok := r.Form["keywords"]
	if !ok {
		http.Error(w, "missing keywords", http.StatusUnprocessableEntity)
		s.logger.Printf("Missing keywords")
		return
	}

	keywords := strings.Split(keywordsStr[0], "\n")
	for _, k := range keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}

		newJob.Data.Keywords = append(newJob.Data.Keywords, k)
	}

	newJob.Data.Lang = r.Form.Get("lang")

	newJob.Data.Zoom, err = strconv.Atoi(r.Form.Get("zoom"))
	if err != nil {
		http.Error(w, "invalid zoom", http.StatusUnprocessableEntity)
		s.logger.Printf("Invalid zoom: %s", r.Form.Get("zoom"))
		return
	}

	if r.Form.Get("fastmode") == "on" {
		newJob.Data.FastMode = true
	}

	newJob.Data.Radius, err = strconv.Atoi(r.Form.Get("radius"))
	if err != nil {
		http.Error(w, "invalid radius", http.StatusUnprocessableEntity)
		s.logger.Printf("Invalid radius: %s", r.Form.Get("radius"))
		return
	}

	newJob.Data.Lat = r.Form.Get("latitude")
	newJob.Data.Lon = r.Form.Get("longitude")

	newJob.Data.Depth, err = strconv.Atoi(r.Form.Get("depth"))
	if err != nil {
		http.Error(w, "invalid depth", http.StatusUnprocessableEntity)
		s.logger.Printf("Invalid depth: %s", r.Form.Get("depth"))
		return
	}

	newJob.Data.Email = r.Form.Get("email") == "on"
	newJob.Data.Images = r.Form.Get("images") == "on"

	// Parse reviews_max field
	if reviewsMaxStr := r.Form.Get("reviews_max"); reviewsMaxStr != "" {
		newJob.Data.ReviewsMax, err = strconv.Atoi(reviewsMaxStr)
		if err != nil {
			http.Error(w, "invalid reviews_max", http.StatusUnprocessableEntity)
			s.logger.Printf("Invalid reviews_max: %s", reviewsMaxStr)
			return
		}
	} else {
		newJob.Data.ReviewsMax = 1 // default value
	}

	// Parse max_results field
	if maxResultsStr := r.Form.Get("max_results"); maxResultsStr != "" {
		newJob.Data.MaxResults, err = strconv.Atoi(maxResultsStr)
		if err != nil {
			http.Error(w, "invalid max_results", http.StatusUnprocessableEntity)
			s.logger.Printf("Invalid max_results: %s", maxResultsStr)
			return
		}
	} else {
		newJob.Data.MaxResults = 0 // default value (unlimited)
	}

	proxies := strings.Split(r.Form.Get("proxies"), "\n")
	if len(proxies) > 0 {
		for _, p := range proxies {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}

			newJob.Data.Proxies = append(newJob.Data.Proxies, p)
		}
	}

	err = ValidateJob(&newJob)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		s.logger.Printf("Job validation failed: %v", err)
		return
	}

	err = s.svc.Create(r.Context(), &newJob)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		s.logger.Printf("Failed to create job: %v", err)
		return
	}

	tmpl, ok := s.tmpl["static/templates/job_row.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		s.logger.Printf("Missing template: job_row.html")
		return
	}

	_ = tmpl.Execute(w, newJob)
	s.logger.Printf("Created job: %s", newJob.ID)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		s.logger.Printf("Method not allowed: %s %s", r.Method, r.URL.Path)
		return
	}

	// Extract ID directly from the URL path using Gorilla Mux
	vars := mux.Vars(r)
	idStr := vars["id"]

	if idStr == "" {
		// Fallback to query parameter if not in path
		idStr = r.URL.Query().Get("id")
	}

	if idStr == "" {
		http.Error(w, "Missing ID", http.StatusUnprocessableEntity)
		s.logger.Printf("Missing ID for download")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Invalid ID format", http.StatusUnprocessableEntity)
		s.logger.Printf("Invalid ID format for download: %v", err)
		return
	}

	// Use new GetCSVReader method which supports both S3 and local filesystem
	reader, fileName, err := s.svc.GetCSVReader(r.Context(), id.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		s.logger.Printf("Failed to get CSV for job %s: %v", id, err)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", "text/csv")

	_, err = io.Copy(w, reader)
	if err != nil {
		http.Error(w, "Failed to send file", http.StatusInternalServerError)
		s.logger.Printf("Failed to send file %s: %v", fileName, err)
		return
	}

	s.logger.Printf("Successfully served CSV file %s for job %s", fileName, id)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("DELETE %s", r.URL.Path)

	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		s.logger.Printf("Method not allowed: %s %s", r.Method, r.URL.Path)
		return
	}

	// Parse ID from request (query parameter or path) and add to context
	r = requestWithID(r)
	deleteID, ok := getIDFromRequest(r)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)
		s.logger.Printf("Invalid ID for delete")
		return
	}

	err := s.svc.Delete(r.Context(), deleteID.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		s.logger.Printf("Failed to delete job %s: %v", deleteID, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	s.logger.Printf("Deleted job: %s", deleteID)
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type apiScrapeRequest struct {
	Name string
	JobData
}

type apiScrapeResponse struct {
	ID string `json:"id"`
}

// PaginatedResultsResponse represents a paginated response for job results
type PaginatedResultsResponse struct {
	Results    []EnhancedResult `json:"results"`
	TotalCount int              `json:"total_count"`
	Page       int              `json:"page"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
	TotalPages int              `json:"total_pages"`
	HasNext    bool             `json:"has_next"`
	HasPrev    bool             `json:"has_prev"`
}

func (s *Server) redocHandler(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	tmpl, ok := s.tmpl["static/templates/redoc.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		s.logger.Printf("Missing template: redoc.html")
		return
	}

	_ = tmpl.Execute(w, nil)
	s.logger.Printf("Rendered API docs page")
}

func (s *Server) apiScrape(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("POST %s", r.URL.Path)

	var req apiScrapeRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		ans := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusUnprocessableEntity, ans)
		s.logger.Printf("Failed to decode API scrape request: %v", err)
		return
	}

	// Require authentication
	if s.authMiddleware == nil {
		ans := apiError{Code: http.StatusUnauthorized, Message: "Authentication not configured"}
		renderJSON(w, http.StatusUnauthorized, ans)
		return
	}

	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		ans := apiError{Code: http.StatusUnauthorized, Message: "User not authenticated"}
		renderJSON(w, http.StatusUnauthorized, ans)
		return
	}

	newJob := Job{
		ID:     uuid.New().String(),
		UserID: userID,
		Name:   req.Name,
		Date:   time.Now().UTC(),
		Status: StatusPending,
		Data:   req.JobData,
	}

	// DEBUG: Log job creation with user ID for troubleshooting
	s.logger.Printf("DEBUG: Creating job %s for user: '%s'", newJob.ID, userID)

	// convert to seconds
	newJob.Data.MaxTime *= time.Second

	err = ValidateJob(&newJob)
	if err != nil {
		ans := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusUnprocessableEntity, ans)
		s.logger.Printf("Job validation failed: %v", err)
		return
	}

	err = s.svc.Create(r.Context(), &newJob)
	if err != nil {
		ans := apiError{
			Code:    http.StatusInternalServerError,
			Message: "failed to create job: " + err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, ans)
		s.logger.Printf("Failed to create job: %v", err)
		return
	}

	ans := apiScrapeResponse{
		ID: newJob.ID,
	}

	renderJSON(w, http.StatusCreated, ans)
	s.logger.Printf("Created API job: %s for user: %s", newJob.ID, userID)
}

func (s *Server) apiGetJobs(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	// Get user ID from context if authentication is enabled
	var jobs []Job
	var err error

	if s.authMiddleware != nil {
		userID, err := auth.GetUserID(r.Context())
		if err != nil {
			apiError := apiError{
				Code:    http.StatusUnauthorized,
				Message: "User not authenticated",
			}
			renderJSON(w, http.StatusUnauthorized, apiError)
			s.logger.Printf("User not authenticated: %v", err)
			return
		}

		// Only return jobs for the authenticated user
		jobs, err = s.svc.All(r.Context(), userID)
		if err != nil {
			apiError := apiError{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
			}
			renderJSON(w, http.StatusInternalServerError, apiError)
			s.logger.Printf("Failed to get jobs for user %s: %v", userID, err)
			return
		}
		s.logger.Printf("Retrieved %d jobs for user %s", len(jobs), userID)
	} else {
		// If authentication is not enabled, return all jobs
		jobs, err = s.svc.All(r.Context(), "")
		if err != nil {
			apiError := apiError{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
			}
			renderJSON(w, http.StatusInternalServerError, apiError)
			s.logger.Printf("Failed to get all jobs: %v", err)
			return
		}
		s.logger.Printf("Retrieved %d jobs (no auth)", len(jobs))
	}

	renderJSON(w, http.StatusOK, jobs)
}

func (s *Server) apiGetUserJobs(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	// This endpoint always requires authentication
	if s.authMiddleware == nil {
		apiError := apiError{
			Code:    http.StatusUnauthorized,
			Message: "Authentication not configured",
		}
		renderJSON(w, http.StatusUnauthorized, apiError)
		s.logger.Printf("Authentication not configured for user jobs endpoint")
		return
	}

	// Get user ID from context
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusUnauthorized,
			Message: "User not authenticated",
		}
		renderJSON(w, http.StatusUnauthorized, apiError)
		s.logger.Printf("User not authenticated: %v", err)
		return
	}

	// Only return jobs for the authenticated user (strict matching)
	jobs, err := s.svc.All(r.Context(), userID)
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		renderJSON(w, http.StatusInternalServerError, apiError)
		s.logger.Printf("Failed to get jobs for user %s: %v", userID, err)
		return
	}

	renderJSON(w, http.StatusOK, jobs)
	s.logger.Printf("Retrieved %d jobs for user %s", len(jobs), userID)
}

func (s *Server) apiGetJob(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	// Extract ID directly from the URL path using Gorilla Mux
	vars := mux.Vars(r)
	idStr := vars["id"]

	if idStr == "" {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Missing job ID",
		}
		renderJSON(w, http.StatusUnprocessableEntity, apiError)
		s.logger.Printf("Missing job ID for get")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID format",
		}
		renderJSON(w, http.StatusUnprocessableEntity, apiError)
		s.logger.Printf("Invalid ID format for get: %v", err)
		return
	}

	job, err := s.svc.Get(r.Context(), id.String())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusNotFound,
			Message: http.StatusText(http.StatusNotFound),
		}
		renderJSON(w, http.StatusNotFound, apiError)
		s.logger.Printf("Failed to get job %s: %v", id, err)
		return
	}

	renderJSON(w, http.StatusOK, job)
	s.logger.Printf("Retrieved job: %s", id)
}

func (s *Server) apiDeleteJob(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("DELETE %s", r.URL.Path)

	// Extract ID directly from the URL path using Gorilla Mux
	vars := mux.Vars(r)
	idStr := vars["id"]

	if idStr == "" {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Missing job ID",
		}
		renderJSON(w, http.StatusUnprocessableEntity, apiError)
		s.logger.Printf("Missing job ID for delete")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID format",
		}
		renderJSON(w, http.StatusUnprocessableEntity, apiError)
		s.logger.Printf("Invalid ID format for delete: %v", err)
		return
	}

	err = s.svc.Delete(r.Context(), id.String())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		renderJSON(w, http.StatusInternalServerError, apiError)
		s.logger.Printf("Failed to delete job %s: %v", id, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	s.logger.Printf("Deleted job: %s", id)
}

func (s *Server) apiCancelJob(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("POST %s", r.URL.Path)

	// Extract ID directly from the URL path using Gorilla Mux
	vars := mux.Vars(r)
	idStr := vars["id"]

	if idStr == "" {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Missing job ID",
		}
		renderJSON(w, http.StatusUnprocessableEntity, apiError)
		s.logger.Printf("Missing job ID for cancel")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID format",
		}
		renderJSON(w, http.StatusUnprocessableEntity, apiError)
		s.logger.Printf("Invalid ID format for cancel: %v", err)
		return
	}

	// Get user ID from context for authorization if authentication is enabled
	if s.authMiddleware != nil {
		userID, err := auth.GetUserID(r.Context())
		if err != nil {
			apiError := apiError{
				Code:    http.StatusUnauthorized,
				Message: "User not authenticated",
			}
			renderJSON(w, http.StatusUnauthorized, apiError)
			s.logger.Printf("User not authenticated for cancel: %v", err)
			return
		}

		// Verify job belongs to user
		job, err := s.svc.Get(r.Context(), id.String())
		if err != nil {
			apiError := apiError{
				Code:    http.StatusNotFound,
				Message: "Job not found",
			}
			renderJSON(w, http.StatusNotFound, apiError)
			s.logger.Printf("Job %s not found for cancel: %v", id, err)
			return
		}

		if job.UserID != userID {
			apiError := apiError{
				Code:    http.StatusForbidden,
				Message: "Access denied",
			}
			renderJSON(w, http.StatusForbidden, apiError)
			s.logger.Printf("User %s tried to cancel job %s owned by %s", userID, id, job.UserID)
			return
		}
	}

	err = s.svc.Cancel(r.Context(), id.String())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		renderJSON(w, http.StatusInternalServerError, apiError)
		s.logger.Printf("Failed to cancel job %s: %v", id, err)
		return
	}

	response := map[string]interface{}{
		"message": "Job cancellation initiated",
		"job_id":  id.String(),
	}

	renderJSON(w, http.StatusOK, response)
	s.logger.Printf("Cancelled job: %s", id)
}

// Result represents a single scraped result
type Result struct {
	ID          int       `json:"id"`
	UserID      string    `json:"user_id"`
	JobID       string    `json:"job_id"`
	InputID     string    `json:"input_id"`
	Link        string    `json:"link"`
	Cid         string    `json:"cid"`
	Title       string    `json:"title"`
	Categories  string    `json:"categories"`
	Category    string    `json:"category"`
	Address     string    `json:"address"`
	Website     string    `json:"website"`
	Phone       string    `json:"phone"`
	PlusCode    string    `json:"plus_code"`
	ReviewCount int       `json:"review_count"`
	Rating      float64   `json:"rating"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	ReviewsLink string    `json:"reviews_link"`
	Thumbnail   string    `json:"thumbnail"`
	Timezone    string    `json:"timezone"`
	PriceRange  string    `json:"price_range"`
	DataID      string    `json:"data_id"`
	Emails      string    `json:"emails"`
	CreatedAt   time.Time `json:"created_at"`
}

// apiGetJobResults returns paginated results for a specific job
func (s *Server) apiGetJobResults(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	// Extract job ID from URL
	vars := mux.Vars(r)
	jobID := vars["id"]

	if jobID == "" {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Missing job ID",
		}
		renderJSON(w, http.StatusUnprocessableEntity, apiError)
		s.logger.Printf("Missing job ID for get job results")
		return
	}

	// Parse pagination parameters
	page := 1
	limit := 50 // default limit

	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if parsedPage, err := strconv.Atoi(pageStr); err == nil && parsedPage > 0 {
			page = parsedPage
		}
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	offset := (page - 1) * limit

	// Get user ID from context if authentication is enabled
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusUnauthorized,
			Message: "User not authenticated",
		}
		renderJSON(w, http.StatusUnauthorized, apiError)
		s.logger.Printf("User not authenticated for job results: %v", err)
		return
	}

	// Verify job belongs to user
	job, err := s.svc.Get(r.Context(), jobID)
	if err != nil {
		apiError := apiError{
			Code:    http.StatusNotFound,
			Message: "Job not found",
		}
		renderJSON(w, http.StatusNotFound, apiError)
		s.logger.Printf("Job %s not found: %v", jobID, err)
		return
	}

	// DEBUG: Log user ID comparison for troubleshooting
	s.logger.Printf("DEBUG: Job %s - Job.UserID: '%s', Request.UserID: '%s'", jobID, job.UserID, userID)

	// TEMPORARY: Skip user verification if auth is disabled (for development)
	if s.authMiddleware != nil && job.UserID != userID {
		apiError := apiError{
			Code:    http.StatusForbidden,
			Message: "Access denied",
		}
		renderJSON(w, http.StatusForbidden, apiError)
		s.logger.Printf("User %s tried to access job %s owned by %s", userID, jobID, job.UserID)
		return
	} else if s.authMiddleware == nil {
		s.logger.Printf("DEBUG: Skipping user verification (auth disabled) - allowing access to job %s", jobID)
	}

	// Get paginated enhanced results from database
	results, totalCount, err := s.getEnhancedJobResultsPaginated(r.Context(), jobID, limit, offset)
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: "Failed to get results: " + err.Error(),
		}
		renderJSON(w, http.StatusInternalServerError, apiError)
		s.logger.Printf("Failed to get enhanced results for job %s: %v", jobID, err)
		return
	}

	// Calculate pagination metadata
	totalPages := (totalCount + limit - 1) / limit // Ceiling division
	if totalPages == 0 {
		totalPages = 1
	}

	response := PaginatedResultsResponse{
		Results:    results,
		TotalCount: totalCount,
		Page:       page,
		Limit:      limit,
		Offset:     offset,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
		HasPrev:    page > 1,
	}

	renderJSON(w, http.StatusOK, response)
	s.logger.Printf("Retrieved %d of %d enhanced results for job %s (page %d/%d)", len(results), totalCount, jobID, page, totalPages)
}

// apiGetUserResults returns all results for the authenticated user
func (s *Server) apiGetUserResults(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	// Get user ID from context
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusUnauthorized,
			Message: "User not authenticated",
		}
		renderJSON(w, http.StatusUnauthorized, apiError)
		s.logger.Printf("User not authenticated for user results: %v", err)
		return
	}

	// Parse query parameters
	limit := 50 // default limit
	offset := 0 // default offset

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsedOffset, err := strconv.Atoi(offsetStr); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	// Get results from database
	results, err := s.getUserResults(r.Context(), userID, limit, offset)
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: "Failed to get results: " + err.Error(),
		}
		renderJSON(w, http.StatusInternalServerError, apiError)
		s.logger.Printf("Failed to get results for user %s: %v", userID, err)
		return
	}

	renderJSON(w, http.StatusOK, results)
	s.logger.Printf("Retrieved %d results for user %s", len(results), userID)
}

// getJobResults retrieves results for a specific job from database
func (s *Server) getJobResults(ctx context.Context, jobID string) ([]Result, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	query := `
		SELECT 
			id, user_id, job_id, input_id, link, cid, title, 
			categories, category, address, website, phone, pluscode,
			review_count, rating, latitude, longitude, status_info,
			description, reviews_link, thumbnail, timezone, price_range,
			data_id, emails, created_at
		FROM results 
		WHERE job_id = $1 
		ORDER BY created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to query results: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		err := rows.Scan(
			&r.ID, &r.UserID, &r.JobID, &r.InputID, &r.Link, &r.Cid, &r.Title,
			&r.Categories, &r.Category, &r.Address, &r.Website, &r.Phone, &r.PlusCode,
			&r.ReviewCount, &r.Rating, &r.Latitude, &r.Longitude, &r.Status,
			&r.Description, &r.ReviewsLink, &r.Thumbnail, &r.Timezone, &r.PriceRange,
			&r.DataID, &r.Emails, &r.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan result: %w", err)
		}
		results = append(results, r)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

// getUserResults retrieves results for a specific user from database
func (s *Server) getUserResults(ctx context.Context, userID string, limit, offset int) ([]Result, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	query := `
		SELECT 
			id, user_id, job_id, input_id, link, cid, title, 
			categories, category, address, website, phone, pluscode,
			review_count, rating, latitude, longitude, status_info,
			description, reviews_link, thumbnail, timezone, price_range,
			data_id, emails, created_at
		FROM results 
		WHERE user_id = $1 
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query results: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		err := rows.Scan(
			&r.ID, &r.UserID, &r.JobID, &r.InputID, &r.Link, &r.Cid, &r.Title,
			&r.Categories, &r.Category, &r.Address, &r.Website, &r.Phone, &r.PlusCode,
			&r.ReviewCount, &r.Rating, &r.Latitude, &r.Longitude, &r.Status,
			&r.Description, &r.ReviewsLink, &r.Thumbnail, &r.Timezone, &r.PriceRange,
			&r.DataID, &r.Emails, &r.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan result: %w", err)
		}
		results = append(results, r)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

func renderJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_ = json.NewEncoder(w).Encode(data)
}

func formatDate(t time.Time) string {
	return t.Format("Jan 02, 2006 15:04:05")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' cdn.redoc.ly cdnjs.cloudflare.com 'unsafe-inline' 'unsafe-eval'; "+
				"worker-src 'self' blob:; "+
				"style-src 'self' 'unsafe-inline' fonts.googleapis.com; "+
				"img-src 'self' data: cdn.redoc.ly; "+
				"font-src 'self' fonts.gstatic.com; "+
				"connect-src 'self'")

		next.ServeHTTP(w, r)
	})
}

// Health check endpoint for staging infrastructure
func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("GET %s", r.URL.Path)

	// Check database connection if available
	dbStatus := "not_configured"
	if s.db != nil {
		if err := s.db.Ping(); err != nil {
			dbStatus = "unhealthy"
		} else {
			dbStatus = "healthy"
		}
	}

	response := map[string]interface{}{
		"status":    "healthy",
		"version":   "v1.0.0",
		"service":   "brezel.ai",
		"timestamp": time.Now().UTC(),
		"checks": map[string]string{
			"database": dbStatus,
			"server":   "healthy",
		},
	}

	// Return 503 if database is unhealthy
	if dbStatus == "unhealthy" {
		renderJSON(w, http.StatusServiceUnavailable, response)
	} else {
		renderJSON(w, http.StatusOK, response)
	}
}
