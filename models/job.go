package models

import (
	"context"
	"time"
)

// JobData contains the configurable options for a job.
//
// All integer caps mirror the constants in web/utils/cap_params.go (struct
// tags can't reference Go consts, so the literal values must be kept in sync
// — the web/utils.ValidateJobData service-layer check uses the named consts).
// There is NO "unlimited" sentinel: every integer field has a strict min and
// max, and clients paginate or run multiple jobs.
type JobData struct {
	Keywords []string `json:"keywords" validate:"required,min=1,max=5,dive,min=1,max=200"`
	Lang     string   `json:"lang"     validate:"required,len=2"`
	Depth    int      `json:"depth"    validate:"required,min=1,max=20"`
	Email    bool     `json:"email"`
	// ImagesMax is the TOTAL number of images across all places in the job
	// — NOT per place. The literal 20000 here mirrors utils.CapImagesMaxTotal.
	// 0 means "skip image scraping" (the billing-safe default). Any positive
	// value enables image scraping with a per-job total budget enforced by
	// the runner via a shared atomic counter (cross-place). The legacy
	// `images` boolean was dropped in migration 000033.
	ImagesMax  int           `json:"images_max"  validate:"omitempty,min=0,max=20000"`
	ReviewsMax int           `json:"reviews_max" validate:"omitempty,min=0,max=500"`
	MaxResults int           `json:"max_results" validate:"required,min=1,max=500"`
	Lat        string        `json:"lat"         validate:"omitempty,latitude"`
	Lon        string        `json:"lon"         validate:"omitempty,longitude"`
	Zoom       int           `json:"zoom"        validate:"omitempty,min=0,max=21"`
	Radius     int           `json:"radius"      validate:"omitempty,min=0,max=50000"`
	MaxTime    time.Duration `json:"max_time"    validate:"required"`
	FastMode   bool          `json:"fast_mode"`
	Proxies    []string      `json:"proxies"     validate:"omitempty,max=100,dive,max=2048"`
}

// Job represents a scraping job
type Job struct {
	ID            string
	UserID        string
	Name          string
	Status        string
	Data          JobData
	Date          time.Time
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
	HasNext bool  `json:"has_next"`
	HasPrev bool  `json:"has_prev"`
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
	StatusWorking   = "working"
	StatusOK        = "ok"
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
