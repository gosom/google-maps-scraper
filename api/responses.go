package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GmapData represents Google Maps scrape results data.
type GmapData any

// ErrorResponse represents an error response
// @Description Error response returned when a request fails
type ErrorResponse struct {
	Message string `json:"message" example:"invalid request body"`
}

// HealthCheckResponse represents a health check response
// @Description Health check response indicating service status
type HealthCheckResponse struct {
	Status string `json:"status" example:"ok"`
}

// ScrapeRequest represents a request to scrape Google Maps
// @Description Request body for submitting a scrape job
type ScrapeRequest struct {
	// Search keyword (e.g., "restaurants in New York")
	Keyword string `json:"keyword" example:"restaurants in New York"`
	// Language code for results (default: "en")
	Lang string `json:"lang,omitempty" example:"en"`
	// Maximum depth for pagination (default: 1, max: 100)
	MaxDepth int `json:"max_depth,omitempty" example:"1"`
	// Whether to extract email addresses from websites
	Email bool `json:"email,omitempty" example:"false"`
	// Geographic coordinates in "lat,lon" format
	GeoCoordinates string `json:"geo_coordinates,omitempty" example:"40.7128,-74.0060"`
	// Zoom level for map search (1-21)
	Zoom int `json:"zoom,omitempty" example:"14"`
	// Search radius in kilometers
	Radius float64 `json:"radius,omitempty" example:"5.0"`
	// Use fast mode (stealth HTTP instead of browser)
	FastMode bool `json:"fast_mode,omitempty" example:"false"`
	// Extract additional reviews
	ExtraReviews bool `json:"extra_reviews,omitempty" example:"false"`
	// Job timeout in seconds (1-300, default: 300)
	Timeout int `json:"timeout,omitempty" example:"300"`
}

func (r *ScrapeRequest) Validate() error {
	if r.Keyword == "" {
		return fmt.Errorf("keyword is required")
	}

	if r.GeoCoordinates != "" {
		if strings.Count(r.GeoCoordinates, ",") != 1 {
			return fmt.Errorf("geo_coordinates must contain exactly one comma")
		}

		latStr, lonStr, ok := strings.Cut(r.GeoCoordinates, ",")
		if !ok {
			return fmt.Errorf("geo_coordinates must be in format 'lat,lon'")
		}

		lat, err := strconv.ParseFloat(strings.TrimSpace(latStr), 64)
		if err != nil {
			return fmt.Errorf("invalid latitude: %w", err)
		}

		lon, err := strconv.ParseFloat(strings.TrimSpace(lonStr), 64)
		if err != nil {
			return fmt.Errorf("invalid longitude: %w", err)
		}

		if lat < -90 || lat > 90 {
			return fmt.Errorf("latitude must be between -90 and 90")
		}

		if lon < -180 || lon > 180 {
			return fmt.Errorf("longitude must be between -180 and 180")
		}
	}

	if r.FastMode {
		if r.GeoCoordinates == "" {
			return fmt.Errorf("geo_coordinates are required in fast mode")
		}

		if r.Zoom == 0 {
			return fmt.Errorf("zoom is required in fast mode")
		}
	}

	if r.Zoom != 0 && (r.Zoom < 1 || r.Zoom > 21) {
		return fmt.Errorf("zoom must be between 1 and 21")
	}

	if r.Radius < 0 {
		return fmt.Errorf("radius must be non-negative")
	}

	if r.MaxDepth < 0 || r.MaxDepth > 100 {
		return fmt.Errorf("max_depth must be between 0 and 100")
	}

	if r.Timeout < 1 || r.Timeout > 300 {
		return fmt.Errorf("timeout must be between 1 and 300 seconds")
	}

	return nil
}

func (r *ScrapeRequest) SetDefaults() {
	if r.Lang == "" {
		r.Lang = "en"
	}

	if r.MaxDepth == 0 {
		r.MaxDepth = 1
	}

	if r.Timeout == 0 {
		r.Timeout = 300 // 5 minutes default
	}
}

// ScrapeResponse represents the response after submitting a scrape job
// @Description Response returned after successfully submitting a scrape job
type ScrapeResponse struct {
	// Unique job identifier
	JobID string `json:"job_id" example:"kYzR8xLmNpQvWjX3"`
	// Current job status
	Status string `json:"status" example:"pending"`
}

// ListJobsRequest represents parameters for listing jobs
// @Description Query parameters for listing jobs with pagination and filtering
type ListJobsRequest struct {
	// Filter by job state (available, running, completed, cancelled, discarded, pending, retryable, scheduled)
	State string `json:"state,omitempty" example:"completed"`
	// Number of jobs to return (default: 20, max: 100)
	Limit int `json:"limit,omitempty" example:"20"`
	// Cursor for pagination (from previous response)
	Cursor string `json:"cursor,omitempty" example:""`
}

// JobSummary represents a job without result data
// @Description Summary of a job without the full result data
type JobSummary struct {
	// Unique job identifier (encoded)
	JobID string `json:"job_id" example:"kYzR8xLmNpQvWjX3"`
	// Current job status
	Status string `json:"status" example:"completed"`
	// Search keyword used for this job
	Keyword string `json:"keyword" example:"restaurants in New York"`
	// When the job was created
	CreatedAt time.Time `json:"created_at" example:"2024-01-15T10:30:00Z"`
	// When the job started processing
	StartedAt *time.Time `json:"started_at,omitempty" example:"2024-01-15T10:30:05Z"`
	// When the job completed
	CompletedAt *time.Time `json:"completed_at,omitempty" example:"2024-01-15T10:35:00Z"`
	// Number of results found
	ResultCount int `json:"result_count" example:"25"`
	// Error message if job failed
	Error string `json:"error,omitempty" example:""`
}

// ListJobsResponse represents a paginated list of jobs
// @Description Paginated list of jobs with cursor for next page
type ListJobsResponse struct {
	// List of jobs
	Jobs []JobSummary `json:"jobs"`
	// Cursor for next page (empty if no more results)
	NextCursor string `json:"next_cursor,omitempty" example:"eyJpZCI6MTIzfQ=="`
	// Whether there are more results
	HasMore bool `json:"has_more" example:"true"`
}

// JobStatusResponse represents the status of a scrape job
// @Description Detailed status of a scrape job including results when completed
type JobStatusResponse struct {
	// Unique job identifier
	JobID string `json:"job_id" example:"kYzR8xLmNpQvWjX3"`
	// Current job status (pending, running, completed, failed)
	Status string `json:"status" example:"completed"`
	// Search keyword used for this job
	Keyword string `json:"keyword" example:"restaurants in New York"`
	// When the job was created
	CreatedAt time.Time `json:"created_at" example:"2024-01-15T10:30:00Z"`
	// When the job started processing
	StartedAt *time.Time `json:"started_at,omitempty" example:"2024-01-15T10:30:05Z"`
	// When the job completed
	CompletedAt *time.Time `json:"completed_at,omitempty" example:"2024-01-15T10:35:00Z"`
	// Scrape results (array of place data)
	Results GmapData `json:"results,omitempty"`
	// Error message if job failed
	Error string `json:"error,omitempty" example:""`
	// Number of results found
	ResultCount int `json:"result_count" example:"25"`
}

// DeleteJobResponse represents the response after requesting job deletion
// @Description Response returned after successfully queueing a job for deletion
type DeleteJobResponse struct {
	// Status message
	Message string `json:"message" example:"deletion queued"`
}
