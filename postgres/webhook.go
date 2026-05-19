package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// webhookCommonColumns is the canonical projection for List* / Get* queries
// that DO NOT need the encrypted secret. Keeping it in one place means a
// future column addition only touches this constant + the matching scan
// helper, not every individual query.
const webhookCommonColumns = `id, user_id, name, url, resolved_ip,
	verified_at, created_at, updated_at, revoked_at,
	consecutive_failures, health_state, disabled_at, disabled_reason`

// webhookColumnsWithSecret is the same projection plus encrypted_secret,
// used by Get* and the delivery-worker-facing list query that needs to
// sign payloads. The column order is fixed across both projections so
// the encrypted_secret slot is always the same index from the right.
const webhookColumnsWithSecret = `id, user_id, name, url, encrypted_secret, resolved_ip,
	verified_at, created_at, updated_at, revoked_at,
	consecutive_failures, health_state, disabled_at, disabled_reason`

func (r *webhookConfigRepository) Create(ctx context.Context, cfg *models.WebhookConfig) error {
	const q = `
		INSERT INTO webhook_configs (
			id, user_id, name, url, encrypted_secret, resolved_ip,
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
		cfg.ID, cfg.UserID, cfg.Name, cfg.URL, cfg.EncryptedSecret,
		resolvedIP, cfg.VerifiedAt, cfg.CreatedAt, cfg.UpdatedAt, cfg.RevokedAt,
	)
	return err
}

// GetByID returns a webhook config including its encrypted_secret.
// When ownerUserID is non-empty the query is scoped to that user (defense-in-depth
// against IDOR from a handler that forgets its own ownership check). Pass ""
// only from trusted internal contexts (e.g. the delivery worker) that enforce
// ownership elsewhere — the worker needs the secret to sign payloads regardless
// of which user owns the config.
func (r *webhookConfigRepository) GetByID(ctx context.Context, id string, ownerUserID string) (*models.WebhookConfig, error) {
	const q = `SELECT ` + webhookColumnsWithSecret + `
		FROM webhook_configs
		WHERE id = $1 AND (user_id = $2 OR $2 = '')`

	return r.scanOne(r.db.QueryRowContext(ctx, q, id, ownerUserID))
}

func (r *webhookConfigRepository) ListByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	const q = `SELECT ` + webhookCommonColumns + `
		FROM webhook_configs
		WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	return r.scanManyList(rows, err)
}

// ListActiveByUserID returns configs where the user has NOT revoked them
// AND the circuit breaker has NOT auto-disabled them — i.e. configs we
// would actually attempt delivery to today. Same predicate as the
// idx_webhook_configs_active index (migration 000037).
func (r *webhookConfigRepository) ListActiveByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	const q = `SELECT ` + webhookCommonColumns + `
		FROM webhook_configs
		WHERE user_id = $1 AND revoked_at IS NULL AND health_state <> 'disabled'
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
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
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
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return models.ErrWebhookConfigNotFound
	}
	return nil
}

// ListActiveWithSecretByUserID is the delivery-worker variant of
// ListActiveByUserID: same predicate (excludes revoked AND disabled), but
// also projects encrypted_secret so the worker can sign payloads.
func (r *webhookConfigRepository) ListActiveWithSecretByUserID(ctx context.Context, userID string) ([]*models.WebhookConfig, error) {
	const q = `SELECT ` + webhookColumnsWithSecret + `
		FROM webhook_configs
		WHERE user_id = $1 AND revoked_at IS NULL AND health_state <> 'disabled'
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	return r.scanManyWithSecret(rows, err)
}

// ---- scan helpers ----

// webhookNullables is the bag of NULLable column scan targets common to
// every webhook_configs SELECT in this file. Pulled out as a struct so the
// three scan helpers below can declare one local variable per row instead
// of four, and so adding a future nullable column means one struct field
// + one webhookApplyNullable line.
type webhookNullables struct {
	resolvedIP     sql.NullString
	verifiedAt     sql.NullTime
	revokedAt      sql.NullTime
	disabledAt     sql.NullTime
	disabledReason sql.NullString
}

func (r *webhookConfigRepository) scanOne(row *sql.Row) (*models.WebhookConfig, error) {
	var cfg models.WebhookConfig
	var n webhookNullables

	err := row.Scan(
		&cfg.ID, &cfg.UserID, &cfg.Name, &cfg.URL, &cfg.EncryptedSecret,
		&n.resolvedIP, &n.verifiedAt, &cfg.CreatedAt, &cfg.UpdatedAt, &n.revokedAt,
		&cfg.ConsecutiveFailures, &cfg.HealthState, &n.disabledAt, &n.disabledReason,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, models.ErrWebhookConfigNotFound
	}
	if err != nil {
		return nil, err
	}
	webhookApplyNullable(&cfg, n)
	return &cfg, nil
}

// scanManyList scans rows without the encrypted secret (defense in depth for list queries).
func (r *webhookConfigRepository) scanManyList(rows *sql.Rows, queryErr error) ([]*models.WebhookConfig, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()

	var configs []*models.WebhookConfig
	for rows.Next() {
		var cfg models.WebhookConfig
		var n webhookNullables

		if err := rows.Scan(
			&cfg.ID, &cfg.UserID, &cfg.Name, &cfg.URL,
			&n.resolvedIP, &n.verifiedAt, &cfg.CreatedAt, &cfg.UpdatedAt, &n.revokedAt,
			&cfg.ConsecutiveFailures, &cfg.HealthState, &n.disabledAt, &n.disabledReason,
		); err != nil {
			return nil, err
		}
		webhookApplyNullable(&cfg, n)
		configs = append(configs, &cfg)
	}
	return configs, rows.Err()
}

