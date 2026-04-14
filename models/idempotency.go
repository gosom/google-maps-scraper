package models

import (
	"context"
	"errors"
	"time"
)

// IdempotencyRecord mirrors a row in the idempotency_keys table created
// by migration 000034. The middleware that owns the table lives in
// web/middleware/idempotency.go and the postgres implementation in
// postgres/idempotency.go — both import this type from the neutral
// models package to avoid an import cycle through web/auth → postgres.
//
// StatusCode and ResponseBody are zero-valued while Status is "started"
// and populated by Complete after the inner handler returns.
type IdempotencyRecord struct {
	ID           string
	UserID       string
	Key          string
	Method       string
	Path         string
	RequestHash  string
	Status       string // "started" | "completed"
	StatusCode   int
	ResponseBody []byte
	CreatedAt    time.Time
	CompletedAt  *time.Time
	ExpiresAt    time.Time
}

// ErrIdempotencyConflict is returned by IdempotencyRepo.InsertStarted
// when the unique (user_id, key) constraint rejects the insert because
// another request already owns the key. The middleware uses this
// sentinel to branch into the conflict / replay path.
var ErrIdempotencyConflict = errors.New("idempotency key already in use")

// IdempotencyRepo is the storage interface the middleware depends on.
// See web/middleware/idempotency.go for the design rationale and
// postgres/idempotency.go for the concrete implementation.
type IdempotencyRepo interface {
	// InsertStarted atomically reserves (user_id, key) by inserting a
	// row with status='started'. Implementations MUST use
	// `INSERT ... ON CONFLICT (user_id, key) DO NOTHING` and return
	// ErrIdempotencyConflict when no row is inserted — that is what
	// makes the reservation atomic across concurrent retries.
	InsertStarted(ctx context.Context, rec IdempotencyRecord) error

	// Get returns the existing row for (user_id, key), or (nil, nil)
	// if none exists. Used by the conflict branch.
	Get(ctx context.Context, userID, key string) (*IdempotencyRecord, error)

	// Complete updates an existing 'started' row to 'completed' with
	// the captured response. Called from the happy path after the
	// inner handler returns.
	Complete(ctx context.Context, id string, statusCode int, body []byte) error

	// CleanupExpired removes (a) completed rows past expires_at and
	// (b) started rows older than the stuck-row grace period. Returns
	// (completedDeleted, startedDeleted, error). Called by the
	// cleanup goroutine; see RunIdempotencyCleanup in the middleware.
	CleanupExpired(ctx context.Context, stuckGrace time.Duration) (int64, int64, error)
}
