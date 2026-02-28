// Package postgres provides a PostgreSQL-backed rate limit store.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gosom/google-maps-scraper/ratelimit"
)

var _ ratelimit.Store = (*Store)(nil)

// Store implements ratelimit.Store using PostgreSQL with a fixed-window algorithm.
type Store struct {
	db *pgxpool.Pool
}

// New creates a new PostgreSQL rate limit store.
func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// Check implements ratelimit.Store.Check using a fixed-window algorithm.
//
// Algorithm:
// 1. If no record exists OR window has expired, create/reset with counter=1
// 2. If window is active and counter < max, increment and allow
// 3. If window is active and counter >= max, deny
func (s *Store) Check(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Result, error) {
	now := time.Now().UTC()

	// Use a single atomic query with ON CONFLICT to handle the upsert
	// This query:
	// 1. Tries to insert a new record with counter=1
	// 2. On conflict, checks if window expired - if so, resets; otherwise increments
	// 3. Returns the final counter and window_start
	var counter int

	var dbWindowStart time.Time

	err := s.db.QueryRow(ctx, `
		INSERT INTO rate_limits (key, counter, window_start)
		VALUES ($1, 1, $2)
		ON CONFLICT (key) DO UPDATE SET
			counter = CASE
				WHEN rate_limits.window_start + $3::interval <= $2
				THEN 1
				ELSE rate_limits.counter + 1
			END,
			window_start = CASE
				WHEN rate_limits.window_start + $3::interval <= $2
				THEN $2
				ELSE rate_limits.window_start
			END
		RETURNING counter, window_start
	`, key, now, window).Scan(&counter, &dbWindowStart)

	if err != nil {
		return ratelimit.Result{}, err
	}

	// Calculate reset time based on actual window start
	resetAt := dbWindowStart.Add(window)

	// Determine if this request is allowed
	allowed := counter <= limit

	remaining := limit - counter
	if remaining < 0 {
		remaining = 0
	}

	return ratelimit.Result{
		Allowed:   allowed,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}

// Reset implements ratelimit.Store.Reset by deleting the rate limit record.
func (s *Store) Reset(ctx context.Context, key string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM rate_limits WHERE key = $1`, key)
	return err
}

// Cleanup removes expired rate limit records. This can be called periodically
// to prevent the table from growing indefinitely.
func (s *Store) Cleanup(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)

	result, err := s.db.Exec(ctx, `DELETE FROM rate_limits WHERE window_start < $1`, cutoff)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}

// Get retrieves the current rate limit state for a key without incrementing.
// Returns nil if no rate limit exists for the key.
func (s *Store) Get(ctx context.Context, key string) (*ratelimit.Result, error) {
	var counter int

	var windowStart time.Time

	err := s.db.QueryRow(ctx, `
		SELECT counter, window_start FROM rate_limits WHERE key = $1
	`, key).Scan(&counter, &windowStart)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &ratelimit.Result{
		Allowed:   true, // Not meaningful without knowing max
		Remaining: 0,    // Not meaningful without knowing max
		ResetAt:   windowStart,
	}, nil
}
