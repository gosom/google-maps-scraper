package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gosom/google-maps-scraper/config"
	"github.com/stripe/stripe-go/v81"
	checkoutsession "github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/webhook"
)

type Service struct {
	db                *sql.DB
	cfg               *config.Service
	stripeSecretKey   string
	webhookSigningKey string
}

type CheckoutRequest struct {
	UserID   string
	Credits  int
	Currency string
}

type CheckoutResponse struct {
	SessionID string
	URL       string
}

func New(db *sql.DB, cfg *config.Service, stripeSecretKey, webhookSigningKey string) *Service {
	return &Service{db: db, cfg: cfg, stripeSecretKey: stripeSecretKey, webhookSigningKey: webhookSigningKey}
}

// CreateCheckoutSession creates a Stripe Checkout Session for purchasing credits.
func (s *Service) CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error) {
	if req.UserID == "" {
		return CheckoutResponse{}, fmt.Errorf("missing user id")
	}
	if req.Credits <= 0 {
		return CheckoutResponse{}, fmt.Errorf("credits must be > 0")
	}
	if req.Currency != "USD" && req.Currency != "EUR" {
		return CheckoutResponse{}, fmt.Errorf("unsupported currency")
	}

	// Fetch price per credit from currency_pricing
	var unitPriceCents int
	const priceQ = `SELECT unit_price_cents FROM currency_pricing WHERE currency=$1 AND is_active=TRUE`
	if err := s.db.QueryRowContext(ctx, priceQ, req.Currency).Scan(&unitPriceCents); err != nil {
		return CheckoutResponse{}, fmt.Errorf("failed to get pricing: %w", err)
	}

	// Prepare Stripe client
	stripe.Key = s.stripeSecretKey

	// Build success/cancel URLs from config (env overrides allowed)
	successURL, _ := s.cfg.GetString(ctx, "stripe_success_url", "https://example.com/success?session_id={CHECKOUT_SESSION_ID}")
	cancelURL, _ := s.cfg.GetString(ctx, "stripe_cancel_url", "https://example.com/cancel")

	// Use price_data with unit_amount = price per credit and quantity = credits
	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String(req.Currency),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String("Brezel Credits"),
					},
					UnitAmount: stripe.Int64(int64(unitPriceCents)),
				},
				Quantity: stripe.Int64(int64(req.Credits)),
			},
		},
		Metadata: map[string]string{
			"user_id":  req.UserID,
			"credits":  fmt.Sprintf("%d", req.Credits),
			"currency": req.Currency,
		},
	}

	sess, err := checkoutsession.New(params)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("failed to create checkout session: %w", err)
	}

	// Persist pending payment
	const ins = `INSERT INTO stripe_payments (user_id, stripe_checkout_session_id, amount_cents, currency, credits_purchased, status)
                 VALUES ($1, $2, $3, $4, $5, 'pending') ON CONFLICT (stripe_checkout_session_id) DO NOTHING`
	_, _ = s.db.ExecContext(ctx, ins, req.UserID, sess.ID, unitPriceCents*req.Credits, req.Currency, req.Credits)

	return CheckoutResponse{SessionID: sess.ID, URL: sess.URL}, nil
}

