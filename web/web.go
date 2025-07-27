// Package web provides HTTP server and API endpoints for the Google Maps Scraper.
package web

import (
	"context"
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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/gosom/google-maps-scraper/runner/auth"
)

//go:embed static
var static embed.FS

type Server struct {
	tmpl   map[string]*template.Template
	srv    *http.Server
	svc    *Service
	apiKey string

	// Stream client management
	streamMu      sync.RWMutex
	streamClients map[string][]chan plugins.StreamEvent // jobID -> client channels

	// Event history for late-connecting clients
	eventHistory map[string][]plugins.StreamEvent // jobID -> recent events
}

func New(svc *Service, addr, apiKey string) (*Server, error) {
	ans := Server{
		svc:           svc,
		tmpl:          make(map[string]*template.Template),
		streamClients: make(map[string][]chan plugins.StreamEvent),
		eventHistory:  make(map[string][]plugins.StreamEvent),
		apiKey:        apiKey,
		srv: &http.Server{
			Addr:              addr,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
	}

	staticFS, err := fs.Sub(static, "static")
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(staticFS))
	mux := http.NewServeMux()

	// Public routes (no authentication required)
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
	mux.HandleFunc("/scrape", ans.scrape)
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		ans.download(w, r)
	})
	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		ans.delete(w, r)
	})
	mux.HandleFunc("/jobs", ans.getJobs)
	mux.HandleFunc("/", ans.index)

	// API documentation (public)
	mux.HandleFunc("/api/docs", ans.redocHandler)

	// Create API router with auth middleware
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			ans.apiScrape(w, r)
		case http.MethodGet:
			ans.apiGetJobs(w, r)
		default:
			ans := apiError{
				Code:    http.StatusMethodNotAllowed,
				Message: "Method not allowed",
			}

			renderJSON(w, http.StatusMethodNotAllowed, ans)
		}
	})

	apiMux.HandleFunc("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		switch r.Method {
		case http.MethodGet:
			ans.apiGetJob(w, r)
		case http.MethodDelete:
			ans.apiDeleteJob(w, r)
		default:
			ans := apiError{
				Code:    http.StatusMethodNotAllowed,
				Message: "Method not allowed",
			}

			renderJSON(w, http.StatusMethodNotAllowed, ans)
		}
	})

	apiMux.HandleFunc("/jobs/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		if r.Method != http.MethodGet {
			ans := apiError{
				Code:    http.StatusMethodNotAllowed,
				Message: "Method not allowed",
			}

			renderJSON(w, http.StatusMethodNotAllowed, ans)

			return
		}

		ans.download(w, r)
	})

	apiMux.HandleFunc("/jobs/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		r = requestWithID(r)

		if r.Method != http.MethodGet {
			ans := apiError{
				Code:    http.StatusMethodNotAllowed,
				Message: "Method not allowed",
			}

			renderJSON(w, http.StatusMethodNotAllowed, ans)

			return
		}

		ans.streamEvents(w, r)
	})

	// Apply auth middleware to API routes and mount them
	authMiddleware := auth.BearerTokenMiddleware(apiKey)
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", authMiddleware(apiMux)))

	handler := securityHeaders(mux)
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

		err := s.srv.Shutdown(context.Background())
		if err != nil {
			log.Println(err)

			return
		}

		log.Println("server stopped")
	}()

	fmt.Fprintf(os.Stderr, "visit http://localhost%s\n", s.srv.Addr)

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

	err = newJob.Validate()
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

	jobs, err := s.svc.All(context.Background())
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

	newJob := Job{
		ID:     uuid.New().String(),
		Name:   req.Name,
		Date:   time.Now().UTC(),
		Status: StatusPending,
		Data:   req.JobData,
	}

	// convert to seconds
	newJob.Data.MaxTime *= time.Second

	err = newJob.Validate()
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
			Message: err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, ans)

		return
	}

	ans := apiScrapeResponse{
		ID: newJob.ID,
	}

	renderJSON(w, http.StatusCreated, ans)
}

