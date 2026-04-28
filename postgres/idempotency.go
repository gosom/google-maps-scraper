package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// IdempotencyRepository implements models.IdempotencyRepo against
// the idempotency_keys table created by migration 000034. The
// middleware-side interface lives in web/middleware/idempotency.go and
// owns the design rationale for the two-phase pattern.
type IdempotencyRepository struct {
	db *sql.DB
}

// NewIdempotencyRepository constructs a postgres-backed idempotency
// repository. Returns the concrete type so callers can assert it
// satisfies models.IdempotencyRepo at construction time.
func NewIdempotencyRepository(db *sql.DB) *IdempotencyRepository {
	return &IdempotencyRepository{db: db}
}

// Compile-time assertion that the concrete type satisfies the
// middleware interface — catches signature drift on either side.
var _ models.IdempotencyRepo = (*IdempotencyRepository)(nil)

// InsertStarted reserves (user_id, key) atomically. The
// `ON CONFLICT (user_id, key) DO NOTHING` clause turns the unique
// constraint into a no-throw lock: the winning request gets a row
// inserted (RowsAffected = 1), every other concurrent request gets
// RowsAffected = 0 which we surface as ErrIdempotencyConflict.
func (r *IdempotencyRepository) InsertStarted(ctx context.Context, rec models.IdempotencyRecord) error {
	const q = `
		INSERT INTO idempotency_keys
			(id, user_id, key, method, path, request_hash, status, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'started', $7, $8)
		ON CONFLICT (user_id, key) DO NOTHING`

	res, err := r.db.ExecContext(ctx, q,
		rec.ID,
		rec.UserID,
		rec.Key,
		rec.Method,
		rec.Path,
		rec.RequestHash,
		rec.CreatedAt,
		rec.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("idempotency insert: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("idempotency insert rows affected: %w", err)
	}
	if n == 0 {
		return models.ErrIdempotencyConflict
	}
	return nil
}

// Get returns the existing row for (user_id, key), or (nil, nil) if no
// row exists. Used by the middleware's conflict branch to decide
// between replay (status='completed') and 409 (status='started').
func (r *IdempotencyRepository) Get(ctx context.Context, userID, key string) (*models.IdempotencyRecord, error) {
	const q = `
		SELECT id, user_id, key, method, path, request_hash, status,
		       COALESCE(status_code, 0), COALESCE(response_body, ''::bytea),
		       created_at, completed_at, expires_at
		FROM idempotency_keys
		WHERE user_id = $1 AND key = $2`

	var rec models.IdempotencyRecord
	var completedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, q, userID, key).Scan(
		&rec.ID,
		&rec.UserID,
		&rec.Key,
		&rec.Method,
		&rec.Path,
		&rec.RequestHash,
		&rec.Status,
		&rec.StatusCode,
		&rec.ResponseBody,
		&rec.CreatedAt,
		&completedAt,
		&rec.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("idempotency get: %w", err)
	}
	if completedAt.Valid {
		t := completedAt.Time
		rec.CompletedAt = &t
	}
	return &rec, nil
}

// Complete marks a 'started' row as 'completed' with the captured
// response. The WHERE clause filters by id (the UUIDv7 the middleware
// generated for InsertStarted), so a stale Complete from a previous
// owner cannot overwrite a different row that happens to share the
// same (user_id, key).
func (r *IdempotencyRepository) Complete(ctx context.Context, id string, statusCode int, body []byte) error {
	const q = `
		UPDATE idempotency_keys
		SET status = 'completed',
		    status_code = $2,
		    response_body = $3,
		    completed_at = NOW()
		WHERE id = $1 AND status = 'started'`

	res, err := r.db.ExecContext(ctx, q, id, statusCode, body)
	if err != nil {
		return fmt.Errorf("idempotency complete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("idempotency complete rows affected: %w", err)
	}
	if n == 0 {
		// Row was reaped by cleanup or already completed. Not an error
		// the caller can act on; the middleware logs it but lets the
		// response continue.
		return fmt.Errorf("idempotency complete: no started row matched id %s", id)
	}
	return nil
}

// CleanupExpired removes (a) completed rows whose TTL has expired and
// (b) started rows older than stuckGrace (a request that crashed
// mid-handler). Returns (completedDeleted, startedDeleted, error).
//
// The two DELETEs are kept separate so the per-class counts stay
// observable in the cleanup goroutine's log line — useful for spotting
// runaway crash loops via the started_deleted metric.
func (r *IdempotencyRepository) CleanupExpired(ctx context.Context, stuckGrace time.Duration) (int64, int64, error) {
	const completedQ = `DELETE FROM idempotency_keys WHERE status = 'completed' AND expires_at < NOW()`
	res, err := r.db.ExecContext(ctx, completedQ)
	if err != nil {
		return 0, 0, fmt.Errorf("idempotency cleanup completed: %w", err)
	}
	completed, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("idempotency cleanup completed rows affected: %w", err)
	}

	const startedQ = `DELETE FROM idempotency_keys WHERE status = 'started' AND created_at < NOW() - $1::interval`
	res, err = r.db.ExecContext(ctx, startedQ, fmt.Sprintf("%d seconds", int(stuckGrace.Seconds())))
	if err != nil {
		return completed, 0, fmt.Errorf("idempotency cleanup started: %w", err)
	}
	started, err := res.RowsAffected()
	if err != nil {
		return completed, 0, fmt.Errorf("idempotency cleanup started rows affected: %w", err)
	}
	return completed, started, nil
}
