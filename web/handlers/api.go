package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	webservices "github.com/gosom/google-maps-scraper/web/services"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
	"github.com/shopspring/decimal"
)

var validate = newValidator()

// newValidator wires a process-wide validator that surfaces JSON tag names
// (e.g. "max_results") in error messages instead of the Go struct field names
// (e.g. "MaxResults"). Without this, the lowercased struct name collapses
// snake_case into the awful "maxresults", which we shipped to production.
func newValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" || name == "" {
			return fld.Name
		}
		return name
	})
	return v
}

// internalError logs err at ERROR level and writes a sanitized 500 response to w.
// The raw error is never sent to the client; only the generic userMsg is.
// Extra slog.Attr values (e.g. user_id, job_id, path, method) can be appended
// for Grafana/Loki traceability.
func internalError(w http.ResponseWriter, log *slog.Logger, err error, userMsg string, extra ...slog.Attr) {
	if log != nil {
		attrs := []slog.Attr{slog.Any("error", err)}
		attrs = append(attrs, extra...)
		args := make([]any, len(attrs))
		for i, a := range attrs {
			args[i] = a
		}
		log.Error("internal_error", args...)
	}
	renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: userMsg})
}

type apiScrapeRequest struct {
	Name string `json:"name" validate:"required,min=1,max=200"`
	models.JobData
}

// estimateRequest is the request body for POST /api/v1/jobs/estimates.
// MaxResults is a pointer so nil means "not set by user" (use depth-based
// estimation), while a positive value means "user-provided hard cap".
type estimateRequest struct {
	Keywords      []string `json:"keywords" validate:"required,min=1,max=5,dive,min=1,max=200"`
	Depth         int      `json:"depth" validate:"min=0,max=20"`
	IncludeEmails bool     `json:"include_emails"`
	MaxImages     *int     `json:"max_images,omitempty" validate:"omitempty,min=0,max=500"`
	MaxReviews    *int     `json:"max_reviews,omitempty" validate:"omitempty,min=0,max=500"`
	MaxResults    *int     `json:"max_results,omitempty" validate:"omitempty,min=1,max=500"`
}

// estimateBalance is the nested balance sub-object in the estimate response.
type estimateBalance struct {
	Current    float64 `json:"current"`
	Sufficient bool    `json:"sufficient"`
}

// estimateResponse is the typed response for EstimateJobCost.
type estimateResponse struct {
	Estimate *webservices.CostEstimate `json:"estimate"`
	Balance  estimateBalance           `json:"balance"`
}

