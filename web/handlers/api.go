package handlers

import (
	"encoding/json"
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

type apiScrapeRequest struct {
	Name string `validate:"required"`
	models.JobData
}

// apiScrape mirrors Server.apiScrape behavior
func (h *APIHandlers) Scrape(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("POST %s", r.URL.Path)
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
		h.Deps.Logger.Printf(
			"CreateJob request: user_id=%s name=%q keywords=%d lang=%s depth=%d email=%t images=%t reviews_max=%d max_results=%d lat=%s lon=%s zoom=%d radius=%d max_time=%d fast_mode=%t proxies=%d",
			userID,
			req.Name,
			len(req.JobData.Keywords),
			req.JobData.Lang,
			req.JobData.Depth,
			req.JobData.Email,
			req.JobData.Images,
			req.JobData.ReviewsMax,
			req.JobData.MaxResults,
			req.JobData.Lat,
			req.JobData.Lon,
			req.JobData.Zoom,
			req.JobData.Radius,
			int64(req.JobData.MaxTime),
			req.JobData.FastMode,
			len(req.JobData.Proxies),
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
				h.Deps.Logger.Printf("ERROR: Failed to estimate job cost for user %s: %v", userID, err)
			}
			renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to estimate job cost"})
			return
		}

		// Log the estimate for debugging
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf(
				"Job cost estimate for user %s: total=%.4f credits (places=%d, reviews=%d, images=%d)",
				userID,
				estimate.TotalEstimatedCost,
				estimate.EstimatedPlaces,
				estimate.EstimatedReviews,
				estimate.EstimatedImages,
			)
		}

		// Check if user has sufficient balance
		if err := estimationSvc.CheckSufficientBalance(r.Context(), userID, estimate); err != nil {
			if h.Deps.Logger != nil {
				h.Deps.Logger.Printf("INFO: Job creation blocked for user %s: %v", userID, err)
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
			h.Deps.Logger.Printf("WARNING: Database not available, skipping cost estimation for user %s", userID)
		}
	}

	if err := h.Deps.App.Create(r.Context(), &newJob); err != nil {
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to create job: " + err.Error()})
		return
	}

	// Log created job id
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("CreateJob success: user_id=%s job_id=%s", userID, newJob.ID)
	}

	renderJSON(w, http.StatusCreated, models.ApiScrapeResponse{ID: newJob.ID})
}

func (h *APIHandlers) GetJobs(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
	}
	var jobs []models.Job
	var err error
	if h.Deps.Auth != nil {
		userID, err := auth.GetUserID(r.Context())
		if err != nil {
			renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
			return
		}
		jobs, err = h.Deps.App.All(r.Context(), userID)
	} else {
		jobs, err = h.Deps.App.All(r.Context(), "")
	}
	if err != nil {
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, jobs)
}

func (h *APIHandlers) GetUserJobs(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
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
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, jobs)
}

func (h *APIHandlers) GetJob(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
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
	job, err := h.Deps.App.Get(r.Context(), id.String())
	if err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)})
		return
	}
	renderJSON(w, http.StatusOK, job)
}

func (h *APIHandlers) DeleteJob(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("DELETE %s", r.URL.Path)
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
	if err := h.Deps.App.Delete(r.Context(), id.String()); err != nil {
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *APIHandlers) CancelJob(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("POST %s", r.URL.Path)
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
	if h.Deps.Auth != nil {
		userID, err := auth.GetUserID(r.Context())
		if err != nil {
			renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
			return
		}
		job, err := h.Deps.App.Get(r.Context(), id.String())
		if err != nil {
			renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found"})
			return
		}
		if job.UserID != userID {
			renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "Access denied"})
			return
		}
	}
	if err := h.Deps.App.Cancel(r.Context(), id.String()); err != nil {
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, map[string]any{"message": "Job cancellation initiated", "job_id": id.String()})
}

func (h *APIHandlers) GetJobResults(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
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
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	job, err := h.Deps.App.Get(r.Context(), jobID)
	if err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found"})
		return
	}
	if h.Deps.Auth != nil && job.UserID != userID {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "Access denied"})
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
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "Failed to get results: " + err.Error()})
		return
	}
	resp := models.PaginatedResultsResponse{Results: results, TotalCount: total, Page: page, Limit: limit, Offset: offset, TotalPages: (total + limit - 1) / limit, HasNext: offset+limit < total, HasPrev: page > 1}
	renderJSON(w, http.StatusOK, resp)
}

// GetJobCosts returns the cost breakdown and totals for a job
func (h *APIHandlers) GetJobCosts(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
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

	// Ensure the job belongs to the user
	job, err := h.Deps.App.Get(r.Context(), jobID)
	if err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found"})
		return
	}
	if job.UserID != userID {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "Access denied"})
		return
	}

	if h.Deps.DB == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "database not available"})
		return
	}

	cs := webservices.NewCostsService(h.Deps.DB)
	resp, err := cs.GetJobCosts(r.Context(), jobID)
	if err != nil {
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, resp)
}

func (h *APIHandlers) GetUserResults(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("GET %s", r.URL.Path)
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
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "Failed to get results: " + err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, results)
}

// EstimateJobCost returns the estimated cost for a job without creating it
func (h *APIHandlers) EstimateJobCost(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Printf("POST %s", r.URL.Path)
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
			h.Deps.Logger.Printf("ERROR: Failed to estimate job cost for user %s: %v", userID, err)
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to estimate job cost"})
		return
	}

	// Get user's current balance
	var creditBalance float64
	const query = `SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1`
	if err := h.Deps.DB.QueryRowContext(r.Context(), query, userID).Scan(&creditBalance); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Printf("ERROR: Failed to get credit balance for user %s: %v", userID, err)
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
