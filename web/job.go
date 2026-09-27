package web

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AxioIntel/Axio-CRED/gmaps"
)

var jobs []Job

const (
	StatusPending = "pending"
	StatusWorking = "working"
	StatusOK      = "ok"
	StatusFailed  = "failed"
	// StatusPartial is a job that ran out of its time ("Stop after", or v2's bound) before every
	// search was done. What it found is kept, as for ok; it is not the same as finishing.
	StatusPartial = "partial"
)

// Finished is whether a job's run is over, however it ended.
func Finished(status string) bool {
	return status == StatusOK || status == StatusPartial || status == StatusFailed
}

type SelectParams struct {
	Status string
	// ExcludeStatus, when set, is a status to leave out instead of one to require. Status and
	// ExcludeStatus are never both set.
	ExcludeStatus string
	Limit         int
	// Oldest orders by creation time ascending: a queue runs in the order jobs were added.
	Oldest bool
}

type JobRepository interface {
	Get(context.Context, string) (Job, error)
	Create(context.Context, *Job) error
	Delete(context.Context, string) error
	Select(context.Context, SelectParams) ([]Job, error)
	// Count is how many jobs match params, without decoding any of them: a campaign can queue
	// tens of thousands of pending jobs, and the dashboard polls every 5s.
	Count(context.Context, SelectParams) (int, error)
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
	Keywords     []string      `json:"keywords"`
	Lang         string        `json:"lang"`
	Zoom         int           `json:"zoom"`
	Lat          string        `json:"lat"`
	Lon          string        `json:"lon"`
	FastMode     bool          `json:"fast_mode"`
	Radius       int           `json:"radius"`
	Depth        int           `json:"depth"`
	Email        bool          `json:"email"`
	ExtraReviews bool          `json:"extra_reviews"`
	MaxTime      time.Duration `json:"max_time"`
	Proxies      []string      `json:"proxies"`
	// Grid, when set, runs every search once per square of one area (see grid.go).
	Grid *GridSpec `json:"grid,omitempty"`
	// Collector is which collector runs the job: "v1" (the browser, the default; also what an
	// empty value means, so every job from before this field is v1) or "v2" (steady HTTP).
	Collector string `json:"collector,omitempty"`
	// Steady is v2's settings; nil is v2's defaults. Ignored for v1.
	Steady *SteadySpec `json:"steady,omitempty"`
	// Tag marks a job the Verify page made: "smoke" or "cmp:<comparison id>".
	Tag string `json:"tag,omitempty"`
}

// IsV2 is whether the job runs on collector v2.
//
//nolint:gocritic // a value receiver so templates can call it on a job passed by value
func (d JobData) IsV2() bool { return d.Collector == gmaps.CollectorV2 }

// CollectorName is "v1" or "v2".
//
//nolint:gocritic // a value receiver so templates can call it on a job passed by value
func (d JobData) CollectorName() string {
	if d.IsV2() {
		return gmaps.CollectorV2
	}

	return gmaps.CollectorV1
}

// SteadySpec is v2's settings as the API and the form carry them: plain numbers.
type SteadySpec struct {
	Pages          int  `json:"pages,omitempty"`
	DelayMs        int  `json:"delay_ms,omitempty"`
	ContactPages   int  `json:"contact_pages"`
	NoRedirects    bool `json:"no_redirects,omitempty"`
	SiteTimeoutSec int  `json:"site_timeout_sec,omitempty"`
	QueryBudgetSec int  `json:"query_budget_sec,omitempty"`
	// Retries is how many more times a refused Google request is tried; nil is 1.
	Retries *int `json:"retries,omitempty"`
}

// SteadyConfig is the job's v2 settings, defaults filled in.
func (d *JobData) SteadyConfig() gmaps.SteadyConfig {
	cfg := gmaps.SteadyConfig{Lang: d.Lang, Retries: gmaps.DefaultSteadyRetries}

	if s := d.Steady; s != nil {
		cfg.Pages = s.Pages
		cfg.Delay = time.Duration(s.DelayMs) * time.Millisecond
		cfg.ContactPages = s.ContactPages
		cfg.NoRedirects = s.NoRedirects
		cfg.SiteTimeout = time.Duration(s.SiteTimeoutSec) * time.Second
		cfg.QueryBudget = time.Duration(s.QueryBudgetSec) * time.Second

		if s.Retries != nil {
			cfg.Retries = *s.Retries
		}
	}

	return cfg.WithDefaults()
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

	if d.FastMode && (d.Lat == "" || d.Lon == "") {
		return errors.New("missing geo coordinates")
	}

	return d.validateCollector()
}

// validateCollector checks the collector choice and v2's settings.
func (d *JobData) validateCollector() error {
	switch d.Collector {
	case "", gmaps.CollectorV1:
		return nil
	case gmaps.CollectorV2:
	default:
		return fmt.Errorf("unknown collector %q: use v1 or v2", d.Collector)
	}

	if d.Grid != nil {
		return errors.New("the map grid runs on v1 only")
	}

	if d.FastMode {
		return errors.New("fast mode is a v1 setting; v2 is already browserless")
	}

	return d.SteadyConfig().Validate()
}
