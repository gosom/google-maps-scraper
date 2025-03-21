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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/web/auth"
)

//go:embed static
var static embed.FS

type Server struct {
	tmpl           map[string]*template.Template
	srv            *http.Server
	svc            *Service
	authMiddleware *auth.AuthMiddleware
	userRepo       postgres.UserRepository
	usageLimiter   postgres.UsageLimiter
	db             *sql.DB
}

type ServerConfig struct {
	Service      *Service
	Addr         string
	PgDB         *sql.DB // Optional PostgreSQL connection
	UserRepo     postgres.UserRepository
	UsageLimiter postgres.UsageLimiter
	ClerkAPIKey  string // Optional Clerk API key for authentication
}

func New(cfg ServerConfig) (*Server, error) {
	ans := Server{
		svc:          cfg.Service,
		tmpl:         make(map[string]*template.Template),
		db:           cfg.PgDB,
		userRepo:     cfg.UserRepo,
		usageLimiter: cfg.UsageLimiter,
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
	if cfg.ClerkAPIKey != "" && cfg.UserRepo != nil && cfg.UsageLimiter != nil {
		var err error
		ans.authMiddleware, err = auth.NewAuthMiddleware(cfg.ClerkAPIKey, cfg.UserRepo, cfg.UsageLimiter)
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

	// Static files
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fileServer))

	// Web UI routes
	router.HandleFunc("/", ans.index).Methods(http.MethodGet)
	router.HandleFunc("/scrape", ans.scrape).Methods(http.MethodPost)
	router.HandleFunc("/jobs", ans.getJobs).Methods(http.MethodGet)
	router.HandleFunc("/download", ans.download).Methods(http.MethodGet)
	router.HandleFunc("/delete", ans.delete).Methods(http.MethodDelete)

	// API documentation
	router.HandleFunc("/api/docs", ans.redocHandler).Methods(http.MethodGet)

	// API routes with authentication if available
	apiRouter := router.PathPrefix("/api/v1").Subrouter()

	// Apply authentication middleware if available
	if ans.authMiddleware != nil {
		apiRouter.Use(ans.authMiddleware.Authenticate)
		apiRouter.Use(ans.authMiddleware.CheckUsageLimit)
	}

	// API endpoints
	apiRouter.HandleFunc("/jobs", ans.apiGetJobs).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/user", ans.apiGetUserJobs).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs", ans.apiScrape).Methods(http.MethodPost)
	apiRouter.HandleFunc("/jobs/{id}", ans.apiGetJob).Methods(http.MethodGet)
	apiRouter.HandleFunc("/jobs/{id}", ans.apiDeleteJob).Methods(http.MethodDelete)
	apiRouter.HandleFunc("/jobs/{id}/download", ans.download).Methods(http.MethodGet)

	// Apply security headers to all routes
	// Apply security headers and CORS to all routes
	handler := corsMiddleware(securityHeaders(router))
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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*") // Or specific origin like "http://localhost:3000"
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
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
			log.Println(err)

			return
		}

		log.Println("server stopped")
	}()

	log.Printf("\033[32mGo server started at http://localhost%s\033[0m\n", s.srv.Addr)

	err := s.srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

type formData struct {
	Name     string
	MaxTime  string
	Keywords []string
	Language string
	Zoom     int
	FastMode bool
	Radius   int
	Lat      string
	Lon      string
	Depth    int
	Email    bool
	Proxies  []string
}

type ctxKey string

const idCtxKey ctxKey = "id"

func requestWithID(r *http.Request) *http.Request {
	id := r.PathValue("id")
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
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	tmpl, ok := s.tmpl["static/templates/index.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	data := formData{
		Name:     "",
		MaxTime:  "10m",
		Keywords: []string{},
		Language: "en",
		Zoom:     15,
		FastMode: false,
		Radius:   10000,
		Lat:      "0",
		Lon:      "0",
		Depth:    10,
		Email:    false,
	}

	_ = tmpl.Execute(w, data)
}

func (s *Server) scrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	newJob := Job{
		ID:     uuid.New().String(),
		Name:   r.Form.Get("name"),
		Date:   time.Now().UTC(),
		Status: StatusPending,
		Data:   JobData{},
	}

	maxTimeStr := r.Form.Get("maxtime")

	maxTime, err := time.ParseDuration(maxTimeStr)
	if err != nil {
		http.Error(w, "invalid max time", http.StatusUnprocessableEntity)

		return
	}

	if maxTime < time.Minute*3 {
		http.Error(w, "max time must be more than 3m", http.StatusUnprocessableEntity)

		return
	}

	newJob.Data.MaxTime = maxTime

	keywordsStr, ok := r.Form["keywords"]
	if !ok {
		http.Error(w, "missing keywords", http.StatusUnprocessableEntity)

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

		return
	}

	if r.Form.Get("fastmode") == "on" {
		newJob.Data.FastMode = true
	}

	newJob.Data.Radius, err = strconv.Atoi(r.Form.Get("radius"))
	if err != nil {
		http.Error(w, "invalid radius", http.StatusUnprocessableEntity)

		return
	}

	newJob.Data.Lat = r.Form.Get("latitude")
	newJob.Data.Lon = r.Form.Get("longitude")

	newJob.Data.Depth, err = strconv.Atoi(r.Form.Get("depth"))
	if err != nil {
		http.Error(w, "invalid depth", http.StatusUnprocessableEntity)

		return
	}

	newJob.Data.Email = r.Form.Get("email") == "on"

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

		return
	}

	err = s.svc.Create(r.Context(), &newJob)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	tmpl, ok := s.tmpl["static/templates/job_row.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	_ = tmpl.Execute(w, newJob)
}