func (s *Server) apiGetJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.svc.All(r.Context())
	if err != nil {
		errResp := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, errResp)

		return
	}

	renderJSON(w, http.StatusOK, jobs)
}

func (s *Server) apiGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := getIDFromRequest(r)
	if !ok {
		errResp := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID",
		}

		renderJSON(w, http.StatusUnprocessableEntity, errResp)

		return
	}

	job, err := s.svc.Get(r.Context(), id.String())
	if err != nil {
		errResp := apiError{
			Code:    http.StatusNotFound,
			Message: http.StatusText(http.StatusNotFound),
		}

		renderJSON(w, http.StatusNotFound, errResp)

		return
	}

	renderJSON(w, http.StatusOK, job)
}

func (s *Server) apiDeleteJob(w http.ResponseWriter, r *http.Request) {
	id, ok := getIDFromRequest(r)
	if !ok {
		errResp := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID",
		}

		renderJSON(w, http.StatusUnprocessableEntity, errResp)

		return
	}

	err := s.svc.Delete(r.Context(), id.String())
	if err != nil {
		errResp := apiError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}

		renderJSON(w, http.StatusInternalServerError, errResp)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// streamEvents handles Server-Sent Events (SSE) streaming for real-time job updates.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	// Get job ID from request
	id, ok := getIDFromRequest(r)
	if !ok {
		errResp := apiError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid ID",
		}
		renderJSON(w, http.StatusUnprocessableEntity, errResp)

		return
	}

	// Verify job exists
	job, err := s.svc.Get(r.Context(), id.String())
	if err != nil {
		errResp := apiError{
			Code:    http.StatusNotFound,
			Message: "Job not found",
		}
		renderJSON(w, http.StatusNotFound, errResp)

		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx buffering

	// Create flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		errResp := apiError{
			Code:    http.StatusInternalServerError,
			Message: "Streaming not supported",
		}
		renderJSON(w, http.StatusInternalServerError, errResp)

		return
	}

	// Create a channel to receive events for this job
	eventChan := make(chan plugins.StreamEvent, 100)
	defer close(eventChan)

	// Register this connection to receive events for the job
	s.registerStreamClient(job.ID, eventChan)
	defer s.unregisterStreamClient(job.ID, eventChan)

	// Connection established, wait for real events from plugin
	flusher.Flush()

	// Disable WriteTimeout for this SSE connection to prevent premature closure
	if rc := http.NewResponseController(w); rc != nil {
		err := rc.SetWriteDeadline(time.Time{}) // Zero time = no deadline
		if err != nil {
			log.Printf("⚠️ Failed to disable write timeout for job %s: %v", job.ID, err)
		} else {
			log.Printf("✅ Disabled write timeout for SSE connection %s", job.ID)
		}
	}

	// Setup heartbeat for additional connection stability (proxy/load balancer protection)
	heartbeat := time.NewTicker(30 * time.Second) // Keep connection alive through intermediaries
	defer heartbeat.Stop()

	// Send initial heartbeat to establish connection
	log.Printf("💓 Sending initial heartbeat for job %s", job.ID)
	fmt.Fprintf(w, ": initial heartbeat\n\n")
	flusher.Flush()

	// Stream events until client disconnects
	for {
		select {
		case event := <-eventChan:
			if err := s.sendSSEEvent(w, event); err != nil {
				return
			}

			flusher.Flush()

		case <-heartbeat.C:
			// Send heartbeat as actual SSE event (not comment) to ensure it reaches client
			log.Printf("💓 Sending heartbeat for job %s", job.ID)

			heartbeatEvent := plugins.StreamEvent{
				Type:      "HEARTBEAT",
				Timestamp: time.Now(),
				JobID:     job.ID,
				Data:      map[string]interface{}{"message": "keepalive"},
			}

			if err := s.sendSSEEvent(w, heartbeatEvent); err != nil {
				log.Printf("❌ Heartbeat failed for job %s: %v", job.ID, err)
				return // Client disconnected
			}

			flusher.Flush()
			log.Printf("✅ Heartbeat sent successfully for job %s", job.ID)

		case <-r.Context().Done():
			// Client disconnected
			return
		}
	}
}

