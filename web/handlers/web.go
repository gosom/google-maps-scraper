package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/postgres"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
)

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

//nolint:gocritic // used in template
func (f formData) ProxiesString() string { return strings.Join(f.Proxies, "\n") }

//nolint:gocritic // used in template
func (f formData) KeywordsString() string { return strings.Join(f.Keywords, "\n") }

// HealthCheck responds with service and database health info.
func (h *WebHandlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
	}

	dbStatus := "not_configured"
	if h.Deps.DB != nil {
		if err := h.Deps.DB.Ping(); err != nil {
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

	if dbStatus == "unhealthy" {
		renderJSON(w, http.StatusServiceUnavailable, response)
		return
	}
	renderJSON(w, http.StatusOK, response)
}

// Redoc serves the API documentation page.
func (h *WebHandlers) Redoc(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
	}
	if h.Deps.Templates == nil {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		return
	}
	tmpl, ok := h.Deps.Templates["static/templates/redoc.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, nil)
}

// minimal renderJSON copy to keep handlers self-contained during migration
func renderJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

// Index mirrors Server.index
func (h *WebHandlers) Index(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmpl, ok := h.Deps.Templates["static/templates/index.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		return
	}
	data := formData{"", "10m", []string{}, "en", 15, false, 10000, "0", "0", 10, false, false, 1, nil}
	_ = tmpl.Execute(w, data)
}

// Jobs mirrors Server.jobs
func (h *WebHandlers) Jobs(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jobs, err := h.Deps.App.All(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, ok := h.Deps.Templates["static/templates/job_rows.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, jobs)
}

// Scrape mirrors Server.scrape
func (h *WebHandlers) Scrape(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("POST %s", r.URL.Path)
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	newJob := models.Job{ID: uuid.New().String(), UserID: "default_user_id", Name: r.Form.Get("name"), Date: time.Now().UTC(), Status: models.StatusPending, Data: models.JobData{}}
	if h.Deps.UserRepo != nil {
		if _, err := h.Deps.UserRepo.GetByID(r.Context(), "default_user_id"); err != nil {
			_ = h.Deps.UserRepo.Create(r.Context(), &postgres.User{ID: "default_user_id", Email: "webui@example.com"})
		}
	}
	maxTimeStr := r.Form.Get("maxtime")
	maxTime, err := time.ParseDuration(maxTimeStr)
	if err != nil {
		http.Error(w, "invalid max time", http.StatusUnprocessableEntity)
		return
	}
	if maxTime < 3*time.Minute {
		http.Error(w, "max time must be more than 3m", http.StatusUnprocessableEntity)
		return
	}
	newJob.Data.MaxTime = maxTime
	if keywordsStrs, ok := r.Form["keywords"]; ok {
		for _, k := range strings.Split(keywordsStrs[0], "\n") {
			if s := strings.TrimSpace(k); s != "" {
				newJob.Data.Keywords = append(newJob.Data.Keywords, s)
			}
		}
	} else {
		http.Error(w, "missing keywords", http.StatusUnprocessableEntity)
		return
	}
	newJob.Data.Lang = r.Form.Get("lang")
	if newJob.Data.Zoom, err = strconv.Atoi(r.Form.Get("zoom")); err != nil {
		http.Error(w, "invalid zoom", http.StatusUnprocessableEntity)
		return
	}
	newJob.Data.FastMode = r.Form.Get("fastmode") == "on"
	if newJob.Data.Radius, err = strconv.Atoi(r.Form.Get("radius")); err != nil {
		http.Error(w, "invalid radius", http.StatusUnprocessableEntity)
		return
	}
	newJob.Data.Lat = r.Form.Get("latitude")
	newJob.Data.Lon = r.Form.Get("longitude")
	if newJob.Data.Depth, err = strconv.Atoi(r.Form.Get("depth")); err != nil {
		http.Error(w, "invalid depth", http.StatusUnprocessableEntity)
		return
	}
	newJob.Data.Email = r.Form.Get("email") == "on"
	newJob.Data.Images = r.Form.Get("images") == "on"
	if v := r.Form.Get("reviews_max"); v != "" {
		if newJob.Data.ReviewsMax, err = strconv.Atoi(v); err != nil {
			http.Error(w, "invalid reviews_max", http.StatusUnprocessableEntity)
			return
		}
	} else {
		newJob.Data.ReviewsMax = 1
	}
	if v := r.Form.Get("max_results"); v != "" {
		if newJob.Data.MaxResults, err = strconv.Atoi(v); err != nil {
			http.Error(w, "invalid max_results", http.StatusUnprocessableEntity)
			return
		}
	}
	for _, p := range strings.Split(r.Form.Get("proxies"), "\n") {
		if s := strings.TrimSpace(p); s != "" {
			newJob.Data.Proxies = append(newJob.Data.Proxies, s)
		}
	}
	if err := webutils.ValidateJob(&newJob); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err := h.Deps.App.Create(r.Context(), &newJob); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, ok := h.Deps.Templates["static/templates/job_row.html"]
	if !ok {
		http.Error(w, "missing tpl", http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, newJob)
}

// Download mirrors Server.download with S3 support
func (h *WebHandlers) Download(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		if vars := mux.Vars(r); vars != nil {
			id = vars["id"]
		}
	}
	if id == "" {
		http.Error(w, "Missing ID", http.StatusUnprocessableEntity)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf("Missing ID for download")
		}
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "Invalid ID format", http.StatusUnprocessableEntity)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf("Invalid ID format for download: %v", err)
		}
		return
	}

	// Check job status - block downloads for failed jobs
	job, err := h.Deps.App.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf("Job %s not found for download: %v", id, err)
		}
		return
	}

	// Block access to failed jobs - billing failed
	if job.Status == "failed" {
		http.Error(w, "Cannot download results: billing failed for this job. Please ensure you have sufficient credits.", http.StatusPaymentRequired)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf("Download blocked for failed job %s (user: %s)", id, job.UserID)
		}
		return
	}

	// Use new GetCSVReader method which supports both S3 and local filesystem
	reader, fileName, err := h.Deps.App.GetCSVReader(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf("Failed to get CSV for job %s: %v", id, err)
		}
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", "text/csv")

	_, err = io.Copy(w, reader)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf("Failed to send file %s: %v", fileName, err)
		}
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("Successfully served CSV file %s for job %s", fileName, id)
	}
}

// Delete mirrors Server.delete
func (h *WebHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("DELETE %s", r.URL.Path)
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "Invalid ID", http.StatusUnprocessableEntity)
		return
	}
	if err := h.Deps.App.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