// apiScrape mirrors Server.apiScrape behavior
func (h *APIHandlers) Scrape(w http.ResponseWriter, r *http.Request) {
	var req apiScrapeRequest
	if err := decodeStrict(r, &req); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("json_decode_failed", slog.String("path", r.URL.Path), slog.Any("error", err))
		}
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid request body"})
		return
	}

	// Fill in REST-conservative defaults for omitted optional fields BEFORE
	// running struct-tag validation. This is the API safety net for direct
	// callers (curl, scripts, integrations) that don't supply every field.
	// Frontend "no cap" toggles send the hard ceiling explicitly — see
	// cap_params.go for the design rationale.
	webutils.ApplyJobDataDefaults(&req.JobData)

	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: formatValidationErrors(err)})
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
		// Note: MaxTime auto-converts between seconds (JSON) and Duration (internal) via DurationSec
		h.Deps.Logger.Info("create_job_request",
			slog.String("user_id", userID),
			slog.String("name", req.Name),
			slog.Int("keywords", len(req.JobData.Keywords)),
			slog.String("language", req.JobData.Language),
			slog.Int("depth", req.JobData.Depth),
			slog.Bool("include_emails", req.JobData.IncludeEmails),
			slog.Int("max_images", req.JobData.MaxImages),
			slog.Int("max_reviews", req.JobData.MaxReviews),
			slog.Int("max_results", req.JobData.MaxResults),
			slog.String("lat", req.JobData.Lat),
			slog.String("lon", req.JobData.Lon),
			slog.Int("zoom", req.JobData.Zoom),
			slog.Int("radius", req.JobData.Radius),
			slog.Int64("max_time_seconds", int64(req.JobData.MaxTime.Duration().Seconds())),
			slog.Bool("fast_mode", req.JobData.FastMode),
			slog.Int("proxies", len(req.JobData.Proxies)),
		)
	}

	newJob := models.Job{ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Name: req.Name, Date: time.Now().UTC(), Status: models.StatusPending, Data: req.JobData}
	if auth.GetAPIKeyID(r.Context()) != "" {
		newJob.Source = models.SourceAPI
	} else {
		newJob.Source = models.SourceWeb
	}
	if err := webutils.ValidateJob(&newJob); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}

	// Pre-flight cost estimation and balance check.
	// The standalone balance check here is a fast-fail optimisation only. The
	// authoritative, race-free check happens inside CreateJobWithLimit under
	// a SELECT ... FOR UPDATE lock (see concurrent_limit.go).
	var estimateOpts *webservices.JobLimitOpts
	if h.Deps.DB != nil {
		estimationSvc := webservices.NewEstimationService(h.Deps.DB, h.Deps.PricingRuleRepo, h.Deps.Logger)

		// Estimate job cost. The three caps map to the EstimationService's
		// pointer-typed inputs with DIFFERENT semantics — keep them straight:
		//
		//   MaxResults=0  → "no hard cap, use depth-based natural yield".
		//                   Pass nil; estimator computes rawEstimate from
		//                   depth × keywords.
		//
		//   MaxReviews=0  → "toggle off, scrape no reviews".
		//                   Pass &0; estimator's nil-branch would otherwise
		//                   apply AvgReviewsPerPlace=50, charging the user
		//                   for reviews they didn't ask for. This was the
		//                   May 10 prod 402 bug: the estimate endpoint
		//                   typed MaxReviews as *int and forwarded &0, so
		//                   the user-visible cost (0.127) didn't match the
		//                   create endpoint's silent 1.727.
		//
		//   MaxImages=0   → same as MaxReviews ("toggle off" — pass &0,
		//                   skip the AvgImagesPerPlace=30 fallback). NOTE:
		//                   May 2026 — Cafe Schöneberg fix — semantics
		//                   switched from per-job-total to per-place. The
		//                   estimator now multiplies by primaryEstimate,
		//                   so under-quoting a 10/place × 40 places job is
		//                   no longer possible.
		//
		// ApplyJobDataDefaults at the entry point already pins the
		// "0 means toggle off" convention for MaxReviews/MaxImages, so we
		// trust the values here and forward unconditionally.
		var mrPtr *int
		if v := newJob.Data.MaxResults; v > 0 {
			mrPtr = &v
		}
		// Local copies so the address is stable across the EstimateJobCost
		// call (loop-variable / parameter capture safety, not strictly
		// required here but cheap and idiomatic).
		rv := newJob.Data.MaxReviews
		im := newJob.Data.MaxImages
		rvPtr := &rv
		imPtr := &im
		estimate, err := estimationSvc.EstimateJobCost(
			r.Context(),
			newJob.Data.Keywords,
			newJob.Data.Depth,
			mrPtr,
			newJob.Data.IncludeEmails,
			rvPtr,
			imPtr,
		)
		if err != nil {
			// Pricing-layer outage (DB unreachable, pricing_rules empty)
			// is a 503: estimate-as-quote semantics require an authoritative
			// price; we will not silently default and risk billing the user
			// at a different rate than what they were quoted.
			if errors.Is(err, webservices.ErrPricingUnavailable) {
				if h.Deps.Logger != nil {
					h.Deps.Logger.Error("pricing_unavailable",
						slog.String("user_id", userID), slog.Any("error", err))
				}
				w.Header().Set("Retry-After", "30")
				renderJSON(w, http.StatusServiceUnavailable, models.APIError{
					Code:    http.StatusServiceUnavailable,
					Message: "Billing service is temporarily unavailable. Please try again in a few seconds.",
				})
				return
			}
			if h.Deps.Logger != nil {
				h.Deps.Logger.Error("job_cost_estimation_failed",
					slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
			}
			renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to estimate job cost"})
			return
		}

		// Log the estimate for debugging
		if h.Deps.Logger != nil {
			h.Deps.Logger.Info("job_cost_estimate",
				slog.String("user_id", userID),
				slog.Float64("total", estimate.Total),
				slog.Int("places", estimate.Places),
				slog.Int("reviews", estimate.Reviews),
				slog.Int("images", estimate.Images),
			)
		}

		// Fast-fail: reject obviously insufficient AVAILABLE balance
		// (credit_balance - credit_held_precise) before taking a row
		// lock. CheckSufficientBalance now compares against the same
		// Total that the transactional gate uses, so the fast-fail and
		// the authoritative check can never disagree on the same input
		// (pre-2026-05-10 the fast-fail used MinTotal and the gate used
		// Total — a user with balance ∈ [Min, Total) would silently pass
		// here and 402 inside the transaction, which was confusing).
		if err := estimationSvc.CheckSufficientBalance(r.Context(), userID, estimate); err != nil {
			if h.Deps.Logger != nil {
				h.Deps.Logger.Info("job_creation_blocked", slog.String("user_id", userID), slog.Any("error", err))
			}
			// Render user-facing message via the typed error's
			// UserMessage. err.Error() is the stable low-cardinality
			// grouping key for log aggregators ("insufficient
			// available balance"); the wire string the user sees is
			// the formatted UserMessage with current numbers.
			userMsg := err.Error()
			var balanceErr webservices.ErrInsufficientBalance
			if errors.As(err, &balanceErr) {
				userMsg = balanceErr.UserMessage()
			}
			renderJSON(w, http.StatusPaymentRequired, models.APIError{
				Code:    http.StatusPaymentRequired,
				Message: userMsg,
			})
			return
		}

		// Pass the estimate to createJob. The transactional gate inside
		// CreateJobWithLimit re-checks affordability under FOR UPDATE,
		// reserves credit_held_precise += estimate.Total, and persists
		// the same value to jobs.estimated_cost_precise as the
		// user-facing quote. The hold is released at job end by
		// webrunner.go regardless of outcome.
		estimateOpts = &webservices.JobLimitOpts{
			EstimatedCost:   estimate.Total,
			EstimatedPlaces: estimate.Places,
		}
	} else {
		// If database is not available, log warning but allow job creation
		// This maintains backward compatibility for non-billing deployments
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("db_unavailable_skipping_cost_estimation", slog.String("user_id", userID))
		}
	}

	if err := h.createJob(r.Context(), &newJob, w, estimateOpts); err != nil {
		// createJob has already written the response on limit/error.
		return
	}

	// Log created job id
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("job_created", slog.String("user_id", userID), slog.String("job_id", newJob.ID))
	}

	renderJSON(w, http.StatusCreated, models.ApiScrapeResponse{ID: newJob.ID})
}

