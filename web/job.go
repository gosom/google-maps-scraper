package web

import (
	"context"
	"errors"
	"time"
)

var jobs []Job

const (
	StatusPending = "pending"
	StatusWorking = "working"
	StatusOK      = "ok"
	StatusFailed  = "failed"
)

type SelectParams struct {
	Status string
	Limit  int
}

type JobRepository interface {
	Get(context.Context, string) (Job, error)
	Create(context.Context, *Job) error
	Delete(context.Context, string) error
	Select(context.Context, SelectParams) ([]Job, error)
	Update(context.Context, *Job) error
}

type Job struct {
	ID     string
	Name   string
	Date   time.Time
	Status string
	Data   JobData
}

func (j *Job) Validate() error {
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

	if err := j.Data.Validate(); err != nil {
		return err
	}

	return nil
}

type JobData struct {
	Keywords      []string      `json:"keywords"`
	Lang          string        `json:"lang"`
	Zoom          int           `json:"zoom"`
	Lat           string        `json:"lat"`
	Lon           string        `json:"lon"`
	Depth         int           `json:"depth"`
	Email         bool          `json:"email"`
	MaxTime       time.Duration `json:"max_time"`
	Proxies       []string      `json:"proxies"`
	ProxyUsername string        `json:"proxy_username"`
	ProxyPassword string        `json:"proxy_password"`
}

func (d *JobData) Validate() error {
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

	return nil
}
