package models

import (
	"context"
	"time"
)

// Role constants for RBAC.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// User represents a registered user in the system
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// StripeCustomerID is populated after the first Stripe Customer is created
	// for this user (lazily on signup or on first checkout). Nil for legacy
	// users until their next checkout triggers lazy creation. Excluded from
	// JSON responses — Stripe identifiers must never leak to API clients.
	StripeCustomerID *string `json:"-"`
	// RefundDeficitCredits is the uncollectable portion of a past Stripe
	// refund. Set when a charge.refunded event exceeds the user's spendable
	// balance because credits were already consumed. The next purchase pays
	// down this deficit before crediting spendable balance. Visible to API
	// clients so they can see what they owe (unlike StripeCustomerID). Stored
	// as NUMERIC(18,6) in Postgres; scanned into float64 here for
	// consistency with credit_balance read paths.
	RefundDeficitCredits float64   `json:"refund_deficit_credits"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// UserRepository manages user operations
type UserRepository interface {
	GetByID(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	Create(ctx context.Context, user *User) error
	// SetStripeCustomerID writes the Stripe Customer ID to the user row.
	// Implementations MUST refuse to overwrite an already-set value with a
	// DIFFERENT one (this would be a billing-integrity breach: the user's
	// charge history would split across two Stripe Customers). Writing the
	// same value is a tolerated no-op for idempotent retries.
	SetStripeCustomerID(ctx context.Context, userID, stripeCustomerID string) error
	Delete(ctx context.Context, id string) error
}
