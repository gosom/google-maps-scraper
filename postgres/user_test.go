package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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
	if !errors.Is(err, ErrStripeCustomerIDConflict) {
		t.Errorf("expected ErrStripeCustomerIDConflict, got: %v", err)
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

func TestSetStripeCustomerID_RejectsMissingUser(t *testing.T) {
	db := openUserTestDB(t)
	defer db.Close()
	ctx := context.Background()

	repo := NewUserRepository(db).(*userRepository)
	missingUserID := "user_test_missing_" + fmt.Sprintf("%d", time.Now().UnixNano())

	err := repo.SetStripeCustomerID(ctx, missingUserID, "cus_test_missing")
	if err == nil {
		t.Fatal("expected error for missing user, got nil")
	}
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got: %v", err)
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

// TestCreate_IsIdempotent_OnDuplicateID locks in the contract that Create may
// be called multiple times with the same user ID without error and without
// overwriting the original row. Required so the Clerk webhook and the
// auth-middleware lazy-provisioning path can both call Create concurrently
// for the same brand-new user (the case that produced the "Failed to load
// dashboard" toast on first sign-up).
func TestCreate_IsIdempotent_OnDuplicateID(t *testing.T) {
	db := openUserTestDB(t)
	defer db.Close()
	ctx := context.Background()

	repo := NewUserRepository(db)
	userID := "user_test_idem_" + fmt.Sprintf("%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	first := User{ID: userID, Email: userID + "@test.invalid"}
	if err := repo.Create(ctx, &first); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Second call with the same ID must succeed (no error, no panic).
	second := User{ID: userID, Email: "different-email@test.invalid"}
	if err := repo.Create(ctx, &second); err != nil {
		t.Fatalf("second Create (must be idempotent): %v", err)
	}

	// ON CONFLICT DO NOTHING semantics: original row preserved, NOT updated.
	got, err := repo.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Email != first.Email {
		t.Errorf("email overwritten: want %q, got %q (DO NOTHING must preserve)", first.Email, got.Email)
	}
}
