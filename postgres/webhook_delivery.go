package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
		WHERE job_id = $1 AND status = $2`

	rows, err := r.db.QueryContext(ctx, q, jobID, models.DeliveryStatusPending)
	return r.scanMany(rows, err)
}

func (r *jobWebhookDeliveryRepository) MarkDelivered(ctx context.Context, jobID, webhookConfigID string) error {
	const q = `
		UPDATE job_webhook_deliveries
		SET status = $1, delivered_at = $2
		WHERE job_id = $3 AND webhook_config_id = $4 AND status = 'delivering'`

	res, err := r.db.ExecContext(ctx, q, models.DeliveryStatusDelivered, time.Now().UTC(), jobID, webhookConfigID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.ErrDeliveryNotFound
	}
	return nil
}

func (r *jobWebhookDeliveryRepository) MarkFailed(ctx context.Context, jobID, webhookConfigID string) error {
	const q = `
		UPDATE job_webhook_deliveries
		SET status = $1
		WHERE job_id = $2 AND webhook_config_id = $3 AND status = 'delivering'`

	res, err := r.db.ExecContext(ctx, q, models.DeliveryStatusFailed, jobID, webhookConfigID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.ErrDeliveryNotFound
	}
	return nil
}

func (r *jobWebhookDeliveryRepository) ListPendingGlobal(ctx context.Context, limit int) ([]*models.JobWebhookDelivery, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback is a no-op after commit

	const selectQ = `
		SELECT job_id, webhook_config_id, attempts, max_attempts, last_attempt_at, next_retry_at, delivered_at, status
		FROM job_webhook_deliveries
		WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY next_retry_at NULLS FIRST
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	rows, err := tx.QueryContext(ctx, selectQ, limit)
	if err != nil {
		return nil, fmt.Errorf("select pending: %w", err)
	}

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
			rows.Close()
			return nil, fmt.Errorf("scan row: %w", err)
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
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}

	const updateQ = `
		UPDATE job_webhook_deliveries
		SET status = 'delivering', attempts = attempts + 1, last_attempt_at = NOW()
		WHERE job_id = $1 AND webhook_config_id = $2`

	now := time.Now().UTC()
	for _, d := range deliveries {
		if _, err := tx.ExecContext(ctx, updateQ, d.JobID, d.WebhookConfigID); err != nil {
			return nil, fmt.Errorf("update delivery: %w", err)
		}
		// Reflect the changes in the returned structs.
		d.Status = models.DeliveryStatusDelivering
		d.Attempts++
		d.LastAttemptAt = &now
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return deliveries, nil
}

func (r *jobWebhookDeliveryRepository) SetNextRetry(ctx context.Context, jobID, webhookConfigID string, nextRetryAt time.Time) error {
	const q = `
		UPDATE job_webhook_deliveries
		SET status = 'pending', next_retry_at = $1
		WHERE job_id = $2 AND webhook_config_id = $3 AND status = 'delivering'`

	res, err := r.db.ExecContext(ctx, q, nextRetryAt, jobID, webhookConfigID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.ErrDeliveryNotFound
	}
	return nil
}

func (r *jobWebhookDeliveryRepository) CreateBatch(ctx context.Context, deliveries []*models.JobWebhookDelivery) error {
	if len(deliveries) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString(`INSERT INTO job_webhook_deliveries (job_id, webhook_config_id, status) VALUES `)

	args := make([]interface{}, 0, len(deliveries)*3)
	for i, d := range deliveries {
		if i > 0 {
			b.WriteString(", ")
		}
		status := d.Status
		if status == "" {
			status = models.DeliveryStatusPending
		}
		base := i * 3
		fmt.Fprintf(&b, "($%d, $%d, $%d)", base+1, base+2, base+3)
		args = append(args, d.JobID, d.WebhookConfigID, status)
	}
	b.WriteString(` ON CONFLICT (job_id, webhook_config_id) DO NOTHING`)

	_, err := r.db.ExecContext(ctx, b.String(), args...)
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
