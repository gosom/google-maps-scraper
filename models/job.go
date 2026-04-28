package models

import (
	"context"
	"encoding/json"
	"time"
)

// DurationSec wraps time.Duration but serializes as seconds in JSON.
// Users send and receive seconds; the internal representation is nanoseconds.
type DurationSec time.Duration

func (d DurationSec) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(time.Duration(d) / time.Second))
}

func (d *DurationSec) UnmarshalJSON(data []byte) error {
	var sec int64
	if err := json.Unmarshal(data, &sec); err != nil {
		return err
	}
	*d = DurationSec(time.Duration(sec) * time.Second)
	return nil
}

// Duration returns the underlying time.Duration.
func (d DurationSec) Duration() time.Duration {
	return time.Duration(d)
}

// JobData contains the configurable options for a job.
//
// REST best-practice posture (locked in 2026-04-09 — see Chunk 2 of the
// API audit plan): only the genuinely-essential fields (Keywords, Lang)
// are required. Every other field is optional with a documented default
// applied by web/utils.ApplyJobDataDefaults at the API entry point.
//
// All integer caps mirror the constants in web/utils/cap_params.go. Struct
// tags can't reference Go consts, so the literal values must be kept in
// sync — the web/utils.ValidateJobData service-layer check uses the named
// consts as the source of truth. There is NO "unlimited" sentinel: every
// integer field has a strict min and max, and clients paginate or run
// multiple jobs.
type JobData struct {
	Keywords []string `json:"keywords" validate:"required,min=1,max=5,dive,min=1,max=200"`
	Language string   `json:"language" validate:"required,len=2"`
	// Depth, MaxResults, and MaxTime are optional — ApplyJobDataDefaults
	// fills in safe defaults (5, 50, and 30 minutes respectively) when
	// the client omits them.
	Depth         int  `json:"depth" validate:"min=1,max=20"`
	IncludeEmails bool `json:"include_emails"`
	// MaxImages is the TOTAL number of images across all places in the job
	// — NOT per place. The literal 40000 here mirrors utils.CapImagesMaxTotal.
	// 0 means "skip image scraping" (the billing-safe default). Any positive
	// value enables image scraping with a per-job total budget enforced by
	// the runner via a shared atomic counter (cross-place). The legacy
	// `images` boolean was dropped in migration 000033.
	MaxImages  int    `json:"max_images"  validate:"omitempty,min=0,max=40000"`
	MaxReviews int    `json:"max_reviews" validate:"omitempty,min=0,max=500"`
	MaxResults int    `json:"max_results" validate:"omitempty,min=0,max=500"`
	Lat        string `json:"lat"         validate:"omitempty,latitude"`
	Lon        string `json:"lon"         validate:"omitempty,longitude"`
	Zoom       int    `json:"zoom"        validate:"omitempty,min=0,max=21"`
	Radius     int    `json:"radius"      validate:"omitempty,min=0,max=50000"`
	// MaxTime range is enforced at the service layer in ValidateJobData
	// (validator/v10 doesn't have a clean duration min/max). Default is
	// 30 minutes; ceiling is 1 hour — see cap_params.go for the
	// headless-browser reasoning.
	MaxTime  DurationSec `json:"max_time"`
	FastMode bool        `json:"fast_mode"`
	Proxies  []string    `json:"proxies" validate:"omitempty,max=100,dive,max=2048"`
}

// Job represents a scraping job
type Job struct {
	ID            string     `json:"id"`
	UserID        string     `json:"-"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	Data          JobData    `json:"data"`
	Date          time.Time  `json:"-"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	Source        string     `json:"source"`
	ResultCount   int        `json:"result_count"`
	TotalCost     string     `json:"total_cost"`
}

// SelectParams defines parameters for filtering job selection
type SelectParams struct {
	Status string
	Limit  int
	UserID string
}

// PaginatedJobsParams defines parameters for paginated job queries.
type PaginatedJobsParams struct {
	UserID string
	Page   int    // 1-based page number
	Limit  int    // items per page (max 100)
	Sort   string // column to sort by: "created_at", "name", "status", "updated_at"
	Order  string // "asc" or "desc"
	Search string // optional case-insensitive search on name or status
}

// PaginatedJobsResponse wraps a page of jobs with pagination metadata.
type PaginatedJobsResponse struct {
	Jobs    []Job `json:"jobs"`
	Total   int   `json:"total"`
	Page    int   `json:"page"`
	Limit   int   `json:"limit"`
	HasMore bool  `json:"has_more"`
}

// JobRepository defines the interface for job storage
type JobRepository interface {
	Get(ctx context.Context, id string, userID string) (Job, error)
	Create(ctx context.Context, job *Job) error
	Delete(ctx context.Context, id string, userID string) error
	Select(ctx context.Context, params SelectParams) ([]Job, error)
	SelectPaginated(ctx context.Context, params PaginatedJobsParams) ([]Job, int, error)
	Update(ctx context.Context, job *Job) error
	Cancel(ctx context.Context, id string, userID string) error
}

// Common status constants
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusAborting  = "aborting"
)

// Job source constants
const (
	SourceWeb   = "web"
	SourceAPI   = "api"
	SourceAdmin = "admin"
)