// createJob inserts a job, enforcing the concurrent job limit and credit
// balance check when the DB is available. The balance check inside the
// transaction (via opts) is the authoritative check that prevents TOCTOU races.
// Returns a non-nil error only when it has already written a response
// to w (so callers must not write another response on non-nil return).
func (h *APIHandlers) createJob(ctx context.Context, job *models.Job, w http.ResponseWriter, opts *webservices.JobLimitOpts) error {
	if h.Deps.ConcurrentLimitSvc != nil {
		err := h.Deps.ConcurrentLimitSvc.CreateJobWithLimit(ctx, job, opts)
		if err != nil {
			var limitErr webservices.ErrConcurrentJobLimitReached
			if errors.As(err, &limitErr) {
				w.Header().Set("Retry-After", "60")
				renderJSON(w, http.StatusTooManyRequests, models.APIError{
					Code:    http.StatusTooManyRequests,
					Message: fmt.Sprintf("concurrent job limit reached (%d active jobs)", limitErr.Limit),
				})
				return err
			}
			var balanceErr webservices.ErrInsufficientBalance
			if errors.As(err, &balanceErr) {
				// UserMessage formats the figures for the response;
				// Error() stays as the low-cardinality grouping key
				// in logs (the failure was already logged by the
				// service layer or will be by internalError).
				renderJSON(w, http.StatusPaymentRequired, models.APIError{
					Code:    http.StatusPaymentRequired,
					Message: balanceErr.UserMessage(),
				})
				return err
			}
			internalError(w, h.Deps.Logger, err, "job creation failed",
				slog.String("user_id", job.UserID), slog.String("job_id", job.ID))
			return err
		}
		return nil
	}
	// No DB available — fall back to plain create (non-billing deployments).
	if err := h.Deps.App.Create(ctx, job); err != nil {
		internalError(w, h.Deps.Logger, err, "job creation failed",
			slog.String("user_id", job.UserID), slog.String("job_id", job.ID))
		return err
	}
	return nil
}