func (s *Server) getJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tmpl, ok := s.tmpl["static/templates/job_rows.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		return
	}

	// For the web UI, we show all jobs (no authentication for web UI)
	jobs, err := s.svc.All(context.Background(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = tmpl.Execute(w, jobs)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	ctx := r.Context()

	id, ok := getIDFromRequest(r)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)

		return
	}

	filePath, err := s.svc.GetCSV(ctx, id.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	fileName := filepath.Base(filePath)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", "text/csv")

	_, err = io.Copy(w, file)
	if err != nil {
		http.Error(w, "Failed to send file", http.StatusInternalServerError)
		return
	}
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	deleteID, ok := getIDFromRequest(r)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)

		return
	}

	err := s.svc.Delete(r.Context(), deleteID.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
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

func (s *Server) redocHandler(w http.ResponseWriter, _ *http.Request) {
	tmpl, ok := s.tmpl["static/templates/redoc.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)

		return
	}

	_ = tmpl.Execute(w, nil)
}

func (s *Server) apiScrape(w http.ResponseWriter, r *http.Request) {
	var req apiScrapeRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		ans := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusUnprocessableEntity, ans)
		return
	}

	// Get user ID from context if authentication is enabled
	var userID string
	if s.authMiddleware != nil {
		userID, err = auth.GetUserID(r.Context())
		if err != nil {
			ans := apiError{
				Code:    http.StatusUnauthorized,
				Message: "User not authenticated",
			}
			renderJSON(w, http.StatusUnauthorized, ans)
			return
		}
	} else {
		// If auth middleware is not enabled but we're using a DB that requires user_id
		// Use a default user ID for development/testing without auth
		// This is useful when running locally without Clerk authentication
		userID = "default_user_id"

		// Check if the default user exists, create it if not
		if s.userRepo != nil {
			_, err := s.userRepo.GetByID(r.Context(), userID)
			if err != nil {
				// Create default user
				defaultUser := postgres.User{
					ID:    userID,
					Email: "default@example.com",
				}
				err = s.userRepo.Create(r.Context(), &defaultUser)
				if err != nil {
					log.Printf("Failed to create default user: %v", err)
					ans := apiError{
						Code:    http.StatusInternalServerError,
						Message: "Failed to create default user: " + err.Error(),
					}
					renderJSON(w, http.StatusInternalServerError, ans)
					return
				}
			}
		}
	}

	newJob := Job{
		ID:     uuid.New().String(),
		UserID: userID, // Set the user ID from authenticated context or default
		Name:   req.Name,
		Date:   time.Now().UTC(),
		Status: StatusPending,
		Data:   req.JobData,
	}

	// convert to seconds
	newJob.Data.MaxTime *= time.Second

	err = ValidateJob(&newJob)
	if err != nil {
		ans := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusUnprocessableEntity, ans)
		return
	}

	err = s.svc.Create(r.Context(), &newJob)
	if err != nil {
		ans := apiError{
			Code:    http.StatusInternalServerError,
			Message: "failed to create job: " + err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, ans)
		return
	}

	// Increment usage if authentication is enabled
	if s.authMiddleware != nil {
		if err := s.authMiddleware.IncrementUsage(r.Context()); err != nil {
			log.Printf("Failed to increment usage for user %s: %v", userID, err)
			// Continue anyway - the job was created successfully
		}
	}

	ans := apiScrapeResponse{
		ID: newJob.ID,
	}

	renderJSON(w, http.StatusCreated, ans)
}

func (s *Server) apiGetJobs(w http.ResponseWriter, r *http.Request) {
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
			return
		}

		// Only return jobs for the authenticated user
		jobs, err = s.svc.All(r.Context(), userID)
	} else {
		// If authentication is not enabled, return all jobs
		jobs, err = s.svc.All(r.Context(), "")
	}

	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		renderJSON(w, http.StatusInternalServerError, apiError)
		return
	}

	renderJSON(w, http.StatusOK, jobs)
}

func (s *Server) apiGetUserJobs(w http.ResponseWriter, r *http.Request) {
	// This endpoint always requires authentication
	fmt.Println("apiGetUserJobs")
	if s.authMiddleware == nil {
		apiError := apiError{
			Code:    http.StatusUnauthorized,
			Message: "Authentication not configured",
		}
		renderJSON(w, http.StatusUnauthorized, apiError)
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
		return
	}

	renderJSON(w, http.StatusOK, jobs)
}

func (s *Server) apiGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := getIDFromRequest(r)
	if !ok {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID",
		}

		renderJSON(w, http.StatusUnprocessableEntity, apiError)

		return
	}

	job, err := s.svc.Get(r.Context(), id.String())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusNotFound,
			Message: http.StatusText(http.StatusNotFound),
		}

		renderJSON(w, http.StatusNotFound, apiError)

		return
	}

	renderJSON(w, http.StatusOK, job)
}

func (s *Server) apiDeleteJob(w http.ResponseWriter, r *http.Request) {
	id, ok := getIDFromRequest(r)
	if !ok {
		apiError := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID",
		}

		renderJSON(w, http.StatusUnprocessableEntity, apiError)

		return
	}

	err := s.svc.Delete(r.Context(), id.String())
	if err != nil {
		apiError := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, apiError)

		return
	}

	w.WriteHeader(http.StatusOK)
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
