package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	webservices "github.com/gosom/google-maps-scraper/web/services"
	webutils "github.com/gosom/google-maps-scraper/web/utils"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

// internalError logs err at ERROR level and writes a sanitized 500 response to w.
// The raw error is never sent to the client; only the generic userMsg is.
func internalError(w http.ResponseWriter, log *slog.Logger, err error, userMsg string) {
	if log != nil {
		log.Error("internal_error", slog.Any("error", err))
	}
	renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: userMsg})
}

type apiScrapeRequest struct {
	Name string `validate:"required"`
	models.JobData
}

// apiScrape mirrors Server.apiScrape behavior
func (h *APIHandlers) Scrape(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "POST"), slog.String("path", r.URL.Path))
	}

	var req apiScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}
	if h.Deps.Auth == nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	// Log request parameters for job creation (no secrets involved)
	if h.Deps.Logger != nil {
		// Note: MaxTime is in seconds for JSON API; multiplied to Duration below
		h.Deps.Logger.Info("create_job_request",
			slog.String("user_id", userID),
			slog.String("name", req.Name),
			slog.Int("keywords", len(req.JobData.Keywords)),
			slog.String("lang", req.JobData.Lang),
			slog.Int("depth", req.JobData.Depth),
			slog.Bool("email", req.JobData.Email),
			slog.Bool("images", req.JobData.Images),
			slog.Int("reviews_max", req.JobData.ReviewsMax),
			slog.Int("max_results", req.JobData.MaxResults),
			slog.String("lat", req.JobData.Lat),
			slog.String("lon", req.JobData.Lon),
			slog.Int("zoom", req.JobData.Zoom),
			slog.Int("radius", req.JobData.Radius),
			slog.Int64("max_time", int64(req.JobData.MaxTime)),
			slog.Bool("fast_mode", req.JobData.FastMode),
			slog.Int("proxies", len(req.JobData.Proxies)),
		)
	}

	newJob := models.Job{ID: uuid.New().String(), UserID: userID, Name: req.Name, Date: time.Now().UTC(), Status: models.StatusPending, Data: req.JobData}
	newJob.Data.MaxTime *= time.Second
	if err := webutils.ValidateJob(&newJob); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}

	// Pre-flight cost estimation and balance check
	if h.Deps.DB != nil {
		estimationSvc := webservices.NewEstimationService(h.Deps.DB)

		// Estimate job cost
		estimate, err := estimationSvc.EstimateJobCost(r.Context(), &newJob.Data)
		if err != nil {
			if h.Deps.Logger != nil {
				h.Deps.Logger.Error("job_cost_estimation_failed", slog.String("user_id", userID), slog.Any("error", err))
			}
			renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to estimate job cost"})
			return
		}

		// Log the estimate for debugging
		if h.Deps.Logger != nil {
			h.Deps.Logger.Info("job_cost_estimate",
				slog.String("user_id", userID),
				slog.Float64("total_estimated_cost", estimate.TotalEstimatedCost),
				slog.Int("estimated_places", estimate.EstimatedPlaces),
				slog.Int("estimated_reviews", estimate.EstimatedReviews),
				slog.Int("estimated_images", estimate.EstimatedImages),
			)
		}

		// Check if user has sufficient balance
		if err := estimationSvc.CheckSufficientBalance(r.Context(), userID, estimate); err != nil {
			if h.Deps.Logger != nil {
				h.Deps.Logger.Info("job_creation_blocked", slog.String("user_id", userID), slog.Any("error", err))
			}
			renderJSON(w, http.StatusPaymentRequired, models.APIError{
				Code:    http.StatusPaymentRequired,
				Message: err.Error(),
			})
			return
		}
	} else {
		// If database is not available, log warning but allow job creation
		// This maintains backward compatibility for non-billing deployments
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("db_unavailable_skipping_cost_estimation", slog.String("user_id", userID))
		}
	}

	if err := h.createJob(r.Context(), &newJob, w); err != nil {
		// createJob has already written the response on limit/error.
		return
	}

	// Log created job id
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("job_created", slog.String("user_id", userID), slog.String("job_id", newJob.ID))
	}

	renderJSON(w, http.StatusCreated, models.ApiScrapeResponse{ID: newJob.ID})
}

// concurrentLimitResponse is the 429 body when a user has hit their job cap.
type concurrentLimitResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Limit   int    `json:"limit"`
}

// createJob inserts a job, enforcing the concurrent job limit when the DB is
// available. Returns a non-nil error only when it has already written a response
// to w (so callers must not write another response on non-nil return).
func (h *APIHandlers) createJob(ctx context.Context, job *models.Job, w http.ResponseWriter) error {
	if h.Deps.ConcurrentLimitSvc != nil {
		err := h.Deps.ConcurrentLimitSvc.CreateJobWithLimit(ctx, job)
		if err != nil {
			var limitErr webservices.ErrConcurrentJobLimitReached
			if errors.As(err, &limitErr) {
				w.Header().Set("Retry-After", "60")
				renderJSON(w, http.StatusTooManyRequests, concurrentLimitResponse{
					Code:    http.StatusTooManyRequests,
					Message: "concurrent job limit reached",
					Limit:   limitErr.Limit,
				})
				return err
			}
			internalError(w, h.Deps.Logger, err, "job creation failed")
			return err
		}
		return nil
	}
	// No DB available — fall back to plain create (non-billing deployments).
	if err := h.Deps.App.Create(ctx, job); err != nil {
		internalError(w, h.Deps.Logger, err, "job creation failed")
		return err
	}
	return nil
}

