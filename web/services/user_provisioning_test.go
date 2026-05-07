package services

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/gosom/google-maps-scraper/postgres"
)

// openServicesTestDB opens a DB connection from PG_TEST_DSN; skips if unset.
func openServicesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("Skipping: PG_TEST_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return db
}

// TestProvision_Concurrent_DoesNotErrorOrDuplicate spawns N goroutines that
// all call Provision for the same Clerk user ID at the same time. This
// reproduces the original race that caused "Failed to load dashboard" toasts
// on first sign-up: parallel /api/v1/* requests all hitting auth-middleware
// lazy provisioning concurrently. Post-fix, all goroutines must succeed and
// the users table must contain exactly one row.
func TestProvision_Concurrent_DoesNotErrorOrDuplicate(t *testing.T) {
	db := openServicesTestDB(t)
	defer db.Close()

	ctx := context.Background()
	userID := "user_test_provision_concurrent_" + fmt.Sprintf("%d", time.Now().UnixNano())
	email := userID + "@test.invalid"
	t.Cleanup(func() {
		// Best-effort cleanup of side effects in dependency order.
		_, _ = db.ExecContext(ctx, `DELETE FROM credit_transactions WHERE user_id = $1`, userID)
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	repo := postgres.NewUserRepository(db)
	// billingSvc nil is supported (skip Stripe customer create) — the auth
	// middleware passes nil in test builds; mirror that here.
	svc := NewUserProvisioning(db, repo, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	const N = 8
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Provision(ctx, userID, email)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Provision returned error: %v", err)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Errorf("users row count: want 1, got %d", count)
	}

	// Signup bonus should be granted exactly once (idempotent via reference_id).
	var bonusCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM credit_transactions WHERE user_id = $1 AND reference_id = 'signup_bonus' AND reference_type = 'system'`,
		userID).Scan(&bonusCount); err != nil {
		t.Fatalf("count bonuses: %v", err)
	}
	if bonusCount != 1 {
		t.Errorf("signup_bonus transactions: want 1, got %d", bonusCount)
	}
}
