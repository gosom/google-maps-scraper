package models

import (
	"context"
	"time"
)

// JobData contains the configurable options for a job
type JobData struct {
	Keywords   []string      `json:"keywords"`
	Lang       string        `json:"lang"`
	Depth      int           `json:"depth"`
	Email      bool          `json:"email"`
	Images     bool          `json:"images"`
	ReviewsMax int           `json:"reviews_max"` // Maximum number of reviews to scrape per location
	MaxResults int           `json:"max_results"` // Maximum number of locations to scrape (0 = unlimited)
	Lat        string        `json:"lat"`
	Lon        string        `json:"lon"`
	Zoom       int           `json:"zoom"`
	Radius     int           `json:"radius"`
	MaxTime    time.Duration `json:"max_time"`
	FastMode   bool          `json:"fast_mode"`
	Proxies    []string      `json:"proxies"`
}

// Job represents a scraping job
type Job struct {
	ID        string
	UserID    string
	Name      string
	Status    string
	Data      JobData
	Date      time.Time
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// SelectParams defines parameters for filtering job selection
type SelectParams struct {
	Status string
	Limit  int
	UserID string
}

// JobRepository defines the interface for job storage
type JobRepository interface {
	Get(ctx context.Context, id string) (Job, error)
	Create(ctx context.Context, job *Job) error
	Delete(ctx context.Context, id string) error
	Select(ctx context.Context, params SelectParams) ([]Job, error)
	Update(ctx context.Context, job *Job) error
	Cancel(ctx context.Context, id string) error
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
