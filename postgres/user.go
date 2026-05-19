package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// ErrUserNotFound is returned by GetByID, GetByEmail, and the disambiguation
// path of SetStripeCustomerID when no matching row exists. Callers MUST use
// errors.Is to distinguish "row does not exist" from transient DB failures;
// treating all errors as not-found risks triggering auto-provision on
// connection resets or query timeouts.
var ErrUserNotFound = errors.New("user not found")

// ErrStripeCustomerIDConflict is returned by SetStripeCustomerID when the
// user row already has a different stripe_customer_id. The existing and
// requested IDs are high-cardinality values; they are NOT interpolated into
// this sentinel so that APM/log aggregators can group all occurrences under
// one fingerprint. Log both IDs as structured fields at the call site.
var ErrStripeCustomerIDConflict = errors.New("stripe_customer_id already set to a different value")

// User is now an alias to the models.User struct
type User = models.User

// UserRepository is now an alias to the models.UserRepository interface
type UserRepository = models.UserRepository

// userRepository implements the UserRepository interface
type userRepository struct {
	db *sql.DB
}

// NewUserRepository creates a new UserRepository
func NewUserRepository(db *sql.DB) UserRepository {
	return &userRepository{db: db}
}

// userSelectColumns is the canonical projection used by every User-returning
// query in this repository. Kept in one place so adding a column on the User
// struct only requires updating one constant + the matching Scan helper.
//
// `(total_credits_purchased > 0)` is a server-side boolean that drives the
// User.Tier computation in scanUser. Comparing NUMERIC(18,6) values is exact
// (Postgres NUMERIC is decimal, not float), and the column is NOT NULL with
// a CHECK (>= 0) constraint (see migration 000012), so no COALESCE needed.
// The boolean roundtrip avoids shipping the raw amount across the wire just
// to compare it to zero.
const userSelectColumns = `id, email, role, stripe_customer_id,
	COALESCE(refund_deficit_credits, 0)::float8,
	created_at, updated_at,
	(total_credits_purchased > 0)`

// rowScanner is the subset of *sql.Row / *sql.Rows we need; lets scanUser work
// with both single-row and multi-row queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanUser scans one row produced by a SELECT with userSelectColumns into a
// fully-populated User, computing Tier from the trailing boolean column.
// Returns ErrUserNotFound when the underlying row is empty so callers don't
// have to re-implement the sentinel translation.
func scanUser(row rowScanner) (User, error) {
	var (
		user         User
		hasPurchased bool
	)
	err := row.Scan(
		&user.ID, &user.Email, &user.Role, &user.StripeCustomerID,
		&user.RefundDeficitCredits, &user.CreatedAt, &user.UpdatedAt,
		&hasPurchased,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	if hasPurchased {
		user.Tier = models.UserTierPaid
	} else {
		user.Tier = models.UserTierFree
	}
	return user, nil
}

// GetByID retrieves a user by ID
func (repo *userRepository) GetByID(ctx context.Context, id string) (User, error) {
	const q = `SELECT ` + userSelectColumns + ` FROM users WHERE id = $1`
	return scanUser(repo.db.QueryRowContext(ctx, q, id))
}

// GetByEmail retrieves a user by email
func (repo *userRepository) GetByEmail(ctx context.Context, email string) (User, error) {
	const q = `SELECT ` + userSelectColumns + ` FROM users WHERE email = $1`
	return scanUser(repo.db.QueryRowContext(ctx, q, email))
}

// Create inserts a new user. The role column is intentionally omitted so new
// users always receive the DB default ('user'). Admin promotion requires direct
// DB access via scripts/promote_admin.sh — there is no API for self-promotion.
func (repo *userRepository) Create(ctx context.Context, user *User) error {
	// ON CONFLICT DO NOTHING (no target column) suppresses both users_pkey AND
	// users_email_key violations. With ON CONFLICT (id) only, a concurrent
	// race between two callers inserting the same (id, email) could surface
	// the email-uniqueness violation first — Postgres reports whichever
	// constraint it checks first. Both call sites (Clerk webhook + auth
	// middleware lazy fallback) derive id and email from the same Clerk user
	// object, so a "different id, same email" insert would signal an upstream
	// bug rather than a legitimate request; no-opping is the right behavior.
	// The post-Create GetByID in services.UserProvisioning.Provision fetches
	// the canonical row regardless of which goroutine actually inserted.
	//
	// WARNING: untargeted ON CONFLICT — adding any new UNIQUE constraint to the
	// users table requires reviewing every Create call site to ensure silent
	// swallowing of the new constraint is still the intended behaviour.
	const q = `INSERT INTO users (id, email, created_at, updated_at)
	           VALUES ($1, $2, $3, $4)
	           ON CONFLICT DO NOTHING`

	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}

	_, err := repo.db.ExecContext(ctx, q, user.ID, user.Email, user.CreatedAt, user.UpdatedAt)
	return err
}

// SetStripeCustomerID writes the Stripe customer ID to the user row.
//
// Uses a guarded UPDATE so a racing write cannot replace an already-set
// customer ID with a DIFFERENT one — that would be a billing-integrity
// breach (the user's charges would split across two Stripe Customers and
// future refund lookups would always miss). Writing the same value is a
// tolerated no-op so concurrent EnsureStripeCustomer callers can both
// succeed without coordination.
//
// updated_at is intentionally NOT touched here: the table-level trigger
// already bumps it on every UPDATE.
//
// Errors are disambiguated when the UPDATE affects 0 rows so ops can tell
// "user does not exist" from "row exists but customer_id is already set
// to a different value" — these are very different failure modes during
// incident investigation.
func (repo *userRepository) SetStripeCustomerID(ctx context.Context, userID, stripeCustomerID string) error {
	if stripeCustomerID == "" {
		return errors.New("stripeCustomerID cannot be empty")
	}
	const q = `UPDATE users
		SET stripe_customer_id = $1
		WHERE id = $2 AND (stripe_customer_id IS NULL OR stripe_customer_id = $1)`
	res, err := repo.db.ExecContext(ctx, q, stripeCustomerID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Disambiguate: did the user not exist, or did the row exist with
		// a different stripe_customer_id? A second SELECT on the cold error
		// path is acceptable for clearer ops diagnostics.
		var existing sql.NullString
		err := repo.db.QueryRowContext(ctx,
			`SELECT stripe_customer_id FROM users WHERE id = $1`, userID).Scan(&existing)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrUserNotFound
			}
			return fmt.Errorf("failed to disambiguate SetStripeCustomerID failure: %w", err)
		}
		// Row exists. Either the customer_id is already set to a different
		// value (the conflict we want to surface), or it matches the requested
		// value (which the WHERE clause should have allowed — if we hit this
		// branch it means a concurrent write changed the value out from
		// under us, which is itself a conflict).
		if existing.Valid {
			return ErrStripeCustomerIDConflict
		}
		// Defensive: row has NULL stripe_customer_id but our UPDATE missed.
		// Shouldn't happen given the WHERE clause; treat as a transient error.
		return errors.New("SetStripeCustomerID UPDATE missed a row with NULL customer_id (transient race?)")
	}
	return nil
}

// Delete removes a user
func (repo *userRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id = $1`
	_, err := repo.db.ExecContext(ctx, q, id)
	return err
}
