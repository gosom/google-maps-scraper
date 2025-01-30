package tasks

import (
	"time"
)

// Task types
const (
	TypeScrapeGMaps    = "scrape:gmaps"
	TypeEmailExtract   = "extract:email"
	TypeHealthCheck    = "health:check"
	TypeConnectionTest = "connection:test"
)

// TaskPriority defines priority levels for tasks
const (
	PriorityLow      = "low"
	PriorityDefault  = "default"
	PriorityCritical = "critical"
)

// ScrapePayload represents the payload for a scrape task
type ScrapePayload struct {
	JobID    string        `json:"job_id"`
	Keywords []string      `json:"keywords"`
	FastMode bool          `json:"fast_mode"`
	Lang     string        `json:"lang"`
	Depth    int           `json:"depth"`
	Email    bool          `json:"email"`
	Lat      string        `json:"lat,omitempty"`
	Lon      string        `json:"lon,omitempty"`
	Zoom     int           `json:"zoom,omitempty"`
	Radius   int           `json:"radius,omitempty"`
	MaxTime  time.Duration `json:"max_time,omitempty"`
	Proxies  []string      `json:"proxies,omitempty"`
}

// EmailPayload represents the payload for an email extraction task
type EmailPayload struct {
	URL       string `json:"url"`
	MaxDepth  int    `json:"max_depth,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}
