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

	"github.com/gosom/google-maps-scraper/models"
)

// openUserTestDB opens a DB connection from PG_TEST_DSN and skips the test
// if not set. Mirrors the helper used elsewhere in the package; defined
// locally to keep this file self-contained for the new S-C3 user tests.
// Registers db.Close via t.Cleanup so row-deletion cleanups registered after
// this call still see an open pool (LIFO ordering: Close runs last).
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
	t.Cleanup(func() { _ = db.Close() })
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

// TestGetByID_TierResolution covers the rate-limiter tier signal: a user with
// total_credits_purchased = 0 is free; any positive value flips them to paid.
// Refund-driven demotion is intentionally NOT tested here because the
// product spec says paid users stay paid for life — refunds reduce balance
// but don't reset total_credits_purchased back to zero.
func TestGetByID_TierResolution(t *testing.T) {
	t.Parallel()
	db := openUserTestDB(t)
	ctx := context.Background()
	repo := NewUserRepository(db)
	userID := seedTestUser(t, db) // total_credits_purchased defaults to 0

	// Default: no purchases → free tier.
	got, err := repo.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("initial GetByID failed: %v", err)
	}
	if got.Tier != models.UserTierFree {
		t.Errorf("brand-new user: expected Tier=%q, got %q", models.UserTierFree, got.Tier)
	}

	// Simulate a Stripe purchase by writing the column directly. The real path
	// runs through billing/service.go; this test isolates the repository.
	if _, err := db.ExecContext(ctx,
		`UPDATE users SET total_credits_purchased = 1.50 WHERE id = $1`, userID); err != nil {
		t.Fatalf("update total_credits_purchased: %v", err)
	}

	got, err = repo.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("post-purchase GetByID failed: %v", err)
	}
	if got.Tier != models.UserTierPaid {
		t.Errorf("after first purchase: expected Tier=%q, got %q", models.UserTierPaid, got.Tier)
	}
}

// TestGetByID_RefundDoesNotDemoteTier locks in the product invariant: once a
// user makes a successful Stripe payment they keep the paid tier for life,
// even if the payment is later refunded. The refund path in billing/service.go
// touches credit_balance and refund_deficit_credits, not
// total_credits_purchased — but a future bug there must NOT silently demote
// a paying customer back to the free rate limit. This test pins the
// monotonic-counter behaviour at the projection level.
func TestGetByID_RefundDoesNotDemoteTier(t *testing.T) {
	t.Parallel()
	db := openUserTestDB(t)
	ctx := context.Background()
	repo := NewUserRepository(db)
	userID := seedTestUser(t, db)

	// Simulate a successful purchase.
	if _, err := db.ExecContext(ctx,
		`UPDATE users SET total_credits_purchased = 5.00 WHERE id = $1`, userID); err != nil {
		t.Fatalf("seed purchase: %v", err)
	}
	got, err := repo.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("post-purchase GetByID: %v", err)
	}
	if got.Tier != models.UserTierPaid {
		t.Fatalf("setup: expected paid before refund, got %q", got.Tier)
	}

	// Simulate the refund: credit_balance drops, refund_deficit_credits rises,
	// total_credits_purchased is intentionally NOT touched (monotonic counter).
	if _, err := db.ExecContext(ctx,
		`UPDATE users SET credit_balance = 0, refund_deficit_credits = 5.00 WHERE id = $1`, userID); err != nil {
		t.Fatalf("seed refund: %v", err)
	}

	got, err = repo.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("post-refund GetByID: %v", err)
	}
	if got.Tier != models.UserTierPaid {
		t.Errorf("refund must not demote: expected Tier=%q, got %q", models.UserTierPaid, got.Tier)
	}
}

// TestGetByEmail_TierResolution mirrors TestGetByID_TierResolution against the
// GetByEmail path so both User-returning queries are covered by the same
// invariant.
func TestGetByEmail_TierResolution(t *testing.T) {
	t.Parallel()
	db := openUserTestDB(t)
	ctx := context.Background()
	repo := NewUserRepository(db)
	userID := seedTestUser(t, db)
	email := userID + "@test.invalid"

	got, err := repo.GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Tier != models.UserTierFree {
		t.Errorf("expected Tier=%q, got %q", models.UserTierFree, got.Tier)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE users SET total_credits_purchased = 9.99 WHERE id = $1`, userID); err != nil {
		t.Fatalf("update total_credits_purchased: %v", err)
	}

	got, err = repo.GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("post-purchase GetByEmail: %v", err)
	}
	if got.Tier != models.UserTierPaid {
		t.Errorf("expected Tier=%q, got %q", models.UserTierPaid, got.Tier)
	}
}

// TestCreate_IsIdempotent_OnDuplicateID locks in the contract that Create may
// be called multiple times with the same user ID without error and without
// overwriting the original row. Required so the Clerk webhook and the
// auth-middleware lazy-provisioning path can both call Create concurrently
// for the same brand-new user (the case that produced the "Failed to load
// dashboard" toast on first sign-up).
func TestCreate_IsIdempotent_OnDuplicateID(t *testing.T) {
	t.Parallel()
	db := openUserTestDB(t)
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
