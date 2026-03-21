package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// apiKeyRepository implements the models.APIKeyRepository interface.
// It targets the api_keys table created by migration 000023.
type apiKeyRepository struct {
	db *sql.DB
}

// NewAPIKeyRepository creates a new APIKeyRepository backed by PostgreSQL.
func NewAPIKeyRepository(db *sql.DB) models.APIKeyRepository {
	return &apiKeyRepository{db: db}
}

// Create inserts a new API key record.
func (r *apiKeyRepository) Create(ctx context.Context, apiKey *models.APIKey) error {
	const q = `
		INSERT INTO api_keys (
			id, user_id, name, lookup_hash, key_hash, key_salt, hash_algorithm,
			key_hint_prefix, key_hint_suffix, created_at, scopes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	if apiKey.CreatedAt.IsZero() {
		apiKey.CreatedAt = time.Now().UTC()
	}

	scopesJSON, err := json.Marshal(apiKey.Scopes)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, q,
		apiKey.ID,
		apiKey.UserID,
		apiKey.Name,
		apiKey.LookupHash,
		apiKey.KeyHash,
		apiKey.KeySalt,
		apiKey.HashAlgorithm,
		apiKey.KeyHintPrefix,
		apiKey.KeyHintSuffix,
		apiKey.CreatedAt,
		scopesJSON,
	)
	return err
}

// GetByID retrieves an API key by its UUID.
func (r *apiKeyRepository) GetByID(ctx context.Context, id string) (*models.APIKey, error) {
	const q = `
		SELECT id, user_id, name, lookup_hash, key_hash, key_salt, hash_algorithm,
		       key_hint_prefix, key_hint_suffix, last_used_at, last_used_ip, usage_count,
		       created_at, revoked_at, scopes
		FROM api_keys
		WHERE id = $1`

	return r.scanOne(r.db.QueryRowContext(ctx, q, id))
}

// GetByLookupHash retrieves an active (non-revoked) API key by its HMAC lookup hash.
// Returns nil, nil when no matching active key exists (not an error during auth).
func (r *apiKeyRepository) GetByLookupHash(ctx context.Context, lookupHash string) (*models.APIKey, error) {
	const q = `
		SELECT id, user_id, name, lookup_hash, key_hash, key_salt, hash_algorithm,
		       key_hint_prefix, key_hint_suffix, last_used_at, last_used_ip, usage_count,
		       created_at, revoked_at, scopes
		FROM api_keys
		WHERE lookup_hash = $1 AND revoked_at IS NULL`

	key, err := r.scanOne(r.db.QueryRowContext(ctx, q, lookupHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return key, err
}

// ListByUserID retrieves all API keys for a user, including revoked ones.
func (r *apiKeyRepository) ListByUserID(ctx context.Context, userID string) ([]*models.APIKey, error) {
	const q = `
		SELECT id, user_id, name, lookup_hash, key_hash, key_salt, hash_algorithm,
		       key_hint_prefix, key_hint_suffix, last_used_at, last_used_ip, usage_count,
		       created_at, revoked_at, scopes
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	return r.scanMany(rows, err)
}

// ListActiveByUserID retrieves all active (non-revoked) API keys for a user.
func (r *apiKeyRepository) ListActiveByUserID(ctx context.Context, userID string) ([]*models.APIKey, error) {
	const q = `
		SELECT id, user_id, name, lookup_hash, key_hash, key_salt, hash_algorithm,
		       key_hint_prefix, key_hint_suffix, last_used_at, last_used_ip, usage_count,
		       created_at, revoked_at, scopes
		FROM api_keys
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	return r.scanMany(rows, err)
}

// UpdateLastUsed atomically increments usage_count and records last_used_at / last_used_ip.
func (r *apiKeyRepository) UpdateLastUsed(ctx context.Context, id string, ipAddress net.IP) error {
	const q = `
		UPDATE api_keys
		SET last_used_at = $1,
		    last_used_ip = $2,
		    usage_count  = usage_count + 1
		WHERE id = $3`

	_, err := r.db.ExecContext(ctx, q, time.Now().UTC(), ipAddress.String(), id)
	return err
}

// Revoke soft-deletes an API key. ownerUserID prevents cross-user revocation.
func (r *apiKeyRepository) Revoke(ctx context.Context, id string, ownerUserID string) error {
	const q = `
		UPDATE api_keys
		SET revoked_at = $1
		WHERE id = $2 AND user_id = $3 AND revoked_at IS NULL`

	res, err := r.db.ExecContext(ctx, q, time.Now().UTC(), id, ownerUserID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return models.ErrAPIKeyNotFound
	}
	return nil
}

// ---- internal scan helpers ----

func (r *apiKeyRepository) scanOne(row *sql.Row) (*models.APIKey, error) {
	var k models.APIKey
	var lastUsedAt sql.NullTime
	var lastUsedIP sql.NullString
	var revokedAt sql.NullTime
	var scopesJSON []byte

	err := row.Scan(
		&k.ID, &k.UserID, &k.Name,
		&k.LookupHash, &k.KeyHash, &k.KeySalt, &k.HashAlgorithm,
		&k.KeyHintPrefix, &k.KeyHintSuffix,
		&lastUsedAt, &lastUsedIP, &k.UsageCount,
		&k.CreatedAt, &revokedAt, &scopesJSON,
	)
	if err != nil {
		return nil, err
	}
	apiKeyApplyNullable(&k, lastUsedAt, lastUsedIP, revokedAt, scopesJSON)
	return &k, nil
}

func (r *apiKeyRepository) scanMany(rows *sql.Rows, queryErr error) ([]*models.APIKey, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()

	var keys []*models.APIKey
	for rows.Next() {
		var k models.APIKey
		var lastUsedAt sql.NullTime
		var lastUsedIP sql.NullString
		var revokedAt sql.NullTime
		var scopesJSON []byte

		if err := rows.Scan(
			&k.ID, &k.UserID, &k.Name,
			&k.LookupHash, &k.KeyHash, &k.KeySalt, &k.HashAlgorithm,
			&k.KeyHintPrefix, &k.KeyHintSuffix,
			&lastUsedAt, &lastUsedIP, &k.UsageCount,
			&k.CreatedAt, &revokedAt, &scopesJSON,
		); err != nil {
			return nil, err
		}
		apiKeyApplyNullable(&k, lastUsedAt, lastUsedIP, revokedAt, scopesJSON)
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

func apiKeyApplyNullable(k *models.APIKey, lastUsedAt sql.NullTime, lastUsedIP sql.NullString, revokedAt sql.NullTime, scopesJSON []byte) {
	if lastUsedAt.Valid {
		k.LastUsedAt = &lastUsedAt.Time
	}
	if lastUsedIP.Valid {
		ip := net.ParseIP(lastUsedIP.String)
		k.LastUsedIP = &ip
	}
	if revokedAt.Valid {
		k.RevokedAt = &revokedAt.Time
	}
	if len(scopesJSON) > 0 {
		_ = json.Unmarshal(scopesJSON, &k.Scopes)
	}
}