// HandleWebhook verifies and processes Stripe events, crediting user accounts on successful payment.
func (s *Service) HandleWebhook(ctx context.Context, payload []byte, signatureHeader string) (int, error) {
	if s.webhookSigningKey == "" {
		return 400, errors.New("webhook signing key not configured")
	}
	event, err := webhook.ConstructEvent(payload, signatureHeader, s.webhookSigningKey)
	if err != nil {
		return 400, fmt.Errorf("invalid signature: %w", err)
	}

	switch event.Type {
	case "checkout.session.completed":
		// Parse object into CheckoutSession
		var session stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
			return 400, fmt.Errorf("failed to parse session: %w", err)
		}
		// Get user/credits from DB row for this session if available
		userID := ""
		credits := 0
		currency := ""
		{
			const sel = `SELECT user_id, credits_purchased, currency FROM stripe_payments WHERE stripe_checkout_session_id=$1 LIMIT 1`
			_ = s.db.QueryRowContext(ctx, sel, session.ID).Scan(&userID, &credits, &currency)
		}
		// Fallback to metadata
		if userID == "" && session.Metadata != nil {
			userID = session.Metadata["user_id"]
			fmt.Sscan(session.Metadata["credits"], &credits)
			currency = session.Metadata["currency"]
		}
		if userID == "" || credits <= 0 || (currency != "USD" && currency != "EUR") {
			return 200, nil
		}
		// Credit the user in a transaction idempotently
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return 500, err
		}
		defer tx.Rollback()

		// Lock user row
		var balanceBefore int
		const selBal = `SELECT credit_balance FROM users WHERE id=$1 FOR UPDATE`
		if err := tx.QueryRowContext(ctx, selBal, userID).Scan(&balanceBefore); err != nil {
			return 500, err
		}

		balanceAfter := balanceBefore + credits

		// Update user balances
		const updUser = `UPDATE users SET credit_balance=$1, total_credits_purchased = total_credits_purchased + $2, updated_at=NOW() WHERE id=$3`
		if _, err := tx.ExecContext(ctx, updUser, balanceAfter, credits, userID); err != nil {
			return 500, err
		}

		// Insert credit transaction
		const insTxn = `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
                         VALUES ($1,'purchase',$2,$3,$4,$5,$6,'payment')`
		if _, err := tx.ExecContext(ctx, insTxn, userID, credits, balanceBefore, balanceAfter, "Stripe purchase", session.ID); err != nil {
			return 500, err
		}

		// Mark stripe payment as succeeded
		const updPay = `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`
		if _, err := tx.ExecContext(ctx, updPay, session.ID); err != nil {
			return 500, err
		}

		if err := tx.Commit(); err != nil {
			return 500, err
		}

		return 200, nil
	case "checkout.session.expired":
		var session stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
			return 400, fmt.Errorf("failed to parse session: %w", err)
		}
		const upd = `UPDATE stripe_payments SET status='canceled', updated_at=NOW() WHERE stripe_checkout_session_id=$1`
		_, _ = s.db.ExecContext(ctx, upd, session.ID)
		return 200, nil
	default:
		return 200, nil
	}
}

// ReconcileSession fetches a Checkout Session from Stripe and applies credits if paid.
func (s *Service) ReconcileSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("missing session id")
	}
	stripe.Key = s.stripeSecretKey
	sess, err := checkoutsession.Get(sessionID, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch session: %w", err)
	}
	// Fetch payment row
	var (
		userID   string
		credits  int
		currency string
		status   string
	)
	const sel = `SELECT user_id, credits_purchased, currency, status FROM stripe_payments WHERE stripe_checkout_session_id=$1 LIMIT 1`
	if err := s.db.QueryRowContext(ctx, sel, sessionID).Scan(&userID, &credits, &currency, &status); err != nil {
		return fmt.Errorf("payment row not found: %w", err)
	}
	if status == "succeeded" {
		return nil
	}
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return fmt.Errorf("session not paid")
	}
	// Apply credits transactionally
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var balanceBefore int
	if err := tx.QueryRowContext(ctx, `SELECT credit_balance FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&balanceBefore); err != nil {
		return err
	}
	balanceAfter := balanceBefore + credits
	if _, err := tx.ExecContext(ctx, `UPDATE users SET credit_balance=$1, total_credits_purchased = total_credits_purchased + $2, updated_at=NOW() WHERE id=$3`, balanceAfter, credits, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type) VALUES ($1,'purchase',$2,$3,$4,$5,$6,'payment')`, userID, credits, balanceBefore, balanceAfter, "Stripe purchase (reconcile)", sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}
