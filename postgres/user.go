package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

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

// GetByID retrieves a user by ID
func (repo *userRepository) GetByID(ctx context.Context, id string) (User, error) {
	const q = `SELECT id, email, role, stripe_customer_id, created_at, updated_at FROM users WHERE id = $1`

	row := repo.db.QueryRowContext(ctx, q, id)

	var user User
	err := row.Scan(&user.ID, &user.Email, &user.Role, &user.StripeCustomerID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, errors.New("user not found")
		}
		return User{}, err
	}

	return user, nil
}

// GetByEmail retrieves a user by email
func (repo *userRepository) GetByEmail(ctx context.Context, email string) (User, error) {
	const q = `SELECT id, email, role, stripe_customer_id, created_at, updated_at FROM users WHERE email = $1`

	row := repo.db.QueryRowContext(ctx, q, email)

	var user User
	err := row.Scan(&user.ID, &user.Email, &user.Role, &user.StripeCustomerID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, errors.New("user not found")
		}
		return User{}, err
	}

	return user, nil
}

// Create inserts a new user. The role column is intentionally omitted so new
// users always receive the DB default ('user'). Admin promotion requires direct
// DB access via scripts/promote_admin.sh — there is no API for self-promotion.
func (repo *userRepository) Create(ctx context.Context, user *User) error {
	const q = `INSERT INTO users (id, email, created_at, updated_at)
	           VALUES ($1, $2, $3, $4)`

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
		return errors.New("user not found or stripe_customer_id already set to a different value")
	}
	return nil
}

// Delete removes a user
func (repo *userRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id = $1`
	_, err := repo.db.ExecContext(ctx, q, id)
	return err
}