// sendSSEEvent formats and sends a single SSE event.
func (s *Server) sendSSEEvent(w io.Writer, event plugins.StreamEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	_, err = fmt.Fprintf(w, "id: %s\n", event.JobID)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "event: %s\n", event.Type)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	if err != nil {
		return err
	}

	return nil
}

// registerStreamClient registers a client to receive events for a specific job.
func (s *Server) registerStreamClient(jobID string, eventChan chan plugins.StreamEvent) {
	s.streamMu.Lock()

	if s.streamClients[jobID] == nil {
		s.streamClients[jobID] = make([]chan plugins.StreamEvent, 0)
	}

	s.streamClients[jobID] = append(s.streamClients[jobID], eventChan)

	// Replay buffered events to new client
	history := s.eventHistory[jobID]
	s.streamMu.Unlock()

	if len(history) > 0 {
		log.Printf("⏪ Replaying %d buffered events for job %s", len(history), jobID)

		go func() {
			for _, event := range history {
				select {
				case eventChan <- event:
					// Event replayed successfully
				default:
					// Client channel is full, stop replay
					log.Printf("⚠️ Client channel full during replay for job %s", jobID)
					return
				}
			}
		}()
	}

	log.Printf("📡 Registered stream client for job %s (total clients: %d)", jobID, len(s.streamClients[jobID]))
}

// unregisterStreamClient removes a client from receiving events for a specific job.
func (s *Server) unregisterStreamClient(jobID string, eventChan chan plugins.StreamEvent) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()

	clients := s.streamClients[jobID]
	if clients == nil {
		return
	}

	// Remove the channel from the slice
	for i, client := range clients {
		if client == eventChan {
			s.streamClients[jobID] = append(clients[:i], clients[i+1:]...)
			break
		}
	}

	// Clean up empty job entries and event history
	if len(s.streamClients[jobID]) == 0 {
		delete(s.streamClients, jobID)
		// Also clean up event history when no more clients
		delete(s.eventHistory, jobID)
		log.Printf("🧹 Cleaned up event history for completed job %s", jobID)
	}

	log.Printf("📡 Unregistered stream client for job %s (remaining clients: %d)", jobID, len(s.streamClients[jobID]))
}

// BroadcastEvent sends an event to all registered clients for a specific job (public interface).
func (s *Server) BroadcastEvent(jobID string, event plugins.StreamEvent) {
	s.broadcastEvent(jobID, event)
}

// broadcastEvent sends an event to all registered clients for a specific job.
func (s *Server) broadcastEvent(jobID string, event plugins.StreamEvent) {
	s.streamMu.Lock()

	// Store event in history buffer (keep last 50 events)
	const maxHistorySize = 50
	if s.eventHistory[jobID] == nil {
		s.eventHistory[jobID] = make([]plugins.StreamEvent, 0, maxHistorySize)
	}

	history := s.eventHistory[jobID]
	if len(history) >= maxHistorySize {
		// Remove oldest event
		history = history[1:]
	}

	history = append(history, event)
	s.eventHistory[jobID] = history

	clients := s.streamClients[jobID]
	s.streamMu.Unlock()

	// Send event to all currently connected clients
	for _, client := range clients {
		select {
		case client <- event:
			// Event sent successfully
		default:
			// Client channel is full, skip this client
			log.Printf("⚠️ Stream client channel full for job %s, dropping event", jobID)
		}
	}

	log.Printf("📦 Stored %s event for job %s (history size: %d)", event.Type, jobID, len(history))
}

func renderJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_ = json.NewEncoder(w).Encode(data)
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
