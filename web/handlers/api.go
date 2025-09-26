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
)

type apiScrapeRequest struct {
	Name string
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

// use renderJSON from handlers package (defined in web.go)
