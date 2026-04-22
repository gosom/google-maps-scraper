package billing

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxSerializableRetries = 3
	pgSerializationFailure = "40001"
	pgDeadlockDetected     = "40P01"
)

// isSerializationFailure returns true if the error is a PostgreSQL
// serialization failure (40001) or deadlock (40P01). Both are retryable
// because PostgreSQL guarantees no side effects from the aborted transaction.
func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgSerializationFailure || pgErr.Code == pgDeadlockDetected
	}
	return false
}

// withSerializableRetry executes fn inside a serializable transaction,
// retrying up to maxSerializableRetries times on serialization failures.
// fn receives a started *sql.Tx and must NOT call Commit or Rollback — the
// helper handles both. If fn returns nil, the transaction is committed;
// if it returns an error, the transaction is rolled back.
func withSerializableRetry(ctx context.Context, db *sql.DB, log *slog.Logger, fn func(tx *sql.Tx) error) error {
	for attempt := 1; attempt <= maxSerializableRetries; attempt++ {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}

		fnErr := fn(tx)
		if fnErr != nil {
			_ = tx.Rollback()
			if isSerializationFailure(fnErr) && attempt < maxSerializableRetries {
				log.Warn("serializable_tx_retry",
					slog.Int("attempt", attempt),
					slog.String("reason", "serialization_failure"),
				)
				jitterSleep(ctx, attempt)
				continue
			}
			return fnErr
		}

		if commitErr := tx.Commit(); commitErr != nil {
			if isSerializationFailure(commitErr) && attempt < maxSerializableRetries {
				log.Warn("serializable_tx_retry",
					slog.Int("attempt", attempt),
					slog.String("reason", "serialization_failure_on_commit"),
				)
				jitterSleep(ctx, attempt)
				continue
			}
			return commitErr
		}
		return nil
	}
	return errors.New("serializable transaction failed after maximum retries")
}

// withSerializableRetryHTTP is like withSerializableRetry but for webhook
// handlers that return (httpStatus, error). The fn must NOT call Commit or
// Rollback. On success fn returns (status, nil) and the helper commits.
func withSerializableRetryHTTP(ctx context.Context, db *sql.DB, log *slog.Logger, fn func(tx *sql.Tx) (int, error)) (int, error) {
	for attempt := 1; attempt <= maxSerializableRetries; attempt++ {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return 500, err
		}

		status, fnErr := fn(tx)
		if fnErr != nil {
			_ = tx.Rollback()
			if isSerializationFailure(fnErr) && attempt < maxSerializableRetries {
				log.Warn("serializable_tx_retry",
					slog.Int("attempt", attempt),
					slog.String("reason", "serialization_failure"),
				)
				jitterSleep(ctx, attempt)
				continue
			}
			return status, fnErr
		}

		if commitErr := tx.Commit(); commitErr != nil {
			if isSerializationFailure(commitErr) && attempt < maxSerializableRetries {
				log.Warn("serializable_tx_retry",
					slog.Int("attempt", attempt),
					slog.String("reason", "serialization_failure_on_commit"),
				)
				jitterSleep(ctx, attempt)
				continue
			}
			return 500, commitErr
		}
		return status, nil
	}
	return 500, errors.New("serializable transaction failed after maximum retries")
}

// jitterSleep waits for a randomized backoff duration based on the attempt
// number. Respects context cancellation.
func jitterSleep(ctx context.Context, attempt int) {
	base := time.Duration(attempt) * 50 * time.Millisecond
	jitter := time.Duration(rand.Int64N(int64(base)))
	select {
	case <-time.After(base + jitter):
	case <-ctx.Done():
	}
}
