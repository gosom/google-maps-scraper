package models

import "time"

// Event type constants for webhook deliveries.
const (
	EventTypeJobCompleted = "job.completed"
	EventTypeJobFailed    = "job.failed"
	EventTypeJobCancelled = "job.cancelled"
)

// WebhookEvent is the JSON payload sent to webhook endpoints
// when a job reaches a terminal state.
type WebhookEvent struct {
	EventType   string    `json:"event_type"`
	JobID       string    `json:"job_id"`
	JobName     string    `json:"job_name"`
	Status      string    `json:"status"`
	ResultCount int       `json:"result_count"`
	CreatedAt   time.Time `json:"created_at"`
	EndedAt     time.Time `json:"ended_at"`
}
