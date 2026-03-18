package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// APIKey represents a user-generated API key stored in the database.
// The raw key is never persisted; only its SHA-256 hash is stored.
type APIKey struct {
	ID        string
	UserID    string
	KeyHash   string
	PlanTier  string // "free" or "paid"
	Name      string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// ErrAPIKeyNotFound is returned when an API key hash has no matching active record.
var ErrAPIKeyNotFound = errors.New("api key not found")

// UserAPIKeyRepository manages user API key lookups.
type UserAPIKeyRepository interface {
	GetByHash(ctx context.Context, hash string) (APIKey, error)
}

type userAPIKeyRepository struct {
	db *sql.DB
}

// NewUserAPIKeyRepository creates a new UserAPIKeyRepository backed by PostgreSQL.
func NewUserAPIKeyRepository(db *sql.DB) UserAPIKeyRepository {
	return &userAPIKeyRepository{db: db}
}

// HashAPIKey returns the lowercase hex SHA-256 hash of a raw API key string.
// Use this before calling GetByHash.
func HashAPIKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(h[:])
}

// GetByHash retrieves an active (non-revoked) API key by its SHA-256 hash.
// Returns ErrAPIKeyNotFound when no active record matches.
func (r *userAPIKeyRepository) GetByHash(ctx context.Context, hash string) (APIKey, error) {
	const q = `
		SELECT id, user_id, key_hash, plan_tier, COALESCE(name, ''), created_at, revoked_at
		FROM user_api_keys
		WHERE key_hash = $1 AND revoked_at IS NULL`

	var k APIKey
	err := r.db.QueryRowContext(ctx, q, hash).Scan(
		&k.ID, &k.UserID, &k.KeyHash, &k.PlanTier, &k.Name, &k.CreatedAt, &k.RevokedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIKey{}, ErrAPIKeyNotFound
		}
		return APIKey{}, err
	}
	return k, nil
}
