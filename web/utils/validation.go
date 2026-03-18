package utils

import (
	"errors"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

const (
	maxDepth      = 20
	maxResults    = 1000
	maxKeywords   = 5
	maxMaxTime    = 4 * time.Hour
	maxReviewsMax = 9999
)

// ValidateJob validates a job payload.
func ValidateJob(j *models.Job) error {
	if j.ID == "" {
		return errors.New("missing id")
	}
	if j.Name == "" {
		return errors.New("missing name")
	}
	if j.Status == "" {
		return errors.New("missing status")
	}
	if j.Date.IsZero() {
		return errors.New("missing date")
	}
	if err := ValidateJobData(&j.Data); err != nil {
		return err
	}
	return nil
}

// ValidateJobData validates job data. This function enforces resource
// consumption limits (CWE-400) in addition to the struct-tag validation
// performed at the HTTP layer, so that non-HTTP callers (CLI, workers) are
// also protected.
func ValidateJobData(d *models.JobData) error {
	if len(d.Keywords) == 0 {
		return errors.New("missing keywords")
	}
	if len(d.Keywords) > maxKeywords {
		return errors.New("too many keywords: maximum is 5")
	}
	if d.Lang == "" {
		return errors.New("missing lang")
	}
	if len(d.Lang) != 2 {
		return errors.New("invalid lang")
	}
	if d.Depth == 0 {
		return errors.New("missing depth")
	}
	if d.Depth > maxDepth {
		return errors.New("depth exceeds maximum of 20")
	}
	if d.MaxTime == 0 {
		return errors.New("missing max time")
	}
	if d.MaxTime > maxMaxTime {
		return errors.New("max_time exceeds maximum of 4 hours")
	}
	if d.FastMode && (d.Lat == "" || d.Lon == "") {
		return errors.New("missing geo coordinates")
	}
	if d.MaxResults < 0 {
		return errors.New("max results cannot be negative")
	}
	if d.MaxResults > maxResults {
		return errors.New("max_results exceeds maximum of 1000")
	}
	if d.ReviewsMax < 0 {
		return errors.New("reviews_max cannot be negative")
	}
	if d.ReviewsMax > maxReviewsMax {
		return errors.New("reviews_max exceeds maximum of 9999")
	}
	return nil
}
