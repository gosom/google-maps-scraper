package models

import (
	"context"
	"errors"
	"net"
	"time"
)

// Sentinel errors for webhook operations.
var (
	ErrWebhookConfigNotFound = errors.New("webhook config not found")
	ErrDeliveryNotFound      = errors.New("webhook delivery not found")
)

// WebhookConfig represents a user-level webhook endpoint configuration.
type WebhookConfig struct {
	ID              string `json:"id"`
	UserID          string `json:"user_id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	EncryptedSecret string `json:"encrypted_secret"` // AES-GCM encrypted signing secret
	// SECURITY: delivery must connect to resolved_ip, not re-resolve DNS (TOCTOU/DNS rebinding prevention)
	ResolvedIP *net.IP    `json:"-"` // Internal; never exposed in API responses
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"` // Soft delete
}

// IsActive returns true if the webhook config has not been revoked.
func (w *WebhookConfig) IsActive() bool {
	return w.RevokedAt == nil
}

// JobWebhookDelivery tracks the delivery state of a webhook for a specific job.
type JobWebhookDelivery struct {
	JobID           string     `json:"job_id"`
	WebhookConfigID string     `json:"webhook_config_id"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"max_attempts"`
	LastAttemptAt   *time.Time `json:"last_attempt_at,omitempty"`
	NextRetryAt     *time.Time `json:"next_retry_at,omitempty"`
	DeliveredAt     *time.Time `json:"delivered_at,omitempty"`
	Status          string     `json:"status"` // pending | delivering | delivered | failed
}

// Delivery status constants.
const (
	DeliveryStatusPending    = "pending"
	DeliveryStatusDelivering = "delivering"
	DeliveryStatusDelivered  = "delivered"
	DeliveryStatusFailed     = "failed"
)

// WebhookConfigRepository manages webhook configuration CRUD operations.
type WebhookConfigRepository interface {
	// Create inserts a new webhook config.
	Create(ctx context.Context, cfg *WebhookConfig) error

	// GetByID retrieves a webhook config by its UUID.
	GetByID(ctx context.Context, id string) (*WebhookConfig, error)

	// ListByUserID retrieves all webhook configs for a user (including revoked).
	ListByUserID(ctx context.Context, userID string) ([]*WebhookConfig, error)

	// ListActiveByUserID retrieves only active (non-revoked) configs for a user.
	ListActiveByUserID(ctx context.Context, userID string) ([]*WebhookConfig, error)

	// Update modifies a webhook config's mutable fields (name, url).
	Update(ctx context.Context, cfg *WebhookConfig) error

	// Revoke soft-deletes a webhook config owned by the given user.
	Revoke(ctx context.Context, id string, ownerUserID string) error

	// ListActiveWithSecretByUserID retrieves active configs including the encrypted secret.
	ListActiveWithSecretByUserID(ctx context.Context, userID string) ([]*WebhookConfig, error)
}

// JobWebhookDeliveryRepository manages webhook delivery tracking.
type JobWebhookDeliveryRepository interface {
	// Create inserts a new delivery record (typically at job creation).
	Create(ctx context.Context, delivery *JobWebhookDelivery) error

	// CreateBatch inserts multiple delivery records in a single query.
	// Uses ON CONFLICT DO NOTHING for idempotency.
	CreateBatch(ctx context.Context, deliveries []*JobWebhookDelivery) error

	// ListByJobID retrieves all deliveries for a job.
	ListByJobID(ctx context.Context, jobID string) ([]*JobWebhookDelivery, error)

	// ListPendingByJobID retrieves undelivered entries for a job.
	ListPendingByJobID(ctx context.Context, jobID string) ([]*JobWebhookDelivery, error)

	// ListPendingGlobal atomically claims up to limit pending deliveries
	// by setting their status to delivering within a transaction.
	ListPendingGlobal(ctx context.Context, limit int) ([]*JobWebhookDelivery, error)

	// MarkDelivering sets status to delivering and increments attempt count.
	MarkDelivering(ctx context.Context, jobID, webhookConfigID string) error

	// MarkDelivered sets delivered_at and status to delivered.
	MarkDelivered(ctx context.Context, jobID, webhookConfigID string) error

	// MarkFailed sets status to failed after exhausting retries.
	MarkFailed(ctx context.Context, jobID, webhookConfigID string) error

	// SetNextRetry resets a delivery to pending with a scheduled retry time.
	SetNextRetry(ctx context.Context, jobID, webhookConfigID string, nextRetryAt time.Time) error
}
