package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/gosom/google-maps-scraper/httpext"
	"github.com/gosom/google-maps-scraper/log"
	"github.com/gosom/google-maps-scraper/rqueue"
)

// IStore defines the interface for API storage operations.
type IStore interface {
	ValidateAPIKey(ctx context.Context, key string) (keyID int, keyName string, err error)
}

// AppState holds dependencies for API handlers.
type AppState struct {
	RQueue *rqueue.Client
	Store  IStore
}

// NewAppState creates a new API AppState.
func NewAppState(rqueue *rqueue.Client, store IStore) *AppState {
	return &AppState{
		RQueue: rqueue,
		Store:  store,
	}
}

// Routes sets up API routes on the given router.
func Routes(r chi.Router, appState *AppState) {
	r.Use(httpext.LoggingMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))
	r.Use(KeyAuth(appState.Store.ValidateAPIKey))

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthCheckHandler(appState))
		r.Post("/scrape", scrapeHandler(appState))
		r.Get("/jobs", listJobsHandler(appState))
		r.Get("/jobs/{job_id}", getJobHandler(appState))
		r.Delete("/jobs/{job_id}", deleteJobHandler(appState))
	})
}

// healthCheckHandler godoc
// @Summary Health check
// @Description Check if the API service is healthy
// @Tags health
// @Produce json
// @Success 200 {object} HealthCheckResponse
// @Router /api/v1/health [get]
func healthCheckHandler(_ *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		respondWithJSON(w, http.StatusOK, HealthCheckResponse{
			Status: "ok",
		})
	}
}

// scrapeHandler godoc
// @Summary Submit a scrape job
// @Description Submit a new Google Maps scrape job for processing
// @Tags scrape
// @Accept json
// @Produce json
// @Param request body ScrapeRequest true "Scrape request parameters"
// @Success 202 {object} ScrapeResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/scrape [post]
func scrapeHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Limit request body to 1MB to prevent DoS
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		var req ScrapeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondWithJSON(w, http.StatusBadRequest, ErrorResponse{
				Message: "invalid request body",
			})

			return
		}

		req.SetDefaults()

		if err := req.Validate(); err != nil {
			respondWithJSON(w, http.StatusBadRequest, ErrorResponse{
				Message: err.Error(),
			})

			return
		}

		jobArgs := rqueue.ScrapeJobArgs{
			Keyword:        req.Keyword,
			Lang:           req.Lang,
			MaxDepth:       req.MaxDepth,
			Email:          req.Email,
			GeoCoordinates: req.GeoCoordinates,
			Zoom:           req.Zoom,
			Radius:         req.Radius,
			FastMode:       req.FastMode,
			ExtraReviews:   req.ExtraReviews,
			TimeoutSecs:    req.Timeout,
		}

		jobID, err := appState.RQueue.InsertJob(r.Context(), jobArgs)
		if err != nil {
			log.Error("failed to insert job", "error", err, "job_id", jobID)
			respondWithJSON(w, http.StatusInternalServerError, ErrorResponse{
				Message: "failed to create job",
			})

			return
		}

		respondWithJSON(w, http.StatusAccepted, ScrapeResponse{
			JobID:  jobID,
			Status: "pending",
		})
	}
}

// listJobsHandler godoc
// @Summary List jobs
// @Description List all scrape jobs with pagination and optional state filtering
// @Tags scrape
// @Produce json
// @Param state query string false "Filter by state (available, running, completed, cancelled, discarded, pending, retryable, scheduled)"
// @Param limit query int false "Number of jobs to return (default: 20, max: 100)"
// @Param cursor query string false "Cursor for pagination"
// @Success 200 {object} ListJobsResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/jobs [get]
func listJobsHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		cursor := r.URL.Query().Get("cursor")

		limit := 20

		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
				limit = n
			}
		}

		if limit > 100 {
			limit = 100
		}

		result, err := appState.RQueue.ListJobs(r.Context(), state, limit, cursor)
		if err != nil {
			respondWithJSON(w, http.StatusBadRequest, ErrorResponse{
				Message: err.Error(),
			})

			return
		}

		jobs := make([]JobSummary, 0, len(result.Jobs))
		for _, job := range result.Jobs {
			jobs = append(jobs, JobSummary{
				JobID:       job.JobID,
				Status:      job.Status,
				Keyword:     job.Keyword,
				CreatedAt:   job.CreatedAt,
				StartedAt:   job.StartedAt,
				CompletedAt: job.CompletedAt,
				ResultCount: job.ResultCount,
				Error:       job.Error,
			})
		}

		respondWithJSON(w, http.StatusOK, ListJobsResponse{
			Jobs:       jobs,
			NextCursor: result.NextCursor,
			HasMore:    result.HasMore,
		})
	}
}

// getJobHandler godoc
// @Summary Get job status
// @Description Get the status and results of a scrape job
// @Tags scrape
// @Produce json
// @Param job_id path string true "Job ID"
// @Success 200 {object} JobStatusResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/jobs/{job_id} [get]
func getJobHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := chi.URLParam(r, "job_id")
		if jobID == "" {
			respondWithJSON(w, http.StatusBadRequest, ErrorResponse{
				Message: "job_id is required",
			})

			return
		}

		jobStatus, err := appState.RQueue.GetJobStatus(r.Context(), jobID)
		if err != nil {
			log.Error("failed to get job status", "error", err, "job_id", jobID)
			respondWithJSON(w, http.StatusNotFound, ErrorResponse{
				Message: "job not found",
			})

			return
		}

		var results any
		if jobStatus.Results != nil {
			_ = json.Unmarshal(jobStatus.Results, &results)
		}

		response := JobStatusResponse{
			JobID:       jobStatus.JobID,
			Status:      jobStatus.Status,
			Keyword:     jobStatus.Keyword,
			CreatedAt:   jobStatus.CreatedAt,
			StartedAt:   jobStatus.StartedAt,
			CompletedAt: jobStatus.CompletedAt,
			Results:     results,
			Error:       jobStatus.Error,
			ResultCount: jobStatus.ResultCount,
		}

		respondWithJSON(w, http.StatusOK, response)
	}
}

// deleteJobHandler godoc
// @Summary Delete a job
// @Description Queues deletion of a job and its results. Running jobs are cancelled first.
// @Tags scrape
// @Param job_id path string true "Job ID"
// @Success 202 {object} DeleteJobResponse
// @Failure 400 {object} ErrorResponse "Invalid job ID"
// @Failure 404 {object} ErrorResponse "Job not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /api/v1/jobs/{job_id} [delete]
func deleteJobHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := chi.URLParam(r, "job_id")
		if jobID == "" {
			respondWithJSON(w, http.StatusBadRequest, ErrorResponse{
				Message: "job_id is required",
			})

			return
		}

		err := appState.RQueue.DeleteJob(r.Context(), jobID)
		if err != nil {
			if containsNotFound(err) {
				respondWithJSON(w, http.StatusNotFound, ErrorResponse{
					Message: "job not found",
				})

				return
			}

			log.Error("failed to delete job", "error", err, "job_id", jobID)
			respondWithJSON(w, http.StatusInternalServerError, ErrorResponse{
				Message: err.Error(),
			})

			return
		}

		respondWithJSON(w, http.StatusAccepted, DeleteJobResponse{
			Message: "deletion queued",
		})
	}
}

// containsNotFound checks if the error message contains "not found".
func containsNotFound(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "not found")
}

func respondWithJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(payload)
}
