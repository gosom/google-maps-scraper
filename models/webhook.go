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
	ID         string
	UserID     string
	Name       string
	URL        string
	SecretHash string  // HMAC-SHA256 hash; plaintext never stored
	ResolvedIP *net.IP // DNS-pinned IP at validation time
	VerifiedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
	RevokedAt  *time.Time // Soft delete
}

// IsActive returns true if the webhook config has not been revoked.
func (w *WebhookConfig) IsActive() bool {
	return w.RevokedAt == nil
}

// JobWebhookDelivery tracks the delivery state of a webhook for a specific job.
type JobWebhookDelivery struct {
	JobID           string
	WebhookConfigID string
	Attempts        int
	LastAttemptAt   *time.Time
	DeliveredAt     *time.Time
	Status          string // pending | delivering | delivered | failed
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
}

// JobWebhookDeliveryRepository manages webhook delivery tracking.
type JobWebhookDeliveryRepository interface {
	// Create inserts a new delivery record (typically at job creation).
	Create(ctx context.Context, delivery *JobWebhookDelivery) error

	// ListByJobID retrieves all deliveries for a job.
	ListByJobID(ctx context.Context, jobID string) ([]*JobWebhookDelivery, error)

	// ListPendingByJobID retrieves undelivered entries for a job.
	ListPendingByJobID(ctx context.Context, jobID string) ([]*JobWebhookDelivery, error)

	// MarkDelivering sets status to delivering and increments attempt count.
	MarkDelivering(ctx context.Context, jobID, webhookConfigID string) error

	// MarkDelivered sets delivered_at and status to delivered.
	MarkDelivered(ctx context.Context, jobID, webhookConfigID string) error

	// MarkFailed sets status to failed after exhausting retries.
	MarkFailed(ctx context.Context, jobID, webhookConfigID string) error
}
