package entities

import (
	"context"
	"errors"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

var (
	ErrOtherJobRunning = errors.New("other job is running")
	ErrJobNotFound     = errors.New("job not found")
)

const (
	JobStatusCreated = "created"
	JobStatusRunning = "running"
	JobStatusDone    = "done"
	JobStatusFailed  = "failed"
	JobStatusStopped = "stopped"
)

type Job struct {
	ID          string
	LangCode    string
	MaxDepth    int
	Debug       bool
	Queries     []string
	CreatedAt   time.Time
	FinishedAt  *time.Time
	Status      string
	Concurrency int
}

type JobResult struct {
	ID    int
	JobID string
	Data  gmaps.Entry
}

type Worker interface {
	Start(ctx context.Context) error
	ScheduleJob(ctx context.Context, job Job) error
}

type JobStore interface {
	CreateJob(ctx context.Context, job *Job) error
	SetJobStatus(ctx context.Context, id string, status string) error
	SelectAllJobs(ctx context.Context) ([]Job, error)
	GetJobResult(ctx context.Context, id string) ([]JobResult, error)
	GetResultCount(ctx context.Context) (map[string]int, error)
}

type Store interface {
	scrapemate.ResultWriter
	JobStore
}
