package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// webhookConfigRepository implements models.WebhookConfigRepository.
type webhookConfigRepository struct {
	db *sql.DB
}

// NewWebhookConfigRepository creates a new WebhookConfigRepository backed by PostgreSQL.
func NewWebhookConfigRepository(db *sql.DB) models.WebhookConfigRepository {
	return &webhookConfigRepository{db: db}
}

func (r *webhookConfigRepository) Create(ctx context.Context, cfg *models.WebhookConfig) error {
	const q = `
		INSERT INTO webhook_configs (
			id, user_id, name, url, secret_hash, resolved_ip,
			verified_at, created_at, updated_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	now := time.Now().UTC()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = now
	}

	var resolvedIP *string
	if cfg.ResolvedIP != nil {
		s := cfg.ResolvedIP.String()
		resolvedIP = &s
	}

	_, err := r.db.ExecContext(ctx, q,
		cfg.ID, cfg.UserID, cfg.Name, cfg.URL, cfg.SecretHash,
		resolvedIP, cfg.VerifiedAt, cfg.CreatedAt, cfg.UpdatedAt, cfg.RevokedAt,
	)
	return err
}

func (r *webhookConfigRepository) GetByID(ctx context.Context, id string) (*models.WebhookConfig, error) {
	const q = `
		SELECT id, user_id, name, url, secret_hash, resolved_ip,
		       verified_at, created_at, updated_at, revoked_at
		FROM webhook_configs
		WHERE id = $1`

	return r.scanOne(r.db.QueryRowContext(ctx, q, id))
}

func (r *webhookConfigRepository) ListByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	const q = `
		SELECT id, user_id, name, url, resolved_ip,
		       verified_at, created_at, updated_at, revoked_at
		FROM webhook_configs
		WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	return r.scanManyList(rows, err)
}

func (r *webhookConfigRepository) ListActiveByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	const q = `
		SELECT id, user_id, name, url, resolved_ip,
		       verified_at, created_at, updated_at, revoked_at
		FROM webhook_configs
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	return r.scanManyList(rows, err)
}

func (r *webhookConfigRepository) Update(ctx context.Context, cfg *models.WebhookConfig) error {
	const q = `
		UPDATE webhook_configs
		SET name = $1, url = $2, resolved_ip = $3, updated_at = $4
		WHERE id = $5 AND user_id = $6 AND revoked_at IS NULL`

	cfg.UpdatedAt = time.Now().UTC()

	var resolvedIP *string
	if cfg.ResolvedIP != nil {
		s := cfg.ResolvedIP.String()
		resolvedIP = &s
	}

	res, err := r.db.ExecContext(ctx, q, cfg.Name, cfg.URL, resolvedIP, cfg.UpdatedAt, cfg.ID, cfg.UserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return models.ErrWebhookConfigNotFound
	}
	return nil
}

func (r *webhookConfigRepository) Revoke(ctx context.Context, id string, ownerUserID string) error {
	const q = `
		UPDATE webhook_configs
		SET revoked_at = $1, updated_at = $1
		WHERE id = $2 AND user_id = $3 AND revoked_at IS NULL`

	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, q, now, id, ownerUserID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return models.ErrWebhookConfigNotFound
	}
	return nil
}

// ---- scan helpers ----

func (r *webhookConfigRepository) scanOne(row *sql.Row) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	var resolvedIP sql.NullString
	var verifiedAt sql.NullTime
	var revokedAt sql.NullTime

	err := row.Scan(
		&cfg.ID, &cfg.UserID, &cfg.Name, &cfg.URL, &cfg.SecretHash,
		&resolvedIP, &verifiedAt, &cfg.CreatedAt, &cfg.UpdatedAt, &revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrWebhookConfigNotFound
	}
	if err != nil {
		return nil, err
	}
	webhookApplyNullable(&cfg, resolvedIP, verifiedAt, revokedAt)
	return &cfg, nil
}

func (r *webhookConfigRepository) scanMany(rows *sql.Rows, queryErr error) ([]*models.WebhookConfig, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()

	var configs []*models.WebhookConfig
	for rows.Next() {
		var cfg models.WebhookConfig
		var resolvedIP sql.NullString
		var verifiedAt sql.NullTime
		var revokedAt sql.NullTime

		if err := rows.Scan(
			&cfg.ID, &cfg.UserID, &cfg.Name, &cfg.URL, &cfg.SecretHash,
			&resolvedIP, &verifiedAt, &cfg.CreatedAt, &cfg.UpdatedAt, &revokedAt,
		); err != nil {
			return nil, err
		}
		webhookApplyNullable(&cfg, resolvedIP, verifiedAt, revokedAt)
		configs = append(configs, &cfg)
	}
	return configs, rows.Err()
}

// scanManyList scans rows without secret_hash (defense in depth for list queries).
func (r *webhookConfigRepository) scanManyList(rows *sql.Rows, queryErr error) ([]*models.WebhookConfig, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()

	var configs []*models.WebhookConfig
	for rows.Next() {
		var cfg models.WebhookConfig
		var resolvedIP sql.NullString
		var verifiedAt sql.NullTime
		var revokedAt sql.NullTime

		if err := rows.Scan(
			&cfg.ID, &cfg.UserID, &cfg.Name, &cfg.URL,
			&resolvedIP, &verifiedAt, &cfg.CreatedAt, &cfg.UpdatedAt, &revokedAt,
		); err != nil {
			return nil, err
		}
		webhookApplyNullable(&cfg, resolvedIP, verifiedAt, revokedAt)
		configs = append(configs, &cfg)
	}
	return configs, rows.Err()
}

func webhookApplyNullable(cfg *models.WebhookConfig, resolvedIP sql.NullString, verifiedAt, revokedAt sql.NullTime) {
	if resolvedIP.Valid {
		ip := net.ParseIP(resolvedIP.String)
		cfg.ResolvedIP = &ip
	}
	if verifiedAt.Valid {
		cfg.VerifiedAt = &verifiedAt.Time
	}
	if revokedAt.Valid {
		cfg.RevokedAt = &revokedAt.Time
	}
}
