package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// jobWebhookDeliveryRepository implements models.JobWebhookDeliveryRepository.
type jobWebhookDeliveryRepository struct {
	db *sql.DB
}

// NewJobWebhookDeliveryRepository creates a new JobWebhookDeliveryRepository backed by PostgreSQL.
func NewJobWebhookDeliveryRepository(db *sql.DB) models.JobWebhookDeliveryRepository {
	return &jobWebhookDeliveryRepository{db: db}
}

func (r *jobWebhookDeliveryRepository) Create(ctx context.Context, d *models.JobWebhookDelivery) error {
	const q = `
		INSERT INTO job_webhook_deliveries (job_id, webhook_config_id, status)
		VALUES ($1, $2, $3)`

	if d.Status == "" {
		d.Status = models.DeliveryStatusPending
	}

	_, err := r.db.ExecContext(ctx, q, d.JobID, d.WebhookConfigID, d.Status)
	return err
}

func (r *jobWebhookDeliveryRepository) ListByJobID(ctx context.Context, jobID string) ([]*models.JobWebhookDelivery, error) {
	const q = `
		SELECT job_id, webhook_config_id, attempts, max_attempts, last_attempt_at, next_retry_at, delivered_at, status
		FROM job_webhook_deliveries
		WHERE job_id = $1`

	rows, err := r.db.QueryContext(ctx, q, jobID)
	return r.scanMany(rows, err)
}

func (r *jobWebhookDeliveryRepository) ListPendingByJobID(ctx context.Context, jobID string) ([]*models.JobWebhookDelivery, error) {
	const q = `
		SELECT job_id, webhook_config_id, attempts, max_attempts, last_attempt_at, next_retry_at, delivered_at, status
		FROM job_webhook_deliveries
		WHERE job_id = $1 AND delivered_at IS NULL AND status != $2`

	rows, err := r.db.QueryContext(ctx, q, jobID, models.DeliveryStatusFailed)
	return r.scanMany(rows, err)
}

func (r *jobWebhookDeliveryRepository) MarkDelivering(ctx context.Context, jobID, webhookConfigID string) error {
	const q = `
		UPDATE job_webhook_deliveries
		SET status = $1, attempts = attempts + 1, last_attempt_at = $2
		WHERE job_id = $3 AND webhook_config_id = $4`

	_, err := r.db.ExecContext(ctx, q, models.DeliveryStatusDelivering, time.Now().UTC(), jobID, webhookConfigID)
	return err
}

func (r *jobWebhookDeliveryRepository) MarkDelivered(ctx context.Context, jobID, webhookConfigID string) error {
	const q = `
		UPDATE job_webhook_deliveries
		SET status = $1, delivered_at = $2
		WHERE job_id = $3 AND webhook_config_id = $4`

	_, err := r.db.ExecContext(ctx, q, models.DeliveryStatusDelivered, time.Now().UTC(), jobID, webhookConfigID)
	return err
}

func (r *jobWebhookDeliveryRepository) MarkFailed(ctx context.Context, jobID, webhookConfigID string) error {
	const q = `
		UPDATE job_webhook_deliveries
		SET status = $1
		WHERE job_id = $2 AND webhook_config_id = $3`

	_, err := r.db.ExecContext(ctx, q, models.DeliveryStatusFailed, jobID, webhookConfigID)
	return err
}

// ---- scan helpers ----

func (r *jobWebhookDeliveryRepository) scanMany(rows *sql.Rows, queryErr error) ([]*models.JobWebhookDelivery, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()

	var deliveries []*models.JobWebhookDelivery
	for rows.Next() {
		var d models.JobWebhookDelivery
		var lastAttemptAt sql.NullTime
		var nextRetryAt sql.NullTime
		var deliveredAt sql.NullTime

		if err := rows.Scan(
			&d.JobID, &d.WebhookConfigID, &d.Attempts, &d.MaxAttempts,
			&lastAttemptAt, &nextRetryAt, &deliveredAt, &d.Status,
		); err != nil {
			return nil, err
		}
		if lastAttemptAt.Valid {
			d.LastAttemptAt = &lastAttemptAt.Time
		}
		if nextRetryAt.Valid {
			d.NextRetryAt = &nextRetryAt.Time
		}
		if deliveredAt.Valid {
			d.DeliveredAt = &deliveredAt.Time
		}
		deliveries = append(deliveries, &d)
	}
	return deliveries, rows.Err()
}
