package web

import (
	"errors"

	"github.com/gosom/google-maps-scraper/models"
)

var jobs []models.Job

// Use the status constants from models package
const (
	StatusPending   = models.StatusPending
	StatusWorking   = models.StatusWorking
	StatusOK        = models.StatusOK
	StatusFailed    = models.StatusFailed
	StatusCancelled = models.StatusCancelled
	StatusAborting  = models.StatusAborting
)

// JobRepository is now an alias to the models.JobRepository interface
type JobRepository = models.JobRepository

// Job is now an alias to the models.Job struct
type Job = models.Job

// JobData is now an alias to the models.JobData struct
type JobData = models.JobData

// SelectParams is now an alias to the models.SelectParams struct
type SelectParams = models.SelectParams

// ValidateJob validates a job
func ValidateJob(j *Job) error {
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

// ValidateJobData validates job data
func ValidateJobData(d *JobData) error {
	if len(d.Keywords) == 0 {
		return errors.New("missing keywords")
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

	if d.MaxTime == 0 {
		return errors.New("missing max time")
	}

	if d.FastMode && (d.Lat == "" || d.Lon == "") {
		return errors.New("missing geo coordinates")
	}

	if d.MaxResults < 0 {
		return errors.New("max results cannot be negative")
	}

	return nil
}
