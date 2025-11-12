package web

import (
	"errors"

	"github.com/gosom/google-maps-scraper/models"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
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
	return webutils.ValidateJob((*models.Job)(j))
}

// ValidateJobData validates job data
func ValidateJobData(d *JobData) error {
	return webutils.ValidateJobData((*models.JobData)(d))
}