func (h *APIHandlers) GetJobs(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}
	if h.Deps.Auth == nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	jobs, err := h.Deps.App.All(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "internal server error")
		return
	}
	renderJSON(w, http.StatusOK, jobs)
}

func (h *APIHandlers) GetUserJobs(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}
	if h.Deps.Auth == nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	jobs, err := h.Deps.App.All(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "internal server error")
		return
	}
	renderJSON(w, http.StatusOK, jobs)
}

func (h *APIHandlers) GetJob(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}
	idStr := mux.Vars(r)["id"]
	if idStr == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid ID format"})
		return
	}
	userID := ""
	if h.Deps.Auth != nil {
		uid, err := auth.GetUserID(r.Context())
		if err != nil {
			renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
			return
		}
		userID = uid
	}
	job, err := h.Deps.App.Get(r.Context(), id.String(), userID)
	if err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)})
		return
	}
	renderJSON(w, http.StatusOK, job)
}

func (h *APIHandlers) DeleteJob(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "DELETE"), slog.String("path", r.URL.Path))
	}
	idStr := mux.Vars(r)["id"]
	if idStr == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid ID format"})
		return
	}
	userID := ""
	if h.Deps.Auth != nil {
		uid, err := auth.GetUserID(r.Context())
		if err != nil {
			renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
			return
		}
		userID = uid
	}
	if err := h.Deps.App.Delete(r.Context(), id.String(), userID); err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *APIHandlers) CancelJob(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "POST"), slog.String("path", r.URL.Path))
	}
	idStr := mux.Vars(r)["id"]
	if idStr == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid ID format"})
		return
	}
	userID := ""
	if h.Deps.Auth != nil {
		uid, err := auth.GetUserID(r.Context())
		if err != nil {
			renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
			return
		}
		userID = uid
	}
	if err := h.Deps.App.Cancel(r.Context(), id.String(), userID); err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)})
		return
	}
	renderJSON(w, http.StatusOK, map[string]any{"message": "Job cancellation initiated", "job_id": id.String()})
}

func (h *APIHandlers) GetJobResults(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}
	jobID := mux.Vars(r)["id"]
	if jobID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}
	page := 1
	limit := 50
	if v := r.URL.Query().Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			page = p
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}
	offset := (page - 1) * limit
	userID := ""
	if h.Deps.Auth != nil {
		uid, err := auth.GetUserID(r.Context())
		if err != nil {
			renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
			return
		}
		userID = uid
	}
	job, err := h.Deps.App.Get(r.Context(), jobID, userID)
	if err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found"})
		return
	}

	// Block access to failed jobs - billing failed
	if job.Status == models.StatusFailed {
		renderJSON(w, http.StatusPaymentRequired, models.APIError{
			Code:    http.StatusPaymentRequired,
			Message: "Cannot access results: billing failed for this job. Please ensure you have sufficient credits.",
		})
		return
	}

	results, total, err := h.Deps.ResultsSvc.GetEnhancedJobResultsPaginated(r.Context(), jobID, limit, offset)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to retrieve results")
		return
	}
	resp := models.PaginatedResultsResponse{Results: results, TotalCount: total, Page: page, Limit: limit, Offset: offset, TotalPages: (total + limit - 1) / limit, HasNext: offset+limit < total, HasPrev: page > 1}
	renderJSON(w, http.StatusOK, resp)
}

// GetJobCosts returns the cost breakdown and totals for a job
func (h *APIHandlers) GetJobCosts(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}
	// Require auth
	if h.Deps.Auth == nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	jobID := mux.Vars(r)["id"]
	if jobID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}

	// Ensure the job belongs to the user (ownership enforced in DB query)
	_, err = h.Deps.App.Get(r.Context(), jobID, userID)
	if err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found"})
		return
	}

	if h.Deps.DB == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "database not available"})
		return
	}

	cs := webservices.NewCostsService(h.Deps.DB)
	resp, err := cs.GetJobCosts(r.Context(), jobID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to retrieve job costs")
		return
	}
	renderJSON(w, http.StatusOK, resp)
}

func (h *APIHandlers) GetUserResults(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if o, err := strconv.Atoi(v); err == nil && o >= 0 {
			offset = o
		}
	}
	results, err := h.Deps.ResultsSvc.GetUserResults(r.Context(), userID, limit, offset)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to retrieve results")
		return
	}
	renderJSON(w, http.StatusOK, results)
}

// EstimateJobCost returns the estimated cost for a job without creating it
func (h *APIHandlers) EstimateJobCost(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "POST"), slog.String("path", r.URL.Path))
	}

	var req apiScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	if h.Deps.Auth == nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}

	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	if h.Deps.DB == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "database not available"})
		return
	}

	// Create estimation service
	estimationSvc := webservices.NewEstimationService(h.Deps.DB)

	// Estimate job cost
	estimate, err := estimationSvc.EstimateJobCost(r.Context(), &req.JobData)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("job_cost_estimation_failed", slog.String("user_id", userID), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to estimate job cost"})
		return
	}

	// Get user's current balance
	var creditBalance float64
	const query = `SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1`
	if err := h.Deps.DB.QueryRowContext(r.Context(), query, userID).Scan(&creditBalance); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("credit_balance_fetch_failed", slog.String("user_id", userID), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to retrieve credit balance"})
		return
	}

	// Build response with estimate and balance info
	response := map[string]interface{}{
		"estimate":               estimate,
		"current_credit_balance": creditBalance,
		"sufficient_balance":     creditBalance >= estimate.TotalEstimatedCost,
	}

	renderJSON(w, http.StatusOK, response)
}

// use renderJSON from handlers package (defined in web.go)