// parseJobID extracts and validates the {id} path variable from a
// gorilla/mux request. Returns the canonical lowercase UUID string on
// success or an error suitable for rendering as a 422 response.
//
// Why this helper exists: GetJob, DeleteJob, and CancelJob each had
// their own copy of this five-line block, but GetJobResults and
// GetJobCosts forgot to validate at all — passing an arbitrary string
// straight to the SQL layer. The arbitrary string couldn't trigger SQL
// injection (the query uses placeholders) but it WAS leaking
// db-specific error messages back to the client when the cast failed,
// helping an attacker fingerprint the database.
//
// Centralizing the parse here ensures (a) every job-id endpoint
// validates the same way, and (b) adding a new endpoint is one
// `parseJobID(r)` call instead of five lines of boilerplate that
// might get skipped.
//
// Returns the canonical (lowercase, hyphen-separated) UUID form so
// downstream queries see a normalized value regardless of how the
// client cased the input.
func parseJobID(r *http.Request) (string, error) {
	raw := mux.Vars(r)["id"]
	if raw == "" {
		return "", errors.New("missing job ID")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", errors.New("invalid job ID format")
	}
	return id.String(), nil
}

// allowedJobSorts is the closed allowlist of columns the ListJobs
// endpoint will sort by. Anything outside this set is rejected with 400
// — this prevents an attacker from sniffing column names by passing
// `?sort=password` and observing whether the request succeeds.
//
// Security: this MUST be a literal set, not a tag scan or reflection
// over models.Job. A reflection-based allowlist would silently include
// any field added to the Job struct in the future, defeating the
// allowlist. Adding a column requires a deliberate code change here.
var allowedJobSorts = map[string]struct{}{
	"created_at": {},
	"name":       {},
	"status":     {},
	"updated_at": {},
}

// maxJobSearchLen caps the `?search=` query parameter at 200 bytes.
// The search value flows into a SQL ILIKE in the repository layer; an
// unbounded search string forces the database into a full table scan
// against arbitrary input, which is both a CWE-400 and a slow-path
// amplifier (one user can DoS the jobs list for everyone). 200 bytes
// is generous for human-typed search input — anything beyond that is
// almost certainly an attack or a buggy client.
const maxJobSearchLen = 200

func (h *APIHandlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Auth == nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	// Parse pagination query parameters with the unified helper. The
	// 10-row default is preserved for this endpoint (smaller than the
	// 50-row default used for results) because the dashboard expects
	// to show ~10 jobs per page in the UI.
	q := r.URL.Query()
	page, limit, _, err := parsePagination(r, 10)
	if err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	sort := "created_at"
	if v := q.Get("sort"); v != "" {
		// Strict allowlist — see allowedJobSorts above. Reject unknown
		// values with 400 instead of silently coercing to default,
		// which would mask client typos and let an attacker fingerprint
		// the schema by observing default-vs-explicit behavior.
		if _, ok := allowedJobSorts[v]; !ok {
			renderJSON(w, http.StatusBadRequest, models.APIError{
				Code:    http.StatusBadRequest,
				Message: "invalid sort field (allowed: created_at, name, status, updated_at)",
			})
			return
		}
		sort = v
	}

	order := "desc"
	if v := q.Get("order"); v == "asc" || v == "desc" {
		order = v
	}

	search := q.Get("search")
	// len() is bytes, not runes — that matches what the validator/v10
	// `max=N` tag does in models/job.go and what the SQL LIKE engine
	// actually consumes. A 200-byte cap is ~200 ASCII characters or
	// ~50-66 CJK characters; both are well above any human-typed
	// search input.
	if len(search) > maxJobSearchLen {
		renderJSON(w, http.StatusBadRequest, models.APIError{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("search exceeds maximum length of %d bytes", maxJobSearchLen),
		})
		return
	}

	params := models.PaginatedJobsParams{
		UserID: userID,
		Page:   page,
		Limit:  limit,
		Sort:   sort,
		Order:  order,
		Search: search,
	}

	jobs, total, err := h.Deps.App.AllPaginated(r.Context(), params)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "internal server error",
			slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method))
		return
	}

	resp := models.PaginatedJobsResponse{
		Jobs:    jobs,
		Total:   total,
		Page:    page,
		Limit:   limit,
		HasMore: page*limit < total,
	}

	renderJSON(w, http.StatusOK, resp)
}

