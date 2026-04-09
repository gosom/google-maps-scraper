package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openUserTestDB opens a DB connection from PG_TEST_DSN and skips the test
// if not set. Mirrors the helper used elsewhere in the package; defined
// locally to keep this file self-contained for the new S-C3 user tests.
func openUserTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("Skipping: PG_TEST_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("failed to ping db: %v", err)
	}
	return db
}

// seedTestUser inserts a fresh user row with NULL stripe_customer_id and
// schedules cleanup. Returns the generated userID.
func seedTestUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	userID := "user_test_sc3_" + suffix
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, credit_balance, created_at, updated_at)
		 VALUES ($1, $2, 0, NOW(), NOW())`,
		userID, userID+"@test.invalid")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return userID
}

func TestSetStripeCustomerID_FirstWrite(t *testing.T) {
	db := openUserTestDB(t)
	defer db.Close()
	ctx := context.Background()

	repo := NewUserRepository(db).(*userRepository)
	userID := seedTestUser(t, db)
	stripeCustomerID := "cus_test_first_" + fmt.Sprintf("%d", time.Now().UnixNano())

	if err := repo.SetStripeCustomerID(ctx, userID, stripeCustomerID); err != nil {
		t.Fatalf("SetStripeCustomerID failed: %v", err)
	}

	var got sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM users WHERE id = $1`, userID).Scan(&got); err != nil {
		t.Fatalf("verify query: %v", err)
	}
	if !got.Valid || got.String != stripeCustomerID {
		t.Errorf("expected stripe_customer_id=%q, got %v", stripeCustomerID, got)
	}
}

func TestSetStripeCustomerID_SameValueIsNoOp(t *testing.T) {
	db := openUserTestDB(t)
	defer db.Close()
	ctx := context.Background()

	repo := NewUserRepository(db).(*userRepository)
	userID := seedTestUser(t, db)
	stripeCustomerID := "cus_test_same_" + fmt.Sprintf("%d", time.Now().UnixNano())

	if err := repo.SetStripeCustomerID(ctx, userID, stripeCustomerID); err != nil {
		t.Fatalf("first SetStripeCustomerID failed: %v", err)
	}
	// Replaying the same value should succeed (idempotent retry).
	if err := repo.SetStripeCustomerID(ctx, userID, stripeCustomerID); err != nil {
		t.Errorf("second SetStripeCustomerID with same value should be a no-op, got: %v", err)
	}
}

func TestSetStripeCustomerID_RefusesDifferentValue(t *testing.T) {
	db := openUserTestDB(t)
	defer db.Close()
	ctx := context.Background()

	repo := NewUserRepository(db).(*userRepository)
	userID := seedTestUser(t, db)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	first := "cus_test_first_" + suffix
	second := "cus_test_second_" + suffix

	if err := repo.SetStripeCustomerID(ctx, userID, first); err != nil {
		t.Fatalf("first SetStripeCustomerID failed: %v", err)
	}
	err := repo.SetStripeCustomerID(ctx, userID, second)
	if err == nil {
		t.Fatal("expected error overwriting with a different stripe_customer_id, got nil")
	}
	if !strings.Contains(err.Error(), "already set to a different value") {
		t.Errorf("expected error to mention 'already set to a different value', got: %v", err)
	}

	// Verify the original value is still in place — defense in depth.
	var got sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM users WHERE id = $1`, userID).Scan(&got); err != nil {
		t.Fatalf("verify query: %v", err)
	}
	if !got.Valid || got.String != first {
		t.Errorf("expected stripe_customer_id to remain %q, got %v", first, got)
	}
}

func TestSetStripeCustomerID_RejectsEmpty(t *testing.T) {
	db := openUserTestDB(t)
	defer db.Close()
	ctx := context.Background()

	repo := NewUserRepository(db).(*userRepository)
	userID := seedTestUser(t, db)

	err := repo.SetStripeCustomerID(ctx, userID, "")
	if err == nil {
		t.Fatal("expected error for empty stripeCustomerID, got nil")
	}
}

func TestGetByID_IncludesStripeCustomerID(t *testing.T) {
	db := openUserTestDB(t)
	defer db.Close()
	ctx := context.Background()

	repo := NewUserRepository(db)
	userID := seedTestUser(t, db)
	stripeCustomerID := "cus_test_getbyid_" + fmt.Sprintf("%d", time.Now().UnixNano())

	if err := repo.SetStripeCustomerID(ctx, userID, stripeCustomerID); err != nil {
		t.Fatalf("SetStripeCustomerID failed: %v", err)
	}

	user, err := repo.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if user.StripeCustomerID == nil {
		t.Fatal("expected StripeCustomerID to be non-nil")
	}
	if *user.StripeCustomerID != stripeCustomerID {
		t.Errorf("expected StripeCustomerID=%q, got %q", stripeCustomerID, *user.StripeCustomerID)
	}
}
