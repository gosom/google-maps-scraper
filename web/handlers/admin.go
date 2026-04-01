package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
)

// AdminHandlers contains routes for admin-only operations.
// Admin handlers bypass credit checks and concurrent job limits.
// They are protected by RequireRole("admin") middleware at the router level,
// but also perform a defense-in-depth role check in each handler.
type AdminHandlers struct{ Deps Dependencies }

// CreateJob creates a scraping job without credit checks or concurrent limits.
// The job is tagged with source="admin" so the billing system skips charging.
func (h *AdminHandlers) CreateJob(w http.ResponseWriter, r *http.Request) {
	// Defense-in-depth: verify admin role even though middleware already checks.
	if !auth.IsAdmin(r.Context()) {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin access required"})
		return
	}

	// Block API key access to admin routes — admin operations require Clerk JWT.
	// This prevents privilege escalation from a compromised API key.
	if auth.GetAPIKeyID(r.Context()) != "" {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin routes require session authentication, not API keys"})
		return
	}

	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	var req apiScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid request body"})
		return
	}
	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	newJob := models.Job{
		ID:     uuid.New().String(),
		UserID: userID,
		Name:   req.Name,
		Date:   time.Now().UTC(),
		Status: models.StatusPending,
		Data:   req.JobData,
		Source: models.SourceAdmin,
	}
	newJob.Data.MaxTime *= time.Second

	if err := webutils.ValidateJob(&newJob); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Warn("admin_job_created",
			slog.String("admin_user_id", userID),
			slog.String("job_id", newJob.ID),
			slog.String("job_name", newJob.Name),
			slog.Int("keywords", len(req.JobData.Keywords)),
			slog.Int("depth", req.JobData.Depth),
		)
	}

	// Bypass ConcurrentLimitService — use App.Create directly.
	// No credit check, no concurrent job limit enforcement.
	if err := h.Deps.App.Create(r.Context(), &newJob); err != nil {
		internalError(w, h.Deps.Logger, err, "admin job creation failed",
			slog.String("user_id", userID), slog.String("job_id", newJob.ID))
		return
	}

	renderJSON(w, http.StatusCreated, models.ApiScrapeResponse{ID: newJob.ID})
}

// GetJobs lists all jobs for the admin user.
func (h *AdminHandlers) GetJobs(w http.ResponseWriter, r *http.Request) {
	if !auth.IsAdmin(r.Context()) {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin access required"})
		return
	}
	if auth.GetAPIKeyID(r.Context()) != "" {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin routes require session authentication, not API keys"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	jobs, err := h.Deps.App.All(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to list admin jobs",
			slog.String("user_id", userID))
		return
	}
	renderJSON(w, http.StatusOK, jobs)
}

// CancelJob cancels an admin job.
func (h *AdminHandlers) CancelJob(w http.ResponseWriter, r *http.Request) {
	if !auth.IsAdmin(r.Context()) {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin access required"})
		return
	}
	if auth.GetAPIKeyID(r.Context()) != "" {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin routes require session authentication, not API keys"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	jobID := mux.Vars(r)["id"]
	if jobID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Warn("admin_job_cancelled",
			slog.String("admin_user_id", userID),
			slog.String("job_id", jobID),
		)
	}

	if err := h.Deps.App.Cancel(r.Context(), jobID, userID); err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found"})
		return
	}
	renderJSON(w, http.StatusOK, map[string]any{"message": "Admin job cancellation initiated", "job_id": jobID})
}
