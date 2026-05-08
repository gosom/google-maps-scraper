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
	"go.uber.org/goleak"

	"github.com/gosom/google-maps-scraper/postgres"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// openServicesTestDB opens a DB connection from PG_TEST_DSN; skips if unset.
// Registers db.Close via t.Cleanup so row-deletion cleanups registered after
// this call still see an open pool (LIFO ordering: Close runs last).
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
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestProvision_Concurrent_DoesNotErrorOrDuplicate spawns N goroutines that
// all call Provision for the same Clerk user ID at the same time. This
// reproduces the original race that caused "Failed to load dashboard" toasts
// on first sign-up: parallel /api/v1/* requests all hitting auth-middleware
// lazy provisioning concurrently. Post-fix, all goroutines must succeed and
// the users table must contain exactly one row.
func TestProvision_Concurrent_DoesNotErrorOrDuplicate(t *testing.T) {
	t.Parallel()
	db := openServicesTestDB(t)

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

	// M14: verify credit_balance equals exactly one signup bonus — a
	// double-UPDATE would show a multiple of SignupBonusAmount here.
	var balance float64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("read credit_balance: %v", err)
	}
	if balance != SignupBonusAmount {
		t.Errorf("credit_balance: want %v, got %v", SignupBonusAmount, balance)
	}
}