func (h *APIHandlers) GetJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := parseJobID(r)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	job, err := h.Deps.App.Get(r.Context(), jobID, userID)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("get_job_failed", slog.String("job_id", jobID), slog.String("user_id", userID), slog.Any("error", err))
		}
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)})
		return
	}
	renderJSON(w, http.StatusOK, job)
}

func (h *APIHandlers) DeleteJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := parseJobID(r)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	if err := h.Deps.App.Delete(r.Context(), jobID, userID); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("delete_job_failed", slog.String("job_id", jobID), slog.String("user_id", userID), slog.Any("error", err))
		}
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandlers) CancelJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := parseJobID(r)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	if err := h.Deps.App.Cancel(r.Context(), jobID, userID); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("cancel_job_failed", slog.String("job_id", jobID), slog.String("user_id", userID), slog.Any("error", err))
		}
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: http.StatusText(http.StatusNotFound)})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandlers) GetJobResults(w http.ResponseWriter, r *http.Request) {
	jobID, err := parseJobID(r)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}
	// Unified pagination — see web/handlers/pagination.go. Caps the
	// limit at MaxPageLimit (100, was 1000) and adds an overflow guard
	// on (page-1)*limit. Default 50 matches the prior behavior so the
	// frontend's default page size is unchanged.
	page, limit, offset, err := parsePagination(r, 50)
	if err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
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

	results, total, err := h.Deps.ResultsSvc.GetEnhancedJobResultsPaginated(r.Context(), jobID, userID, limit, offset)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to retrieve results",
			slog.String("user_id", userID), slog.String("job_id", jobID), slog.String("path", r.URL.Path), slog.String("method", r.Method))
		return
	}
	resp := models.PaginatedResultsResponse{Results: results, Total: total, Page: page, Limit: limit, HasMore: page*limit < total}
	renderJSON(w, http.StatusOK, resp)
}

// GetJobCosts returns the cost breakdown and totals for a job
func (h *APIHandlers) GetJobCosts(w http.ResponseWriter, r *http.Request) {
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

	jobID, err := parseJobID(r)
	if err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
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

	cs := webservices.NewCostsService(h.Deps.DB, h.Deps.Logger)
	resp, err := cs.GetJobCosts(r.Context(), jobID, userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to retrieve job costs",
			slog.String("user_id", userID), slog.String("job_id", jobID), slog.String("path", r.URL.Path), slog.String("method", r.Method))
		return
	}
	renderJSON(w, http.StatusOK, resp)
}

// GetBatchJobCosts returns cost breakdowns and totals for multiple jobs in a
// single request, eliminating N+1 individual cost fetches.
func (h *APIHandlers) GetBatchJobCosts(w http.ResponseWriter, r *http.Request) {
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

	var req models.BatchJobCostsRequest
	if err := decodeStrict(r, &req); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("json_decode_failed",
				slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		}
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid request body"})
		return
	}
	if len(req.JobIDs) == 0 {
		renderJSON(w, http.StatusOK, models.BatchJobCostsResponse{Costs: map[string]models.JobCostResponse{}})
		return
	}
	if len(req.JobIDs) > 100 {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "Maximum 100 job IDs per request"})
		return
	}

	// Validate all job IDs are valid UUIDs
	for _, id := range req.JobIDs {
		if _, err := uuid.Parse(id); err != nil {
			renderJSON(w, http.StatusUnprocessableEntity, models.APIError{
				Code:    http.StatusUnprocessableEntity,
				Message: "Invalid job ID format: " + id,
			})
			return
		}
	}

	// Verify ownership: fetch all user jobs and filter to only owned IDs.
	// This reuses the existing App.All which already filters by user_id.
	userJobs, err := h.Deps.App.All(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to verify job ownership",
			slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method))
		return
	}
	ownedSet := make(map[string]struct{}, len(userJobs))
	for _, j := range userJobs {
		ownedSet[j.ID] = struct{}{}
	}
	var validIDs []string
	for _, id := range req.JobIDs {
		if _, ok := ownedSet[id]; ok {
			validIDs = append(validIDs, id)
		}
	}

	if len(validIDs) == 0 {
		renderJSON(w, http.StatusOK, models.BatchJobCostsResponse{Costs: map[string]models.JobCostResponse{}})
		return
	}

	cs := webservices.NewCostsService(h.Deps.DB, h.Deps.Logger)
	costs, err := cs.GetBatchJobCosts(r.Context(), validIDs)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to retrieve batch job costs",
			slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method))
		return
	}

	renderJSON(w, http.StatusOK, models.BatchJobCostsResponse{Costs: costs})
}

