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
	Credits  string
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
	if req.Credits == "" || req.Credits == "0" {
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

	// For Stripe MVP: accept only whole credits as quantity
	var creditsInt int
	if _, err := fmt.Sscan(req.Credits, &creditsInt); err != nil || creditsInt <= 0 {
		return CheckoutResponse{}, fmt.Errorf("only whole credits supported for checkout in MVP")
	}

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
				Quantity: stripe.Int64(int64(creditsInt)),
			},
		},
		Metadata: map[string]string{
			"user_id":  req.UserID,
			"credits":  fmt.Sprintf("%d", creditsInt),
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
	_, _ = s.db.ExecContext(ctx, ins, req.UserID, sess.ID, unitPriceCents*creditsInt, req.Currency, creditsInt)

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

		// Lock user row and update using NUMERIC arithmetic
		const updUser = `UPDATE users SET credit_balance = credit_balance + $1::numeric, total_credits_purchased = total_credits_purchased + $1::numeric, updated_at=NOW() WHERE id=$2`
		if _, err := tx.ExecContext(ctx, updUser, credits, userID); err != nil {
			return 500, err
		}

		// Insert credit transaction with computed balances
		const insTxn = `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
                         VALUES ($1,'purchase',$2,
                                 (SELECT credit_balance - $2::numeric FROM users WHERE id=$1),
                                 (SELECT credit_balance FROM users WHERE id=$1),
                                 $3,$4,'payment')`
		if _, err := tx.ExecContext(ctx, insTxn, userID, credits, "Stripe purchase", session.ID); err != nil {
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

	// Update balances using SQL arithmetic
	if _, err := tx.ExecContext(ctx, `UPDATE users SET credit_balance = credit_balance + $1::numeric, total_credits_purchased = total_credits_purchased + $1::numeric, updated_at=NOW() WHERE id=$2`, credits, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type) VALUES ($1,'purchase',$2,(SELECT credit_balance - $2::numeric FROM users WHERE id=$1),(SELECT credit_balance FROM users WHERE id=$1),$3,$4,'payment')`, userID, credits, "Stripe purchase (reconcile)", sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// ChargeEvent inserts a billing event and atomically deducts credits based on resolved pricing.
// It enforces non-negative balances and uses idempotency via metadata.idempotency_key.
func (s *Service) ChargeEvent(ctx context.Context, userID, jobID, eventType string, quantity int, idempotencyKey string, metadata map[string]any) error {
	if s.db == nil {
		return fmt.Errorf("db not configured")
	}
	if userID == "" || jobID == "" || eventType == "" || quantity <= 0 {
		return fmt.Errorf("invalid charge params")
	}
	// Merge idempotency key into metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	if idempotencyKey != "" {
		metadata["idempotency_key"] = idempotencyKey
	}
	metaJSON, _ := json.Marshal(metadata)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert billing event; trigger resolves pricing and totals
	var (
		eventID               string
		unitPrice, totalPrice string // scan as text to preserve precision
	)
	const insEvent = `INSERT INTO billing_events (user_id, job_id, event_type_code, quantity, metadata)
	                  VALUES ($1,$2,$3,$4,$5::jsonb)
	                  RETURNING id, unit_price_credits::text, total_price_credits::text`
	if err := tx.QueryRowContext(ctx, insEvent, userID, jobID, eventType, quantity, string(metaJSON)).Scan(&eventID, &unitPrice, &totalPrice); err != nil {
		return fmt.Errorf("insert billing event: %w", err)
	}

	// Decrement user balance atomically, ensuring non-negative balance
	// If insufficient credits, no row is updated -> error
	const decBal = `UPDATE users
		SET credit_balance = credit_balance - $1::numeric,
		    total_credits_consumed = total_credits_consumed + $1::numeric,
		    updated_at = NOW()
		WHERE id = $2 AND credit_balance >= $1::numeric
		RETURNING credit_balance::text`
	var newBalance string
	if err := tx.QueryRowContext(ctx, decBal, totalPrice, userID).Scan(&newBalance); err != nil {
		return fmt.Errorf("insufficient credits or update failed: %w", err)
	}

	// Insert credit transaction (consumption), linking to billing event via metadata reference_id
	const insTxn = `INSERT INTO credit_transactions (
		user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type, metadata
	) VALUES (
		$1,'consumption', -$2::numeric,
		(SELECT ($3::numeric) + $2::numeric),
		$3::numeric,
		$4, $5, 'job', jsonb_build_object('billing_event_id',$6)
	)`
	if _, err := tx.ExecContext(ctx, insTxn, userID, totalPrice, newBalance, fmt.Sprintf("Billing charge: %s", eventType), jobID, eventID); err != nil {
		return fmt.Errorf("insert credit transaction: %w", err)
	}

	return tx.Commit()
}

// ChargeActorStart charges a flat actor_start event (quantity=1) for a job.
func (s *Service) ChargeActorStart(ctx context.Context, userID, jobID string) error {
	return s.ChargeEvent(ctx, userID, jobID, "actor_start", 1, "job:"+jobID+":actor_start", map[string]any{})
}

// ChargePlaces charges place_scraped for N places for a job.
func (s *Service) ChargePlaces(ctx context.Context, userID, jobID string, places int) error {
	if places <= 0 {
		return nil
	}
	return s.ChargeEvent(ctx, userID, jobID, "place_scraped", places, "job:"+jobID+":place_scraped", map[string]any{})
}
