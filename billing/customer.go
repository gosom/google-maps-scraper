package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/stripe/stripe-go/v82"
	stripecustomer "github.com/stripe/stripe-go/v82/customer"
)

// customerRepo is the minimal dependency EnsureStripeCustomer needs to
// persist the stripe_customer_id. Satisfied by models.UserRepository.
// Declared here as a narrow interface (rather than depending on the full
// UserRepository) so callers can pass test doubles without implementing
// every user operation.
type customerRepo interface {
	SetStripeCustomerID(ctx context.Context, userID, stripeCustomerID string) error
}

// EnsureStripeCustomer creates a Stripe Customer for the given internal user
// if one does not exist, and persists the resulting cus_... via the repo.
//
// Safe to call multiple times for the same user: when existingCustomerID is
// non-empty the function returns it unchanged without hitting Stripe (the
// fast path used by CreateCheckoutSession on every checkout). The
// idempotency key scoped to the user ID further protects the slow path
// from creating duplicate Customers on network retries within Stripe's
// 24-hour idempotency window.
//
// Failure modes (logged at ERROR, returned to caller):
//   - missing userID: validation error, no Stripe call
//   - Stripe API error: returned wrapped, no repo write attempted
//   - repo persist failure: Stripe-side Customer exists but the local link
//     is missing. The next call will create ANOTHER Customer (acceptable —
//     duplicates are recoverable in the Stripe Dashboard, blocked checkouts
//     are not). The orphan is logged with both the userID and the cus_...
//     so ops can manually reconcile.
func (s *Service) EnsureStripeCustomer(
	ctx context.Context,
	userID, email, existingCustomerID string,
	repo customerRepo,
) (string, error) {
	if userID == "" {
		return "", errors.New("missing user id")
	}
	if existingCustomerID != "" {
		return existingCustomerID, nil
	}
	if repo == nil {
		return "", errors.New("missing customer repo")
	}

	params := &stripe.CustomerParams{
		Metadata: map[string]string{
			"brezel_user_id": userID,
		},
	}
	if email != "" {
		params.Email = stripe.String(email)
	}
	// Idempotency key scoped to the user. Stripe retries within 24h return
	// the same Customer object rather than creating duplicates. Required by
	// Stripe's API guidelines for all POST requests.
	params.Params.IdempotencyKey = stripe.String("customer:create:" + userID)

	cust, err := stripecustomer.New(params)
	if err != nil {
		s.logger.Error("stripe_customer_create_failed",
			slog.String("user_id", userID),
			slog.Any("error", err),
		)
		return "", fmt.Errorf("failed to create stripe customer: %w", err)
	}

	if err := repo.SetStripeCustomerID(ctx, userID, cust.ID); err != nil {
		// Stripe-side Customer exists but we failed to persist the link.
		// Log the orphan for manual reconciliation; the next call will
		// create ANOTHER Customer (acceptable — duplicates are recoverable
		// in the Stripe Dashboard, an unable-to-checkout user is not).
		s.logger.Error("stripe_customer_persist_failed",
			slog.String("user_id", userID),
			slog.String("stripe_customer_id", cust.ID),
			slog.Any("error", err),
		)
		return "", fmt.Errorf("failed to persist stripe customer id: %w", err)
	}

	s.logger.Info("stripe_customer_created",
		slog.String("user_id", userID),
		slog.String("stripe_customer_id", cust.ID),
	)
	return cust.ID, nil
}

// Compile-time assertion: models.UserRepository satisfies customerRepo so
// that callers can pass a postgres.NewUserRepository(...) directly.
var _ customerRepo = (models.UserRepository)(nil)