func (h *APIHandlers) GetUserResults(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	// Unified pagination — see web/handlers/pagination.go. Caps the
	// limit at MaxPageLimit (100, was 1000) and rejects out-of-range
	// offset/limit values with 400 instead of silently coercing.
	limit, offset, err := parseOffsetPagination(r, 50)
	if err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}
	results, err := h.Deps.ResultsSvc.GetUserResults(r.Context(), userID, limit, offset)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to retrieve results",
			slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method))
		return
	}
	renderJSON(w, http.StatusOK, results)
}

// EstimateJobCost returns the estimated cost for a job without creating it.
func (h *APIHandlers) EstimateJobCost(w http.ResponseWriter, r *http.Request) {
	var req estimateRequest
	if err := decodeStrict(r, &req); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("json_decode_failed", slog.String("path", r.URL.Path), slog.Any("error", err))
		}
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid request body"})
		return
	}

	// Apply defaults for zero-valued optional fields.
	if req.Depth == 0 {
		req.Depth = webutils.DefaultDepth
	}

	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: formatValidationErrors(err)})
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

	// Estimate cost.
	estimationSvc := webservices.NewEstimationService(h.Deps.DB, h.Deps.PricingRuleRepo, h.Deps.Logger)
	estimate, err := estimationSvc.EstimateJobCost(
		r.Context(),
		req.Keywords,
		req.Depth,
		req.MaxResults,
		req.IncludeEmails,
		req.MaxReviews,
		req.MaxImages,
	)
	if err != nil {
		if errors.Is(err, webservices.ErrPricingUnavailable) {
			if h.Deps.Logger != nil {
				h.Deps.Logger.Error("pricing_unavailable",
					slog.String("user_id", userID), slog.Any("error", err))
			}
			w.Header().Set("Retry-After", "30")
			renderJSON(w, http.StatusServiceUnavailable, models.APIError{
				Code:    http.StatusServiceUnavailable,
				Message: "Billing service is temporarily unavailable. Please try again in a few seconds.",
			})
			return
		}
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("job_cost_estimation_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to estimate job cost"})
		return
	}

	// Read both gross balance AND held; report AVAILABLE on the wire so
	// the frontend's "Available X.XXXX" display and the `sufficient`
	// flag match the affordability gate the create endpoint enforces.
	// PR #66 added credit_held_precise but didn't update this endpoint;
	// without subtracting holds, a user with held>0 would see
	// sufficient=true on the preview AND get a 402 from the create
	// endpoint. Now symmetric with CheckSufficientBalance + the
	// transactional gate in ConcurrentLimitService.
	var balanceStr, heldStr string
	const query = `SELECT COALESCE(credit_balance, 0)::text, COALESCE(credit_held_precise, 0)::text FROM users WHERE id = $1`
	if err := h.Deps.DB.QueryRowContext(r.Context(), query, userID).Scan(&balanceStr, &heldStr); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("credit_balance_fetch_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to retrieve credit balance"})
		return
	}
	balanceDec, err := decimal.NewFromString(balanceStr)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("credit_balance_parse_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to parse credit balance"})
		return
	}
	heldDec, err := decimal.NewFromString(heldStr)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("credit_held_parse_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "failed to parse credit balance"})
		return
	}
	availableDec := balanceDec.Sub(heldDec)
	availableFloat, _ := availableDec.Float64()
	availableMicro := availableDec.Mul(decimal.NewFromInt(models.MicroUnit)).IntPart()

	// estimateBalance.Current is intentionally the AVAILABLE figure
	// (gross minus held) — matching the meaning the FE should render
	// as "Available X.XXXX" on the create-job page. The Sufficient
	// flag uses the same comparison the transactional gate uses.
	response := estimateResponse{
		Estimate: estimate,
		Balance: estimateBalance{
			Current:    availableFloat,
			Sufficient: availableMicro >= estimate.TotalMicro(),
		},
	}

	w.Header().Set("Cache-Control", "no-store")
	renderJSON(w, http.StatusOK, response)
}

// use renderJSON from handlers package (defined in web.go)
