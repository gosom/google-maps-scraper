package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
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
//
// On success (HTTP 200):
//
//	{"status":"ok","db":"ok","version":"<GIT_SHA>"}
//
// On DB failure (HTTP 503):
//
//	{"status":"unhealthy","db":"unreachable"}
//
// The DB probe runs SELECT 1 with a 3-second timeout rather than a bare Ping()
// so it validates that the connection can actually execute a round-trip query.
func (h *WebHandlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}

	if h.Deps.DB == nil {
		renderJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unhealthy",
			"db":     "unreachable",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var one int
	if err := h.Deps.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("health_db_probe_failed", slog.Any("error", err))
		}
		renderJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unhealthy",
			"db":     "unreachable",
		})
		return
	}

	v := h.Deps.Version
	if v == "" {
		v = "dev"
	}
	renderJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"db":      "ok",
		"version": v,
	})
}

// Redoc serves the API documentation page.
func (h *WebHandlers) Redoc(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
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
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
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

// Download mirrors Server.download with S3 support
func (h *WebHandlers) Download(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
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
			h.Deps.Logger.Warn("download_missing_id")
		}
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "Invalid ID format", http.StatusUnprocessableEntity)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("download_invalid_id", slog.Any("error", err))
		}
		return
	}

	// Check job status - block downloads for failed jobs (admin bypass for download)
	job, err := h.Deps.App.Get(r.Context(), id, "")
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("download_job_not_found", slog.String("job_id", id), slog.Any("error", err))
		}
		return
	}

	// Block access to failed jobs - billing failed
	if job.Status == "failed" {
		http.Error(w, "Cannot download results: billing failed for this job. Please ensure you have sufficient credits.", http.StatusPaymentRequired)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("download_blocked_failed_job", slog.String("job_id", id), slog.String("user_id", job.UserID))
		}
		return
	}

	// Use new GetCSVReader method which supports both S3 and local filesystem
	reader, fileName, err := h.Deps.App.GetCSVReader(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("download_csv_fetch_failed", slog.String("job_id", id), slog.Any("error", err))
		}
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.Header().Set("Content-Type", "text/csv")

	_, err = io.Copy(w, reader)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("download_send_failed", slog.String("file_name", fileName), slog.Any("error", err))
		}
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("csv_served", slog.String("file_name", fileName), slog.String("job_id", id))
	}
}

