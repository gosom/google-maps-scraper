package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gosom/google-maps-scraper/api"
	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/log"
)

var (
	ErrAPIKeyNotFound = errors.New("api key not found")
	ErrAPIKeyRevoked  = errors.New("api key has been revoked")
)

type store struct {
	db *pgxpool.Pool
}

// New creates a new API store.
func New(db *pgxpool.Pool) api.IStore {
	return &store{db: db}
}

// ValidateAPIKey validates an API key and returns the key info.
func (s *store) ValidateAPIKey(ctx context.Context, key string) (int, string, error) { //nolint:gocritic // unnamedResult: return types match IStore interface signature exactly
	keyHash := cryptoext.Sha256Hash(key)

	var id int

	var name string

	var revokedAt *time.Time

	err := s.db.QueryRow(ctx,
		`SELECT id, name, revoked_at
		 FROM api_keys WHERE key_hash = $1`,
		keyHash,
	).Scan(&id, &name, &revokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", ErrAPIKeyNotFound
		}

		return 0, "", err
	}

	if revokedAt != nil {
		return 0, "", ErrAPIKeyRevoked
	}

	now := time.Now().UTC()
	oneMinAgo := now.Add(-1 * time.Minute)

	_, err = s.db.Exec(ctx, `UPDATE api_keys
		SET last_used_at = $1
		WHERE id = $2 AND (last_used_at IS NULL OR last_used_at < $3)`,
		now, id, oneMinAgo,
	)
	if err != nil {
		log.Warn("failed to update api key last_used_at", "error", err, "api_key_id", id)
	}

	return id, name, nil
}

// Ensure store implements api.IStore.
var _ api.IStore = (*store)(nil)
