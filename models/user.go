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

// Tier constants for API rate-limit bucketing.
//
// A user is paid the moment total_credits_purchased first goes above zero
// (i.e. the first successful Stripe payment). Refunds do NOT demote a paid
// user back to free: once they've paid, they keep the higher quota for the
// life of their account. Signup bonuses and other non-Stripe credit grants
// keep the user on the free tier.
//
// Tier is computed by the postgres layer on every User read; it is not a
// persisted column and not part of the JSON API surface.
const (
	UserTierFree = "free"
	UserTierPaid = "paid"
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

	// Tier is the user's billing tier (UserTierFree or UserTierPaid).
	// Computed from users.total_credits_purchased > 0 on every DB read;
	// not stored as its own column and not exposed in API responses (the
	// `json:"-"` tag keeps it server-side). Consumed by the API rate
	// limiter to grant paid customers their higher quota.
	//
	// Race note: the underlying column is updated by a Stripe webhook
	// handler with no FOR UPDATE on the read path here. A request that
	// arrives mid-transaction will see the pre-commit snapshot (Tier=free)
	// for one or two more requests until the webhook commits. That's a
	// brief tightening of rate limits on the first paid request burst —
	// acceptable, NOT a bug to "fix" with a SHARE lock. The webhook also
	// only ever increments (never decrements), so once Tier=paid for a
	// user, it stays paid.
	Tier string `json:"-"`
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
