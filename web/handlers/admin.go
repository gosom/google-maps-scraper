package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
)

const (
	errMsgAdminRequired    = "admin access required"
	errMsgAPIKeyForbidden  = "admin routes require session authentication, not API keys"
)

// AdminHandlers contains routes for admin-only operations.
// Admin handlers bypass credit checks and concurrent job limits.
// They are protected by RequireRole("admin") middleware at the router level,
// but also perform a defense-in-depth role check in each handler via
// requireAdminSession.
type AdminHandlers struct{ Deps Dependencies }

// requireAdminSession validates that the request comes from an authenticated
// admin user via Clerk JWT (not an API key). It returns the user ID on success.
// On failure it writes the error response to w and returns ("", false).
//
// This is a defense-in-depth check: the RequireRole middleware at the router
// level is the primary gate, but this guard ensures admin-only behaviour even
// if the middleware is misconfigured.
func requireAdminSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !auth.IsAdmin(r.Context()) {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: errMsgAdminRequired})
		return "", false
	}
	// Block API key access — admin operations require Clerk JWT sessions.
	// This prevents privilege escalation from a compromised API key.
	if auth.GetAPIKeyID(r.Context()) != "" {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: errMsgAPIKeyForbidden})
		return "", false
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return "", false
	}
	return userID, true
}

// CreateJob creates a scraping job without credit checks or concurrent limits.
// The job is tagged with source="admin" so the billing system skips charging.
func (h *AdminHandlers) CreateJob(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireAdminSession(w, r)
	if !ok {
		return
	}

	var req apiScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid request body"})
		return
	}
	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: formatValidationErrors(err)})
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

	// Bypass ConcurrentLimitService — use App.Create directly.
	// No credit check, no concurrent job limit enforcement.
	if err := h.Deps.App.Create(r.Context(), &newJob); err != nil {
		internalError(w, h.Deps.Logger, err, "admin job creation failed",
			slog.String("user_id", userID), slog.String("job_id", newJob.ID))
		return
	}

	// Admin actions are logged at Warn level (not Info) so they stand out in
	// log aggregation dashboards and can be filtered for audit review.
	if h.Deps.Logger != nil {
		h.Deps.Logger.Warn("admin_job_created",
			slog.String("admin_user_id", userID),
			slog.String("job_id", newJob.ID),
			slog.String("job_name", newJob.Name),
			slog.Int("keywords", len(req.JobData.Keywords)),
			slog.Int("depth", req.JobData.Depth),
		)
	}

	renderJSON(w, http.StatusCreated, models.ApiScrapeResponse{ID: newJob.ID})
}

// GetJobs lists the admin user's own jobs.
func (h *AdminHandlers) GetJobs(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireAdminSession(w, r)
	if !ok {
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Warn("admin_jobs_listed",
			slog.String("admin_user_id", userID),
		)
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
	userID, ok := requireAdminSession(w, r)
	if !ok {
		return
	}

	jobID := mux.Vars(r)["id"]
	if jobID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}

	if err := h.Deps.App.Cancel(r.Context(), jobID, userID); err != nil {
		// Distinguish "not found" from unexpected errors so DB failures
		// return 500 instead of a misleading 404.
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "cannot be cancelled") {
			renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found or cannot be cancelled"})
		} else {
			internalError(w, h.Deps.Logger, err, "admin job cancellation failed",
				slog.String("user_id", userID), slog.String("job_id", jobID))
		}
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Warn("admin_job_cancel_completed",
			slog.String("admin_user_id", userID),
			slog.String("job_id", jobID),
		)
	}

	renderJSON(w, http.StatusOK, map[string]any{"message": "Admin job cancellation initiated", "job_id": jobID})
}