// scanManyWithSecret scans rows including the encrypted_secret column.
func (r *webhookConfigRepository) scanManyWithSecret(rows *sql.Rows, queryErr error) ([]*models.WebhookConfig, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()

	var configs []*models.WebhookConfig
	for rows.Next() {
		var cfg models.WebhookConfig
		var n webhookNullables

		if err := rows.Scan(
			&cfg.ID, &cfg.UserID, &cfg.Name, &cfg.URL, &cfg.EncryptedSecret,
			&n.resolvedIP, &n.verifiedAt, &cfg.CreatedAt, &cfg.UpdatedAt, &n.revokedAt,
			&cfg.ConsecutiveFailures, &cfg.HealthState, &n.disabledAt, &n.disabledReason,
		); err != nil {
			return nil, err
		}
		webhookApplyNullable(&cfg, n)
		configs = append(configs, &cfg)
	}
	return configs, rows.Err()
}

// webhookApplyNullable copies optional column values from the scan-target
// struct into the model's nullable pointer fields. Centralises the
// NULL-to-nil translation so every scan helper handles new optional
// columns the same way.
func webhookApplyNullable(cfg *models.WebhookConfig, n webhookNullables) {
	if n.resolvedIP.Valid {
		ip := net.ParseIP(n.resolvedIP.String)
		cfg.ResolvedIP = &ip
	}
	if n.verifiedAt.Valid {
		cfg.VerifiedAt = &n.verifiedAt.Time
	}
	if n.revokedAt.Valid {
		cfg.RevokedAt = &n.revokedAt.Time
	}
	if n.disabledAt.Valid {
		cfg.DisabledAt = &n.disabledAt.Time
	}
	if n.disabledReason.Valid {
		reason := n.disabledReason.String
		cfg.DisabledReason = &reason
	}
}

// RecordDeliverySuccess implements WebhookConfigRepository.
// Idempotent: only writes when there's something to change, so the hot
// path on a healthy webhook doesn't allocate row-level locks.
func (r *webhookConfigRepository) RecordDeliverySuccess(ctx context.Context, configID string) error {
	const q = `
		UPDATE webhook_configs
		SET consecutive_failures = 0,
		    health_state = 'healthy',
		    disabled_at = NULL,
		    disabled_reason = NULL,
		    updated_at = NOW()
		WHERE id = $1
		  AND (consecutive_failures > 0 OR health_state <> 'healthy')`
	_, err := r.db.ExecContext(ctx, q, configID)
	return err
}

// RecordDeliveryFailure implements WebhookConfigRepository.
//
// Single-statement atomic update so concurrent failures from parallel
// delivery workers cannot race the consecutive_failures counter past the
// threshold and double-fire the auto-disable notification. The RETURNING
// clause reports the post-update state and a boolean indicating whether
// THIS call is the one that tripped the breaker.
func (r *webhookConfigRepository) RecordDeliveryFailure(ctx context.Context, configID, reason string) (newState string, justDisabled bool, err error) {
	// $2 = AutoDisableThreshold, $3 = DegradedThreshold, $4 = reason.
	const q = `
		UPDATE webhook_configs
		SET consecutive_failures = consecutive_failures + 1,
		    health_state = CASE
		        -- Once disabled, stay disabled until an explicit Reenable
		        -- or RecordDeliverySuccess clears the state. Without this
		        -- clause, the next-tier CASE arm below could demote a
		        -- disabled row to 'degraded' while disabled_at is still
		        -- populated, violating chk_webhook_configs_disabled_at_consistency.
		        WHEN health_state = 'disabled' THEN 'disabled'
		        WHEN consecutive_failures + 1 >= $2 THEN 'disabled'
		        WHEN consecutive_failures + 1 >= $3 THEN 'degraded'
		        ELSE health_state
		    END,
		    disabled_at = CASE
		        WHEN consecutive_failures + 1 >= $2 AND disabled_at IS NULL THEN NOW()
		        ELSE disabled_at
		    END,
		    disabled_reason = CASE
		        WHEN consecutive_failures + 1 >= $2 AND disabled_reason IS NULL THEN $4
		        ELSE disabled_reason
		    END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING
		    health_state,
		    (health_state = 'disabled' AND disabled_at = updated_at)`
	err = r.db.QueryRowContext(ctx, q, configID,
		models.AutoDisableThreshold, models.DegradedThreshold, reason,
	).Scan(&newState, &justDisabled)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, models.ErrWebhookConfigNotFound
	}
	return newState, justDisabled, err
}

// Reenable implements WebhookConfigRepository. Scoped by ownerUserID
// because re-enabling is a user-driven recovery action; the delivery
// worker must never silently re-enable a config it just disabled (that
// would defeat the breaker).
func (r *webhookConfigRepository) Reenable(ctx context.Context, configID, ownerUserID string) error {
	const q = `
		UPDATE webhook_configs
		SET consecutive_failures = 0,
		    health_state = 'healthy',
		    disabled_at = NULL,
		    disabled_reason = NULL,
		    updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, configID, ownerUserID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return models.ErrWebhookConfigNotFound
	}
	return nil
}
