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

// Webhook health-state constants. health_state is distinct from revoked_at:
// revoked_at is user-driven ("I deleted this webhook"), health_state is
// system-driven ("we stopped delivering because your endpoint was broken").
// A config is considered deliverable only when revoked_at IS NULL AND
// health_state != WebhookHealthDisabled — see IsDeliverable below.
const (
	WebhookHealthHealthy  = "healthy"
	WebhookHealthDegraded = "degraded"
	WebhookHealthDisabled = "disabled"
)

// AutoDisableThreshold is the consecutive-failure count that trips the
// circuit breaker. 10 matches the common reference implementation
// (InvokeBot's webhook-reliability-patterns guide). Each delivery has its
// own internal 5-retry budget with exponential backoff (cap 1h), so the
// counter only ticks up after a delivery has truly given up — 10 failed
// deliveries means the endpoint has been broken across 10 separate jobs.
//
// DegradedThreshold is reserved for a future UX banner ("this webhook is
// flapping — check your endpoint"); no behavioural change at the
// threshold today.
const (
	AutoDisableThreshold = 10
	DegradedThreshold    = 5
)

// WebhookConfig represents a user-level webhook endpoint configuration.
type WebhookConfig struct {
	ID              string `json:"id"`
	UserID          string `json:"user_id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	EncryptedSecret string `json:"-"` // AES-GCM encrypted; never exposed in API responses
	// SECURITY: delivery must connect to resolved_ip, not re-resolve DNS (TOCTOU/DNS rebinding prevention)
	ResolvedIP *net.IP    `json:"-"` // Internal; never exposed in API responses
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"` // User-driven soft delete

	// Circuit-breaker state (migration 000037).
	//
	// HealthState is one of WebhookHealthHealthy / WebhookHealthDegraded /
	// WebhookHealthDisabled, with the same enum-via-CHECK constraint on the
	// DB side. ConsecutiveFailures resets to 0 on any 2xx delivery and
	// trips to WebhookHealthDisabled when it reaches AutoDisableThreshold.
	HealthState         string     `json:"health_state"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	DisabledAt          *time.Time `json:"disabled_at,omitempty"`
	DisabledReason      *string    `json:"disabled_reason,omitempty"`
}

// IsActive returns true if the webhook config has not been revoked by the
// user. Use IsDeliverable when deciding whether to actually POST a payload.
func (w *WebhookConfig) IsActive() bool {
	return w.RevokedAt == nil
}

// IsDeliverable returns true when the webhook should receive new delivery
// attempts: the user has not revoked it, AND we have not auto-disabled it
// for chronic failure. The delivery loop should call this before issuing
// any HTTP POST so a tripped breaker actually stops the bleed.
func (w *WebhookConfig) IsDeliverable() bool {
	return w.RevokedAt == nil && w.HealthState != WebhookHealthDisabled
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

	// GetByID retrieves a webhook config by its UUID. When ownerUserID is
	// non-empty, the lookup is scoped to that user (defense-in-depth against
	// IDOR); pass "" only from trusted internal contexts (e.g. the delivery
	// worker) where ownership is enforced elsewhere.
	GetByID(ctx context.Context, id string, ownerUserID string) (*WebhookConfig, error)

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

	// RecordDeliverySuccess clears consecutive_failures and restores
	// health_state to 'healthy' for the given config. A no-op when the
	// row is already healthy and the counter is already 0 — safe to call
	// after every successful delivery without extra branching at the call
	// site.
	RecordDeliverySuccess(ctx context.Context, configID string) error

	// RecordDeliveryFailure atomically increments consecutive_failures
	// and transitions health_state when thresholds are crossed.
	//
	// Returns:
	//   newState     — the post-update health_state ('healthy' / 'degraded' / 'disabled')
	//   justDisabled — true ONLY on the request that flipped health_state
	//                  from non-disabled to disabled (so the caller can
	//                  fire a one-shot notification without re-emitting
	//                  on every subsequent failure)
	//
	// reason is stored on disabled_reason when (and only when) the breaker
	// trips — short machine-readable tag like "10_consecutive_failures",
	// "timeout", "5xx". Already-disabled rows keep their original reason.
	RecordDeliveryFailure(ctx context.Context, configID, reason string) (newState string, justDisabled bool, err error)

	// Reenable clears the disabled state for a config owned by the given
	// user. Idempotent — safe to call on an already-healthy row (no-op).
	// Returns ErrWebhookConfigNotFound when no matching active row exists.
	Reenable(ctx context.Context, configID, ownerUserID string) error
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

	// MarkDelivered sets delivered_at and status to delivered.
	MarkDelivered(ctx context.Context, jobID, webhookConfigID string) error

	// MarkFailed sets status to failed after exhausting retries.
	MarkFailed(ctx context.Context, jobID, webhookConfigID string) error

	// SetNextRetry resets a delivery to pending with a scheduled retry time.
	SetNextRetry(ctx context.Context, jobID, webhookConfigID string, nextRetryAt time.Time) error

	// CountRecentByUserID returns the number of delivery attempts for a user in the given window.
	CountRecentByUserID(ctx context.Context, userID string, since time.Time) (int, error)

	// CountRecentByIP returns the number of delivery attempts to a resolved IP in the given window.
	CountRecentByIP(ctx context.Context, resolvedIP string, since time.Time) (int, error)
}
