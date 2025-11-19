package models

import (
	"context"
	"time"
)

// JobData contains the configurable options for a job
type JobData struct {
	Keywords   []string      `json:"keywords" validate:"required,min=1,max=5"`
	Lang       string        `json:"lang" validate:"required,len=2"`
	Depth      int           `json:"depth" validate:"min=1,max=20"`
	Email      bool          `json:"email"`
	Images     bool          `json:"images"`
	ReviewsMax int           `json:"reviews_max" validate:"min=0,max=9999"` // Max Reviews: 0-9999 (0 = don't scrape reviews)
	MaxResults int           `json:"max_results" validate:"min=0,max=1000"` // Max results: 0-1000 (0 = unlimited)
	Lat        string        `json:"lat" validate:"omitempty,latitude"`
	Lon        string        `json:"lon" validate:"omitempty,longitude"`
	Zoom       int           `json:"zoom" validate:"omitempty,min=0,max=21"`
	Radius     int           `json:"radius" validate:"omitempty,min=0"`
	MaxTime    time.Duration `json:"max_time"`
	FastMode   bool          `json:"fast_mode"`
	Proxies    []string      `json:"proxies"`
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
