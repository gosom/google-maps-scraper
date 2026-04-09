package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/pkg/metrics"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v82"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

type Service struct {
	db                *sql.DB
	cfg               *config.Service
	webhookSigningKey string
	// userRepo is required by CreateCheckoutSession to look up the user's
	// stripe_customer_id (and lazy-create it via EnsureStripeCustomer if
	// missing). nil-safe: tests that don't exercise the checkout path can
	// leave it unset, but CreateCheckoutSession will return a clean error.
	userRepo models.UserRepository
	logger   *slog.Logger
	metrics  *metrics.BillingMetrics
}

type CheckoutRequest struct {
	UserID   string
	Credits  string
	Currency string
}

type CheckoutResponse struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
}

// New constructs a billing.Service. userRepo is required for the checkout
// path (it powers the stripe_customer_id lookup); pass nil only when
// constructing a Service for non-checkout flows like background event
// charging in webrunner. CreateCheckoutSession returns a clean error if
// userRepo is nil rather than panicking on a nil dereference.
func New(db *sql.DB, cfg *config.Service, stripeSecretKey, webhookSigningKey string, userRepo models.UserRepository) *Service {
	// Set the Stripe API key once at startup to avoid a data race from
	// concurrent goroutines writing the package-level global on every request.
	// Guard: only set when non-empty so a second billing.New("") used for
	// non-Stripe event charging does not clobber a previously set key.
	if stripeSecretKey != "" {
		stripe.Key = stripeSecretKey
	}

	return &Service{
		db:                db,
		cfg:               cfg,
		webhookSigningKey: webhookSigningKey,
		userRepo:          userRepo,
		logger:            pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "billing"),
		metrics:           metrics.NewBillingMetrics(nil), // uses default Prometheus registry
	}
}

// buildCheckoutSessionParams constructs the *stripe.CheckoutSessionParams for
// CreateCheckoutSession. Extracted as a pure builder so the field assignments
// (especially the S-H4 ClientReferenceID and PaymentIntentData.Metadata) can
// be unit-tested without requiring a Stripe mock.
//
// Stripe propagation rules (verified against docs.stripe.com/metadata):
//   - top-level Metadata stays on the Checkout Session object only
//   - PaymentIntentData.Metadata is stored on the underlying PaymentIntent at
//     creation, and from there is snapshot-copied to the Charge object — so
//     fields here are visible on charge.refunded AND charge.dispute.created
//     webhook event payloads
//   - ClientReferenceID is NOT metadata; it's a top-level reconciliation
//     reference (max 200 chars) surfaced in the Stripe Dashboard
func buildCheckoutSessionParams(
	req CheckoutRequest,
	stripeCustomerID string,
	creditsInt, unitPriceCents int,
	successURL, cancelURL string,
) *stripe.CheckoutSessionParams {
	return &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
		// Customer links the session, payment intent, and charge to a single
		// durable Stripe Customer record (S-C3).
		Customer: stripe.String(stripeCustomerID),
		// ClientReferenceID surfaces the internal user ID in the Stripe
		// Dashboard for search/reconciliation. Max 200 chars per Stripe docs;
		// Clerk user IDs are well under this limit. (S-H4)
		ClientReferenceID: stripe.String(req.UserID),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
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
		// Top-level Metadata stays on the Checkout Session object only — it
		// does NOT propagate to the resulting PaymentIntent or Charge per
		// Stripe's metadata propagation rules. handleCheckoutSessionCompleted
		// still reads session.Metadata as a defense against the (rare) case
		// where the stripe_payments DB row is missing.
		Metadata: map[string]string{
			"user_id":  req.UserID,
			"credits":  fmt.Sprintf("%d", creditsInt),
			"currency": req.Currency,
		},
		// PaymentIntentData.Metadata DOES propagate downstream:
		// Session → PaymentIntent → Charge (one-time snapshot at PI creation).
		// Stripe docs: "Data you specify with the payment_intent_data.metadata
		// attribute is stored in the metadata of the underlying PaymentIntent."
		// And: "When a PaymentIntent creates a payment, the metadata is copied
		// to the charge in a one-time snapshot."
		// This means brezel_user_id is available on the Charge object delivered
		// by both charge.refunded AND charge.dispute.created webhooks, enabling
		// direct user lookups without joining through stripe_payments. (S-H4)
		PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{
			Description: stripe.String(fmt.Sprintf("Brezel Credits x%d", creditsInt)),
			Metadata: map[string]string{
				"brezel_user_id": req.UserID,
				"credits":        fmt.Sprintf("%d", creditsInt),
			},
		},
	}
}

// checkoutIdempotencyKey builds the Stripe idempotency key for a checkout
// session. Scoped to (user, credits, currency, hour-bucket) so the 24h
// Stripe dedup window collapses retries-within-an-hour to the same
// session and lets retries-across-hours create a fresh one. Pure function
// to enable unit testing. (S-H1)
func checkoutIdempotencyKey(userID string, credits int, currency string, now time.Time) string {
	bucket := now.Truncate(time.Hour).Unix()
	return fmt.Sprintf("checkout:%s:%d:%s:%d", userID, credits, currency, bucket)
}

// CreateCheckoutSession creates a Stripe Checkout Session for purchasing credits.
func (s *Service) CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error) {
	if req.UserID == "" {
		return CheckoutResponse{}, fmt.Errorf("missing user id")
	}
	// MVP: USD-only
	if req.Currency != "USD" {
		return CheckoutResponse{}, fmt.Errorf("unsupported currency; only USD is enabled in MVP")
	}

	creditsInt, err := parseCreditsStrict(req.Credits)
	if err != nil {
		return CheckoutResponse{}, err
	}

	// Ensure the user has a Stripe Customer (lazy-create for legacy users
	// who signed up before S-C3 added signup-time provisioning). Passing
	// Customer on the CheckoutSessionParams links the session, payment
	// intent, and charge to a single durable Customer record. Without this,
	// every checkout creates a fresh guest Customer in Stripe and the
	// refund handler's fallback lookup by stripe_customer_id always misses.
	if s.userRepo == nil {
		return CheckoutResponse{}, errors.New("user repository not configured")
	}
	user, err := s.userRepo.GetByID(ctx, req.UserID)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("failed to look up user: %w", err)
	}
	var existingCustomerID string
	if user.StripeCustomerID != nil {
		existingCustomerID = *user.StripeCustomerID
	}
	stripeCustomerID, err := s.EnsureStripeCustomer(ctx, req.UserID, user.Email, existingCustomerID, s.userRepo)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("failed to ensure stripe customer: %w", err)
	}

	// MVP: fixed $1 per credit
	unitPriceCents := 100

	// Build success/cancel URLs from config (env overrides allowed)
	successURL, _ := s.cfg.GetString(ctx, "stripe_success_url", "https://example.com/success")
	cancelURL, _ := s.cfg.GetString(ctx, "stripe_cancel_url", "https://example.com/cancel")

	params := buildCheckoutSessionParams(req, stripeCustomerID, creditsInt, unitPriceCents, successURL, cancelURL)

	// Idempotency key scoped to (user, credits, currency, hour). Stripe's
	// 24h dedup window means repeated attempts within an hour collapse to
	// the same Stripe session — which prevents network retries / SDK retries
	// / double-clicks from creating duplicate sessions and duplicate
	// stripe_payments rows. Crossing an hour boundary creates a fresh
	// session so a user who closed the tab can retry without being stuck on
	// a stale checkout URL. (S-H1)
	//
	// Stripe docs: https://docs.stripe.com/error-low-level#idempotency
	//   "Use [idempotency keys] for all POST requests to the Stripe API."
	params.Params.IdempotencyKey = stripe.String(
		checkoutIdempotencyKey(req.UserID, creditsInt, req.Currency, time.Now().UTC()),
	)

	sess, err := checkoutsession.New(params)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("failed to create checkout session: %w", err)
	}

	// Persist pending payment
	const ins = `INSERT INTO stripe_payments (id, user_id, stripe_checkout_session_id, amount_cents, currency, credits_purchased, status)
                 VALUES ($1, $2, $3, $4, $5, $6, 'pending') ON CONFLICT (stripe_checkout_session_id) DO NOTHING`
	if _, err := s.db.ExecContext(ctx, ins, uuid.Must(uuid.NewV7()).String(), req.UserID, sess.ID, unitPriceCents*creditsInt, req.Currency, creditsInt); err != nil {
		s.logger.Error("failed_to_persist_pending_payment",
			slog.String("user_id", req.UserID),
			slog.String("session_id", sess.ID),
			slog.Any("error", err),
		)
	}

	return CheckoutResponse{SessionID: sess.ID, URL: sess.URL}, nil
}

func (s *Service) HandleWebhook(ctx context.Context, payload []byte, signatureHeader string) (int, error) {
	if s.webhookSigningKey == "" {
		s.logger.Error("webhook_signing_key_not_configured")
		return 400, errors.New("webhook signing key not configured")
	}

	event, err := webhook.ConstructEvent(payload, signatureHeader, s.webhookSigningKey)
	if err != nil {
		s.logger.Error("invalid_webhook_signature", slog.Any("error", err))
		return 400, fmt.Errorf("invalid signature: %w", err)
	}

	s.logger.Info("webhook_signature_verified", slog.String("event_type", string(event.Type)), slog.String("event_id", event.ID))

	switch event.Type {
	case "checkout.session.completed":
		return s.handleCheckoutSessionCompleted(ctx, event)
	case "checkout.session.expired":
		return s.handleCheckoutSessionExpired(ctx, event)
	case "charge.refunded":
		return s.handleChargeRefunded(ctx, event)
	case "charge.failed":
		return s.handleChargeFailed(ctx, event)
	case "charge.dispute.created":
		return s.handleChargeDisputeCreated(ctx, event)
	case "refund.updated":
		return s.handleRefundUpdated(ctx, event)
	case "checkout.session.async_payment_succeeded":
		return s.handleCheckoutAsyncPaymentSucceeded(ctx, event)
	case "checkout.session.async_payment_failed":
		return s.handleCheckoutAsyncPaymentFailed(ctx, event)
	default:
		s.logger.Info("unhandled_event_type", slog.String("event_type", string(event.Type)))
		return 200, nil
	}
}

// markEventProcessed inserts the event into processed_webhook_events at the start of a transaction.
// It returns isDuplicate=true if the event was already recorded (ON CONFLICT), with no error.
// Callers should rollback the transaction and return 200 on duplicate.
func (s *Service) markEventProcessed(ctx context.Context, tx *sql.Tx, eventID, eventType string) (isDuplicate bool, err error) {
	result, err := tx.ExecContext(ctx,
		"INSERT INTO processed_webhook_events (event_id, event_type, processed_at) VALUES ($1, $2, NOW()) ON CONFLICT (event_id) DO NOTHING",
		eventID, eventType)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 0, nil
}

func (s *Service) handleCheckoutSessionCompleted(ctx context.Context, event stripe.Event) (int, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		s.logger.Error("failed_to_parse_checkout_session", slog.Any("error", err))
		return 400, fmt.Errorf("failed to parse session: %w", err)
	}

	s.logger.Info("processing_checkout_session_completed", slog.String("session_id", session.ID))

	// Verify payment status before processing
	if session.PaymentStatus != "paid" {
		s.logger.Info("session_completed_not_paid", slog.String("session_id", session.ID), slog.String("payment_status", string(session.PaymentStatus)))
		return 200, nil
	}

	// Look up the user/credits/currency from the stripe_payments row inserted
	// during CreateCheckoutSession. The DB row is the AUTHORITATIVE source.
	//
	// Historical note: this handler used to fall back to session.Metadata when
	// the DB row was missing. That fallback was a foot-gun: any future code
	// path that lets a client influence session metadata becomes a
	// free-credits vulnerability. The DB row is server-controlled and
	// authoritative; metadata is rendered into the Session by us, not the
	// authoritative source. (S-M1)
	//
	// If the DB row is missing despite a 'paid' webhook arriving, that's a
	// real bug worth alerting on (the row insert at CreateCheckoutSession
	// silently failed, or the row was deleted by ops). We log at ERROR and
	// return 200 to prevent a Stripe retry storm — the alert is the metric
	// counter, not the webhook dispatch.
	var (
		userID   string
		credits  int
		currency string
	)
	const sel = `SELECT user_id, (credits_purchased)::int, currency FROM stripe_payments WHERE stripe_checkout_session_id=$1 LIMIT 1`
	err := s.db.QueryRowContext(ctx, sel, session.ID).Scan(&userID, &credits, &currency)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.logger.Error("checkout_completed_missing_db_row",
				slog.String("session_id", session.ID),
				slog.String("event_id", event.ID),
			)
			s.metrics.CheckoutMissingRowTotal.Inc()
			return 200, nil // ack to prevent Stripe retry storm; rely on metric alert
		}
		s.logger.Error("database_query_failed", slog.Any("error", err))
		return 500, fmt.Errorf("database query failed: %w", err)
	}

	// The DB CHECK constraint on stripe_payments.credits_purchased > 0 means
	// these guards should be unreachable, but kept as defense-in-depth.
	if userID == "" {
		s.logger.Warn("no_user_id_for_session", slog.String("session_id", session.ID))
		return 200, nil
	}
	if credits <= 0 {
		s.logger.Warn("invalid_credits_amount", slog.Int("credits", credits), slog.String("session_id", session.ID))
		return 200, nil
	}
	if currency != "USD" {
		s.logger.Warn("unsupported_currency", slog.String("currency", currency), slog.String("session_id", session.ID))
		return 200, nil
	}

	// Process the payment in a transaction with proper isolation level
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		s.logger.Error("failed_to_begin_transaction", slog.Any("error", err))
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()

	// Dedup check: insert into processed_webhook_events at the START of the transaction.
	// ON CONFLICT DO NOTHING returns 0 rows affected if already processed.
	// This is the sole idempotency gate — no pre-check outside the transaction.
	isDuplicate, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		s.logger.Error("failed_to_mark_event_processed", slog.Any("error", err))
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}
	if isDuplicate {
		s.logger.Info("event_already_processed", slog.String("event_id", event.ID))
		return 200, nil // tx deferred Rollback handles cleanup
	}

	// Backfill the Stripe PaymentIntent ID onto the stripe_payments row. The
	// row was created during CreateCheckoutSession with only the checkout
	// session ID; this is the first and canonical opportunity to learn the PI
	// ID and link them. Without this backfill, the charge.refunded handler's
	// primary lookup by stripe_payment_intent_id always misses. (S-C2)
	paymentIntentID := ""
	if session.PaymentIntent != nil && session.PaymentIntent.ID != "" {
		paymentIntentID = session.PaymentIntent.ID
	}
	if paymentIntentID != "" {
		// Idempotent: tolerate webhook replays (where the PI ID may already be set)
		// by matching both NULL and the same value.
		const updPI = `UPDATE stripe_payments
			SET stripe_payment_intent_id = $1
			WHERE stripe_checkout_session_id = $2
			  AND (stripe_payment_intent_id IS NULL OR stripe_payment_intent_id = $1)`
		if _, err := tx.ExecContext(ctx, updPI, paymentIntentID, session.ID); err != nil {
			s.logger.Error("failed_to_backfill_payment_intent_id",
				slog.String("session_id", session.ID),
				slog.String("payment_intent_id", paymentIntentID),
				slog.Any("error", err),
			)
			return 500, fmt.Errorf("failed to backfill payment intent: %w", err)
		}
	} else {
		// Missing PI on a payment-mode session should not happen, but we log
		// rather than fail because balance credit is more important than the link.
		s.logger.Warn("checkout_completed_missing_payment_intent",
			slog.String("session_id", session.ID),
			slog.String("event_id", event.ID),
		)
	}

	// Read the user's current balance AND refund deficit in a single
	// SELECT FOR UPDATE. The same row-level lock protects both fields — we
	// need the deficit value because the refund deficit ledger (S-C4) pays
	// down any outstanding deficit from the incoming purchase BEFORE crediting
	// new spendable balance. credit_balance is scanned as float64 (via
	// ::float8) while refund_deficit_credits is scanned as text and then
	// parsed into int64 micro-credits to avoid IEEE 754 rounding errors in
	// the split arithmetic below.
	var currentBalance float64
	var deficitStr string
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(credit_balance, 0)::float8, COALESCE(refund_deficit_credits, 0)::text
		 FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&currentBalance, &deficitStr)
	if err != nil {
		if err == sql.ErrNoRows {
			s.logger.Warn("user_not_found", slog.String("user_id", userID))
			return 400, fmt.Errorf("user not found: %s", userID)
		}
		s.logger.Error("failed_to_get_user_balance_and_deficit", slog.Any("error", err))
		return 500, fmt.Errorf("failed to get user balance and deficit: %w", err)
	}
	deficitFloat, _ := strconv.ParseFloat(deficitStr, 64)
	deficitMicro := int64(math.Round(deficitFloat * models.MicroUnit))

	// Apply the incoming credits to the refund deficit first. Any remainder
	// goes to spendable balance. This is what makes the refund deficit ledger
	// self-correcting: users who owe us credits from a past refund pay it back
	// through their next purchase before any new spendable balance accrues.
	//
	// We do the split in integer micro-credit arithmetic to avoid float drift.
	purchaseMicro := int64(credits) * models.MicroUnit
	appliedToDeficitMicro := int64(0)
	appliedToBalanceMicro := purchaseMicro
	if deficitMicro > 0 {
		if purchaseMicro >= deficitMicro {
			appliedToDeficitMicro = deficitMicro
			appliedToBalanceMicro = purchaseMicro - deficitMicro
		} else {
			appliedToDeficitMicro = purchaseMicro
			appliedToBalanceMicro = 0
		}
	}
	appliedToDeficitFloat := float64(appliedToDeficitMicro) / models.MicroUnit
	appliedToBalanceFloat := float64(appliedToBalanceMicro) / models.MicroUnit

	// Update user credit balance. The deficit paydown is applied via
	// GREATEST(0, ...) so a concurrent write cannot drive it negative — the
	// CHECK constraint on the column would reject the row even if we tried.
	// total_credits_purchased tracks lifetime purchases regardless of how
	// much of this purchase hit the balance vs deficit.
	// updated_at is intentionally NOT set here — the table trigger handles it.
	const updUser = `UPDATE users SET
		credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
		total_credits_purchased = COALESCE(total_credits_purchased, 0) + $2::numeric,
		refund_deficit_credits = GREATEST(0, refund_deficit_credits - $3::numeric)
		WHERE id = $4`
	result, err := tx.ExecContext(ctx, updUser, appliedToBalanceFloat, credits, appliedToDeficitFloat, userID)
	if err != nil {
		s.logger.Error("failed_to_update_user_credits", slog.Any("error", err))
		return 500, fmt.Errorf("failed to update user credits: %w", err)
	}

	// Check if user exists
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		s.logger.Error("failed_to_get_rows_affected", slog.Any("error", err))
		return 500, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		s.logger.Warn("no_user_found", slog.String("user_id", userID))
		return 400, fmt.Errorf("user not found: %s", userID)
	}

	// If any portion of the purchase paid down a deficit, record a
	// deficit_paydown ledger row for audit. amount=0 because the deficit
	// lives outside the spendable balance ledger; balance_before and
	// balance_after are identical (currentBalance) so the
	// balance_calculation_check constraint is satisfied.
	if appliedToDeficitMicro > 0 {
		const insTxnPaydown = `INSERT INTO credit_transactions
			(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
			VALUES ($1, $2, 'deficit_paydown', 0, $3, $3, $4, $5, 'payment')`
		desc := fmt.Sprintf("Deficit paydown from Stripe purchase %s: %.6f credits", session.ID, appliedToDeficitFloat)
		if _, err := tx.ExecContext(ctx, insTxnPaydown,
			uuid.Must(uuid.NewV7()).String(), userID, currentBalance, desc, session.ID); err != nil {
			s.logger.Error("failed_to_insert_deficit_paydown_transaction", slog.Any("error", err))
			return 500, fmt.Errorf("failed to insert deficit paydown transaction: %w", err)
		}
	}

	// Insert the purchase ledger row. The amount, balance_before, and
	// balance_after triple must satisfy balance_after = balance_before + amount
	// per the balance_calculation_check constraint. We use
	// appliedToBalanceFloat (not the full purchase amount) because only that
	// portion actually hit the spendable balance — the deficit paydown is a
	// separate entry above.
	const insTxn = `INSERT INTO credit_transactions (id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
					VALUES ($1, $2, 'purchase', $3, $4, $5, $6, $7, 'payment')`
	_, err = tx.ExecContext(ctx, insTxn, uuid.Must(uuid.NewV7()).String(), userID,
		appliedToBalanceFloat,
		currentBalance,
		currentBalance+appliedToBalanceFloat,
		"Stripe purchase", session.ID)
	if err != nil {
		s.logger.Error("failed_to_insert_credit_transaction", slog.Any("error", err))
		return 500, fmt.Errorf("failed to insert credit transaction: %w", err)
	}

	// Mark stripe payment as succeeded
	const updPay = `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`
	_, err = tx.ExecContext(ctx, updPay, session.ID)
	if err != nil {
		s.logger.Error("failed_to_update_payment_status", slog.Any("error", err))
		return 500, fmt.Errorf("failed to update payment status: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		s.logger.Error("failed_to_commit_transaction", slog.Any("error", err))
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.logger.Info("checkout_session_completed", slog.String("user_id", userID), slog.Int("credits", credits), slog.String("session_id", session.ID))
	return 200, nil
}

func (s *Service) handleCheckoutSessionExpired(ctx context.Context, event stripe.Event) (int, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		s.logger.Error("failed_to_parse_expired_session", slog.Any("error", err))
		return 400, fmt.Errorf("failed to parse session: %w", err)
	}

	s.logger.Info("processing_checkout_session_expired", slog.String("session_id", session.ID))

	// Begin transaction for consistency and idempotency
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		s.logger.Error("failed_to_begin_transaction_expired", slog.Any("error", err))
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()

	// Dedup check at the START of the transaction.
	isDuplicate, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		s.logger.Error("failed_to_mark_expired_event_processed", slog.Any("error", err))
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}
	if isDuplicate {
		s.logger.Info("event_already_processed", slog.String("event_id", event.ID))
		return 200, nil
	}

	// Update payment status to canceled
	const upd = `UPDATE stripe_payments SET status='canceled', updated_at=NOW() WHERE stripe_checkout_session_id=$1`
	_, err = tx.ExecContext(ctx, upd, session.ID)
	if err != nil {
		s.logger.Error("failed_to_update_expired_payment_status", slog.Any("error", err))
		return 500, fmt.Errorf("failed to update expired payment status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		s.logger.Error("failed_to_commit_expired_session_transaction", slog.Any("error", err))
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.logger.Info("checkout_session_expired", slog.String("session_id", session.ID))
	return 200, nil
}

// ReconcileSession fetches a Checkout Session from Stripe and applies credits if paid.
// This can be used as a fallback mechanism if webhooks fail.
// callerUserID must be the authenticated user — ownership is enforced against stripe_payments.
func (s *Service) ReconcileSession(ctx context.Context, sessionID, callerUserID string) error {
	if sessionID == "" {
		return fmt.Errorf("missing session id")
	}
	if callerUserID == "" {
		return fmt.Errorf("missing user id")
	}
	sess, err := checkoutsession.Get(sessionID, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch session: %w", err)
	}

	// Fetch payment row, enforcing ownership (CWE-639: IDOR prevention).
	var (
		userID   string
		credits  int
		currency string
		status   string
	)
	const sel = `SELECT user_id, (credits_purchased)::int, currency, status FROM stripe_payments WHERE stripe_checkout_session_id=$1 AND user_id=$2 LIMIT 1`
	if err := s.db.QueryRowContext(ctx, sel, sessionID, callerUserID).Scan(&userID, &credits, &currency, &status); err != nil {
		return fmt.Errorf("payment row not found: %w", err)
	}

	// Skip if already succeeded
	if status == "succeeded" {
		return nil
	}

	// Check if session is paid
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return fmt.Errorf("session not paid")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()

	var exists bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM credit_transactions WHERE reference_id=$1 AND reference_type='payment')",
		sessionID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check transaction existence: %w", err)
	}
	if exists {
		if _, err := tx.ExecContext(ctx, `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`, sessionID); err != nil {
			return fmt.Errorf("failed to update payment status (idempotent path): %w", err)
		}
		return tx.Commit()
	}

	// Read the user's current balance AND refund deficit in one
	// SELECT FOR UPDATE. Same row-level lock protects both fields. The
	// deficit-paydown logic mirrors handleCheckoutSessionCompleted (S-C4)
	// so the webhook-fallback reconcile path applies the same paydown
	// invariant — without this, a user could exploit a delayed webhook to
	// bypass the deficit ledger by triggering reconcile from the frontend.
	var currentBalance float64
	var deficitStr string
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(credit_balance, 0)::float8, COALESCE(refund_deficit_credits, 0)::text
		 FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&currentBalance, &deficitStr)
	if err != nil {
		return fmt.Errorf("failed to get user balance and deficit: %w", err)
	}
	deficitFloat, _ := strconv.ParseFloat(deficitStr, 64)
	deficitMicro := int64(math.Round(deficitFloat * models.MicroUnit))

	// Apply incoming credits to deficit first, remainder to balance.
	// Integer micro-credit math avoids float drift on the split.
	purchaseMicro := int64(credits) * models.MicroUnit
	appliedToDeficitMicro := int64(0)
	appliedToBalanceMicro := purchaseMicro
	if deficitMicro > 0 {
		if purchaseMicro >= deficitMicro {
			appliedToDeficitMicro = deficitMicro
			appliedToBalanceMicro = purchaseMicro - deficitMicro
		} else {
			appliedToDeficitMicro = purchaseMicro
			appliedToBalanceMicro = 0
		}
	}
	appliedToDeficitFloat := float64(appliedToDeficitMicro) / models.MicroUnit
	appliedToBalanceFloat := float64(appliedToBalanceMicro) / models.MicroUnit

	// Update user row: add to balance, add to lifetime, decrement deficit.
	// updated_at handled by trigger.
	const updUser = `UPDATE users SET
		credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
		total_credits_purchased = COALESCE(total_credits_purchased, 0) + $2::numeric,
		refund_deficit_credits = GREATEST(0, refund_deficit_credits - $3::numeric)
		WHERE id = $4`
	if _, err := tx.ExecContext(ctx, updUser, appliedToBalanceFloat, credits, appliedToDeficitFloat, userID); err != nil {
		return fmt.Errorf("failed to update user credits: %w", err)
	}

	// If any portion paid down deficit, record a deficit_paydown ledger row.
	// amount=0 because deficit lives outside the spendable balance ledger.
	if appliedToDeficitMicro > 0 {
		const insTxnPaydown = `INSERT INTO credit_transactions
			(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
			VALUES ($1, $2, 'deficit_paydown', 0, $3, $3, $4, $5, 'payment')`
		desc := fmt.Sprintf("Deficit paydown from Stripe purchase %s (reconcile): %.6f credits", sessionID, appliedToDeficitFloat)
		if _, err := tx.ExecContext(ctx, insTxnPaydown,
			uuid.Must(uuid.NewV7()).String(), userID, currentBalance, desc, sessionID); err != nil {
			return fmt.Errorf("failed to insert deficit paydown transaction: %w", err)
		}
	}

	// Insert the purchase ledger row using appliedToBalanceFloat (not full
	// credits) so the balance_calculation_check constraint holds.
	const insTxn = `INSERT INTO credit_transactions
		(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
		VALUES ($1, $2, 'purchase', $3, $4, $5, $6, $7, 'payment')`
	if _, err := tx.ExecContext(ctx, insTxn,
		uuid.Must(uuid.NewV7()).String(), userID,
		appliedToBalanceFloat,
		currentBalance,
		currentBalance+appliedToBalanceFloat,
		"Stripe purchase (reconcile)", sessionID); err != nil {
		return fmt.Errorf("failed to insert credit transaction: %w", err)
	}

	// Update payment status
	if _, err := tx.ExecContext(ctx, `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`, sessionID); err != nil {
		return fmt.Errorf("failed to update payment status: %w", err)
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
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal billing metadata: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()

	// Insert billing event; trigger resolves pricing and totals
	var (
		eventID               string
		unitPrice, totalPrice string // scan as text to preserve precision
	)
	const insEvent = `INSERT INTO billing_events (id, user_id, job_id, event_type_code, quantity, metadata)
	                  VALUES ($1,$2,$3,$4,$5,$6::jsonb)
	                  ON CONFLICT (job_id, event_type_code, (metadata->>'idempotency_key'))
	                  WHERE (metadata ? 'idempotency_key')
	                  DO UPDATE SET quantity = billing_events.quantity
	                  RETURNING id, unit_price_credits::text, total_price_credits::text`
	if err := tx.QueryRowContext(ctx, insEvent, uuid.Must(uuid.NewV7()).String(), userID, jobID, eventType, quantity, string(metaJSON)).Scan(&eventID, &unitPrice, &totalPrice); err != nil {
		return fmt.Errorf("insert billing event: %w", err)
	}

	// Decrement user balance atomically, ensuring non-negative balance
	const decBal = `UPDATE users
		SET credit_balance = credit_balance - $1::numeric,
		    total_credits_consumed = COALESCE(total_credits_consumed, 0) + $1::numeric,
		    updated_at = NOW()
		WHERE id = $2 AND credit_balance >= $1::numeric
		RETURNING credit_balance::text`
	var newBalance string
	if err := tx.QueryRowContext(ctx, decBal, totalPrice, userID).Scan(&newBalance); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("insufficient credits")
		}
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Insert credit transaction (consumption), linking to billing event via metadata reference_id
	const insTxn = `INSERT INTO credit_transactions (
		id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type, metadata
	) VALUES (
		$1, $2, 'consumption', -$3::numeric,
		($4::numeric + $3::numeric),
		$4::numeric,
		$5, $6, 'job', jsonb_build_object('billing_event_id', $7::text)
	)`
	if _, err := tx.ExecContext(ctx, insTxn, uuid.Must(uuid.NewV7()).String(), userID, totalPrice, newBalance, fmt.Sprintf("Billing charge: %s", eventType), jobID, eventID); err != nil {
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

// ChargeReviews charges review events for N reviews scraped in a job.
func (s *Service) ChargeReviews(ctx context.Context, userID, jobID string, reviews int) error {
	if reviews <= 0 {
		return nil
	}
	return s.ChargeEvent(ctx, userID, jobID, "review", reviews, "job:"+jobID+":review", map[string]any{})
}

// ChargeImages charges image events for N images scraped in a job.
func (s *Service) ChargeImages(ctx context.Context, userID, jobID string, images int) error {
	if images <= 0 {
		return nil
	}
	return s.ChargeEvent(ctx, userID, jobID, "image", images, "job:"+jobID+":image", map[string]any{})
}

// ChargeContactDetails charges contact_details events for N places where contact details were extracted.
func (s *Service) ChargeContactDetails(ctx context.Context, userID, jobID string, placesWithContacts int) error {
	if placesWithContacts <= 0 {
		return nil
	}
	return s.ChargeEvent(ctx, userID, jobID, "contact_details", placesWithContacts, "job:"+jobID+":contact_details", map[string]any{})
}

// BillingCounts represents the counts of billable items in a job's results.
type BillingCounts struct {
	TotalReviews       int
	TotalImages        int
	PlacesWithContacts int
}

// queryRowContexter is satisfied by both *sql.DB and *sql.Tx.
type queryRowContexter interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// billableItemsQuery is the shared SQL used by CountBillableItems and countBillableItemsWith.
const billableItemsQuery = `
	SELECT
		COALESCE(SUM(
			CASE
				WHEN user_reviews IS NOT NULL AND jsonb_typeof(user_reviews) = 'array'
				THEN jsonb_array_length(user_reviews)
				ELSE 0
			END +
			CASE
				WHEN user_reviews_extended IS NOT NULL AND jsonb_typeof(user_reviews_extended) = 'array'
				THEN jsonb_array_length(user_reviews_extended)
				ELSE 0
			END
		), 0) AS total_reviews,
		COALESCE(SUM(
			CASE
				WHEN images IS NOT NULL AND jsonb_typeof(images) = 'array'
				THEN jsonb_array_length(images)
				ELSE 0
			END
		), 0) AS total_images,
		COUNT(CASE WHEN emails IS NOT NULL AND emails != '' THEN 1 END) AS places_with_contacts
	FROM results
	WHERE job_id = $1
`

// countBillableItemsWith counts reviews, images, and contact details from job results
// using the provided querier (either *sql.DB or *sql.Tx).
func (s *Service) countBillableItemsWith(ctx context.Context, q queryRowContexter, jobID string) (*BillingCounts, error) {
	counts := &BillingCounts{}
	err := q.QueryRowContext(ctx, billableItemsQuery, jobID).Scan(
		&counts.TotalReviews,
		&counts.TotalImages,
		&counts.PlacesWithContacts,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to count billable items: %w", err)
	}
	return counts, nil
}

// CountBillableItems counts reviews, images, and contact details from job results.
// This scans the results table and aggregates counts from JSONB fields.
func (s *Service) CountBillableItems(ctx context.Context, jobID string) (*BillingCounts, error) {
	if s.db == nil {
		return nil, fmt.Errorf("db not configured")
	}
	return s.countBillableItemsWith(ctx, s.db, jobID)
}

// ChargeAllJobEvents charges all billing events for a completed job in a single transaction.
// This ensures atomicity - either all charges succeed or all are rolled back.
// If any charge fails due to insufficient balance, the entire transaction is rolled back.
func (s *Service) ChargeAllJobEvents(ctx context.Context, userID, jobID string, placesCount int) error {
	if s.db == nil {
		return fmt.Errorf("db not configured")
	}

	// Start a single transaction for all charges
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}() // Rollback if we don't commit

	// Helper function to charge an event within this transaction
	chargeEventInTx := func(eventType string, quantity int, idempotencyKey string) error {
		if quantity <= 0 {
			return nil // Skip if nothing to charge
		}

		metadata := map[string]any{"idempotency_key": idempotencyKey}
		metaJSON, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal billing metadata: %w", err)
		}

		// Insert billing event
		var eventID, unitPrice, totalPrice string
		const insEvent = `INSERT INTO billing_events (id, user_id, job_id, event_type_code, quantity, metadata)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb)
			ON CONFLICT (job_id, event_type_code, (metadata->>'idempotency_key'))
			WHERE (metadata ? 'idempotency_key')
			DO UPDATE SET quantity = billing_events.quantity
			RETURNING id, unit_price_credits::text, total_price_credits::text`

		if err := tx.QueryRowContext(ctx, insEvent, uuid.Must(uuid.NewV7()).String(), userID, jobID, eventType, quantity, string(metaJSON)).Scan(&eventID, &unitPrice, &totalPrice); err != nil {
			return fmt.Errorf("insert %s event: %w", eventType, err)
		}

		// Decrement user balance atomically
		const decBal = `UPDATE users
			SET credit_balance = credit_balance - $1::numeric,
			    total_credits_consumed = COALESCE(total_credits_consumed, 0) + $1::numeric,
			    updated_at = NOW()
			WHERE id = $2 AND credit_balance >= $1::numeric
			RETURNING credit_balance::text`

		var newBalance string
		if err := tx.QueryRowContext(ctx, decBal, totalPrice, userID).Scan(&newBalance); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("insufficient credits to charge %s (%d items)", eventType, quantity)
			}
			return fmt.Errorf("failed to update balance for %s: %w", eventType, err)
		}

		// Insert credit transaction
		const insTxn = `INSERT INTO credit_transactions (
			id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type, metadata
		) VALUES (
			$1, $2, 'consumption', -$3::numeric,
			($4::numeric + $3::numeric),
			$4::numeric,
			$5, $6, 'job', jsonb_build_object('billing_event_id', $7::text)
		)`
		if _, err := tx.ExecContext(ctx, insTxn, uuid.Must(uuid.NewV7()).String(), userID, totalPrice, newBalance, fmt.Sprintf("Billing charge: %s", eventType), jobID, eventID); err != nil {
			return fmt.Errorf("insert credit transaction for %s: %w", eventType, err)
		}

		return nil
	}

	// 1. Charge for places
	if err := chargeEventInTx("place_scraped", placesCount, "job:"+jobID+":place_scraped"); err != nil {
		return err // Transaction will be rolled back
	}

	// 2. Count and charge for reviews, images, and contacts (within the tx to avoid read skew)
	counts, err := s.countBillableItemsWith(ctx, tx, jobID)
	if err != nil {
		return fmt.Errorf("failed to count billable items: %w", err)
	}

	// 3. Charge for reviews
	if err := chargeEventInTx("review", counts.TotalReviews, "job:"+jobID+":review"); err != nil {
		return err // Transaction will be rolled back
	}

	// 4. Charge for images
	if err := chargeEventInTx("image", counts.TotalImages, "job:"+jobID+":image"); err != nil {
		return err // Transaction will be rolled back
	}

	// 5. Charge for contact details
	if err := chargeEventInTx("contact_details", counts.PlacesWithContacts, "job:"+jobID+":contact_details"); err != nil {
		return err // Transaction will be rolled back
	}

	// All charges succeeded - commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit billing transaction: %w", err)
	}

	return nil
}

// handleChargeRefunded processes charge.refunded Stripe webhook events.
// It deducts credits proportional to the refunded amount and records the
// transaction, using the same idempotency pattern as other webhook handlers.
func (s *Service) handleChargeRefunded(ctx context.Context, event stripe.Event) (int, error) {
	var charge stripe.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		s.logger.Error("failed_to_parse_charge_refunded", slog.Any("error", err))
		return 400, fmt.Errorf("failed to parse charge: %w", err)
	}

	s.logger.Info("processing_charge_refunded",
		slog.String("charge_id", charge.ID),
		slog.Int64("amount_refunded_cents", charge.AmountRefunded),
		slog.Int64("original_amount_cents", charge.Amount),
	)

	if charge.Amount <= 0 || charge.AmountRefunded <= 0 {
		s.logger.Warn("charge_refunded_no_refund_amount", slog.String("charge_id", charge.ID))
		return 200, nil
	}

	// Look up the payment record using the payment intent ID to find the user and credits.
	// Scan credits_purchased as text to preserve NUMERIC(18,6) precision; the
	// proportional refund math below runs in decimal.Decimal to avoid float drift. (S-H2)
	var (
		userID            string
		creditsGrantedStr string
		amountCents       int64
	)

	paymentIntentID := ""
	if charge.PaymentIntent != nil {
		paymentIntentID = charge.PaymentIntent.ID
	}

	if paymentIntentID != "" {
		const sel = `SELECT user_id, credits_purchased::text, amount_cents FROM stripe_payments WHERE stripe_payment_intent_id = $1 LIMIT 1`
		err := s.db.QueryRowContext(ctx, sel, paymentIntentID).Scan(&userID, &creditsGrantedStr, &amountCents)
		if err != nil && err != sql.ErrNoRows {
			s.logger.Error("failed_to_lookup_payment_for_charge", slog.Any("error", err))
			return 500, fmt.Errorf("failed to lookup payment: %w", err)
		}
	}

	// Fallback: try to look up via customer ID on the users table.
	if userID == "" && charge.Customer != nil && charge.Customer.ID != "" {
		const sel = `SELECT id FROM users WHERE stripe_customer_id = $1 LIMIT 1`
		if err := s.db.QueryRowContext(ctx, sel, charge.Customer.ID).Scan(&userID); err != nil && err != sql.ErrNoRows {
			s.logger.Warn("fallback_user_lookup_failed",
				slog.String("customer_id", charge.Customer.ID),
				slog.Any("error", err),
			)
		}
	}

	if userID == "" {
		s.logger.Warn("charge_refunded_no_user_found",
			slog.String("charge_id", charge.ID),
			slog.String("payment_intent_id", paymentIntentID),
		)
		return 200, nil
	}

	// Parse credits_purchased into a decimal.Decimal so the proportional refund
	// math runs without float drift. The customer-fallback lookup path
	// (line ~1004) does not populate creditsGrantedStr, so an empty string
	// here means we have no granted-credits info and must skip the deduction.
	// (S-H2)
	var creditsGranted decimal.Decimal
	if creditsGrantedStr != "" {
		var parseErr error
		creditsGranted, parseErr = decimal.NewFromString(creditsGrantedStr)
		if parseErr != nil {
			s.logger.Error("failed_to_parse_credits_granted",
				slog.String("credits_granted_str", creditsGrantedStr),
				slog.Any("error", parseErr),
			)
			return 500, fmt.Errorf("failed to parse credits_granted: %w", parseErr)
		}
	}
	// shouldDeduct guards the same condition the old `creditsToDeduct > 0`
	// branch did: we need both a non-zero granted credit amount and a valid
	// original cents value to compute the proportional split.
	shouldDeduct := !creditsGranted.IsZero() && amountCents > 0

	// Compute the proportional credit deduction in decimal (for the trailing
	// "charge_refunded_processed" log). The actual balance/deficit split
	// happens inside the if-shouldDeduct branch via computeRefundSplit; this
	// scalar is the same value that split sums to. Zero when shouldDeduct=false.
	var creditsToDeductDec decimal.Decimal
	if shouldDeduct {
		refunded := decimal.NewFromInt(charge.AmountRefunded)
		original := decimal.NewFromInt(amountCents)
		creditsToDeductDec = refunded.Div(original).Mul(creditsGranted).Round(6)
	}

	// Begin transaction for idempotency and credit deduction.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		s.logger.Error("failed_to_begin_transaction_charge_refunded", slog.Any("error", err))
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()

	isDuplicate, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		s.logger.Error("failed_to_mark_charge_refunded_processed", slog.Any("error", err))
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}
	if isDuplicate {
		s.logger.Info("event_already_processed", slog.String("event_id", event.ID))
		return 200, nil
	}

	if shouldDeduct {
		// Get current balance with a row lock — scan as text and parse with
		// decimal.NewFromString to preserve NUMERIC(18,6) precision through
		// the proportional refund math. (S-H2)
		var balanceStr string
		err = tx.QueryRowContext(ctx,
			"SELECT COALESCE(credit_balance, 0)::text FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&balanceStr)
		if err != nil {
			s.logger.Error("failed_to_get_user_balance_for_refund", slog.Any("error", err))
			return 500, fmt.Errorf("failed to get user balance: %w", err)
		}
		balance, parseErr := decimal.NewFromString(balanceStr)
		if parseErr != nil {
			s.logger.Error("failed_to_parse_user_balance_for_refund", slog.Any("error", parseErr))
			return 500, fmt.Errorf("failed to parse user balance: %w", parseErr)
		}

		// Compute the (balance, deficit) split using exact decimal arithmetic.
		// See computeRefundSplit godoc for the rule. (S-H2)
		//
		// The deficit portion represents credits that were already consumed
		// before this refund arrived. Instead of failing the refund or silently
		// losing integrity, we write the remainder to users.refund_deficit_credits
		// so the next purchase pays it down before crediting new spendable balance.
		//
		// This preserves the CHECK (credit_balance >= 0) financial-safety
		// invariant while making the refund pipeline financially correct
		// end-to-end. Matches the pre-paid credit refund pattern used by
		// Vercel, Anthropic (Claude API), OpenAI, and Stripe Billing's own
		// credit grants.
		//
		// Idempotency: the markEventProcessed gate (processed_webhook_events)
		// at the top of this transaction prevents double-deductions on Stripe
		// webhook retries. The metric increment below is therefore also
		// idempotency-safe — it is only reached once per Stripe event.
		//
		// Alerting: the refund_deficit_applied_total Prometheus counter is
		// incremented when any deficit is created. Any non-zero rate indicates
		// users buying, consuming, then refunding — possibly benign churn or
		// possibly fraud. See pkg/metrics/billing.go for the ops runbook.
		deductFromBalanceDec, deductFromDeficitDec := computeRefundSplit(balance, creditsGranted, amountCents, charge.AmountRefunded)
		newBalanceDec := balance.Sub(deductFromBalanceDec)

		// Float64 values are computed ONLY for ledger row inserts and slog
		// log fields where the existing schema/types require float64. All
		// arithmetic operands above are decimal.Decimal — these conversions
		// happen at the leaf rendering edge only.
		balanceFloat, _ := balance.Float64()
		newBalanceFloat, _ := newBalanceDec.Float64()
		actualDeductFloat, _ := deductFromBalanceDec.Float64()
		deficitIncreaseFloat, _ := deductFromDeficitDec.Float64()

		// Update both columns in one UPDATE — pass decimal values as strings
		// so Postgres NUMERIC handles the arithmetic without any Go-side float
		// rounding. updated_at is intentionally NOT touched; the trigger handles it.
		const updBalance = `UPDATE users
			SET credit_balance = $1::numeric,
			    refund_deficit_credits = refund_deficit_credits + $2::numeric
			WHERE id = $3`
		if _, err := tx.ExecContext(ctx, updBalance,
			newBalanceDec.StringFixed(6),
			deductFromDeficitDec.StringFixed(6),
			userID); err != nil {
			s.logger.Error("failed_to_deduct_credits_for_refund", slog.Any("error", err))
			return 500, fmt.Errorf("failed to deduct credits: %w", err)
		}

		// Record the balance-side deduction as a 'refund' ledger row, only if
		// any portion actually hit the spendable balance. The amount,
		// balance_before, and balance_after triple satisfies the
		// balance_calculation_check constraint (balance_after = balance_before + amount).
		if !deductFromBalanceDec.IsZero() {
			const insTxnRefund = `INSERT INTO credit_transactions
				(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
				VALUES ($1, $2, 'refund', $3, $4, $5, $6, $7, 'payment')`
			desc := fmt.Sprintf("Stripe refund for charge %s", charge.ID)
			if _, err := tx.ExecContext(ctx, insTxnRefund,
				uuid.Must(uuid.NewV7()).String(), userID, -actualDeductFloat,
				balanceFloat, newBalanceFloat, desc, charge.ID); err != nil {
				s.logger.Error("failed_to_insert_refund_transaction", slog.Any("error", err))
				return 500, fmt.Errorf("failed to insert refund transaction: %w", err)
			}
		}

		// Record the deficit-side portion as a separate 'refund_deficit' row.
		// amount=0 because the deficit lives outside the spendable balance
		// ledger; balance_before and balance_after are identical (newBalanceFloat)
		// so the balance_calculation_check constraint is satisfied. The
		// deficit amount is captured in metadata for audit queries.
		if !deductFromDeficitDec.IsZero() {
			const insTxnDeficit = `INSERT INTO credit_transactions
				(id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type, metadata)
				VALUES ($1, $2, 'refund_deficit', 0, $3, $3, $4, $5, 'payment', $6::jsonb)`
			desc := fmt.Sprintf("Stripe refund deficit for charge %s: %.6f credits uncollectable", charge.ID, deficitIncreaseFloat)
			// Use json.Marshal rather than hand-formatting the JSON: even though
			// charge.ID is alphanumeric by Stripe convention, defensive encoding
			// removes injection-shape concerns regardless of what Stripe ever sends.
			metadataBytes, err := json.Marshal(map[string]string{
				"deficit_amount": strconv.FormatFloat(deficitIncreaseFloat, 'f', 6, 64),
				"charge_id":      charge.ID,
			})
			if err != nil {
				s.logger.Error("failed_to_marshal_refund_deficit_metadata", slog.Any("error", err))
				return 500, fmt.Errorf("failed to marshal refund deficit metadata: %w", err)
			}
			if _, err := tx.ExecContext(ctx, insTxnDeficit,
				uuid.Must(uuid.NewV7()).String(), userID, newBalanceFloat, desc, charge.ID, string(metadataBytes)); err != nil {
				s.logger.Error("failed_to_insert_refund_deficit_transaction", slog.Any("error", err))
				return 500, fmt.Errorf("failed to insert refund deficit transaction: %w", err)
			}

			// Emit metric + ERROR log so ops sees every deficit event. ERROR
			// (not WARN) because this is the signal that a user bought,
			// consumed, then refunded — worth a Grafana alert.
			s.metrics.RefundDeficitAppliedTotal.Inc()
			s.logger.Error("refund_deficit_applied",
				slog.String("user_id", userID),
				slog.String("charge_id", charge.ID),
				slog.Float64("deficit_credits", deficitIncreaseFloat),
				slog.Float64("actual_balance_deduction", actualDeductFloat),
				slog.Float64("original_balance", balanceFloat),
			)
		}

		// Update stripe_payments record if we have a payment intent ID.
		// Status precedence:
		//   - refund_deficit_applied (any deficit was created — ops review)
		//   - partial_refund (Stripe refunded less than the original charge)
		//   - refunded (full refund, no deficit)
		// updated_at is intentionally NOT touched; trigger handles it.
		if paymentIntentID != "" {
			paymentStatus := "refunded"
			if charge.AmountRefunded < charge.Amount {
				paymentStatus = "partial_refund"
			}
			if !deductFromDeficitDec.IsZero() {
				paymentStatus = "refund_deficit_applied"
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE stripe_payments SET status = $1, refunded_amount_cents = $2 WHERE stripe_payment_intent_id = $3`,
				paymentStatus, charge.AmountRefunded, paymentIntentID); err != nil {
				s.logger.Error("failed_to_update_stripe_payment_status",
					slog.String("payment_intent_id", paymentIntentID),
					slog.String("status", paymentStatus),
					slog.Any("error", err),
				)
				return 500, fmt.Errorf("failed to update stripe payment status: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		s.logger.Error("failed_to_commit_charge_refunded", slog.Any("error", err))
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	creditsToDeductFloat, _ := creditsToDeductDec.Float64()
	s.logger.Info("charge_refunded_processed",
		slog.String("user_id", userID),
		slog.String("charge_id", charge.ID),
		slog.Float64("credits_deducted", creditsToDeductFloat),
	)
	return 200, nil
}

// handleChargeFailed processes charge.failed Stripe webhook events.
// No credit changes are made — credits are only granted on checkout.session.completed.
func (s *Service) handleChargeFailed(ctx context.Context, event stripe.Event) (int, error) {
	var charge stripe.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		s.logger.Error("failed_to_parse_charge_failed", slog.Any("error", err))
		return 400, fmt.Errorf("failed to parse charge: %w", err)
	}

	failureMsg := ""
	if charge.FailureMessage != "" {
		failureMsg = charge.FailureMessage
	}

	userID := ""
	if charge.Customer != nil {
		if err := s.db.QueryRowContext(ctx, "SELECT id FROM users WHERE stripe_customer_id = $1 LIMIT 1", charge.Customer.ID).Scan(&userID); err != nil && err != sql.ErrNoRows {
			s.logger.Warn("charge_failed_user_lookup_failed",
				slog.String("customer_id", charge.Customer.ID),
				slog.Any("error", err),
			)
		}
	}

	s.logger.Warn("charge_failed",
		slog.String("charge_id", charge.ID),
		slog.String("user_id", userID),
		slog.Int64("amount_cents", charge.Amount),
		slog.String("failure_message", failureMsg),
		slog.String("failure_code", string(charge.FailureCode)),
	)

	return 200, nil
}

// handleChargeDisputeCreated processes charge.dispute.created Stripe webhook
// events. A dispute is a chargeback initiated by the cardholder against one
// of our charges; Stripe pulls the disputed funds AND a dispute fee out of
// our balance immediately, and we have until evidence_details.due_by to
// submit evidence.
//
// This handler does NOT touch credit_balance or refund_deficit_credits — the
// chargeback is a separate financial event from the proportional credit
// reversal that charge.refunded handles. We only:
//
//  1. Mark the affected stripe_payments row with status='disputed' so the ops
//     dashboard can surface it
//  2. Increment dispute_created_total Prometheus metric for alerting
//  3. Log at ERROR level with dispute_id, charge_id, payment_intent_id,
//     amount_cents, reason, due_by — everything ops needs to start an
//     evidence response
//
// Idempotency: gated via processed_webhook_events under SERIALIZABLE, same as
// every other webhook handler in this file. Stripe webhook retries do not
// double-flag the payment row.
//
// Stripe references:
//   - https://docs.stripe.com/disputes
//   - https://docs.stripe.com/api/disputes/object
//   - https://docs.stripe.com/api/events/types#event_types-charge.dispute.created
//
// (S-H5)
func (s *Service) handleChargeDisputeCreated(ctx context.Context, event stripe.Event) (int, error) {
	var dispute stripe.Dispute
	if err := json.Unmarshal(event.Data.Raw, &dispute); err != nil {
		s.logger.Error("failed_to_parse_charge_dispute_created", slog.Any("error", err))
		return 400, fmt.Errorf("failed to parse dispute: %w", err)
	}

	// Extract identifiers. Stripe sends both Charge and PaymentIntent as
	// either string IDs (default) or expanded objects (when expand was set
	// on the original API call). The stripe-go custom UnmarshalJSON
	// (`charge.go`, `paymentintent.go`) handles both shapes — we get the .ID
	// regardless.
	chargeID := ""
	if dispute.Charge != nil {
		chargeID = dispute.Charge.ID
	}
	paymentIntentID := ""
	if dispute.PaymentIntent != nil {
		paymentIntentID = dispute.PaymentIntent.ID
	}

	// Evidence response deadline. Stripe documents this as the date by which
	// we must submit evidence to challenge the dispute; missing it forfeits.
	var dueBy int64
	if dispute.EvidenceDetails != nil {
		dueBy = dispute.EvidenceDetails.DueBy
	}

	s.logger.Error("charge_dispute_created",
		slog.String("dispute_id", dispute.ID),
		slog.String("charge_id", chargeID),
		slog.String("payment_intent_id", paymentIntentID),
		slog.Int64("amount_cents", dispute.Amount),
		slog.String("currency", string(dispute.Currency)),
		slog.String("reason", string(dispute.Reason)),
		slog.String("status", string(dispute.Status)),
		slog.Bool("is_charge_refundable", dispute.IsChargeRefundable),
		slog.Int64("evidence_due_by_unix", dueBy),
	)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		s.logger.Error("failed_to_begin_transaction_charge_dispute_created", slog.Any("error", err))
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()

	isDuplicate, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		s.logger.Error("failed_to_mark_dispute_event_processed", slog.Any("error", err))
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}
	if isDuplicate {
		s.logger.Info("event_already_processed", slog.String("event_id", event.ID))
		return 200, nil
	}

	// Flag the affected stripe_payments row with status='disputed'. We use
	// the PaymentIntent ID as the join key (the same path the refund handler
	// uses, populated by the S-C2 backfill on checkout.session.completed).
	// If the row doesn't exist (legacy payment without backfilled PI ID, or
	// non-checkout charge), we fall through to the metric/log only — the
	// ERROR log above is the only ops signal in that case.
	if paymentIntentID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE stripe_payments SET status = 'disputed' WHERE stripe_payment_intent_id = $1`,
			paymentIntentID); err != nil {
			s.logger.Error("failed_to_flag_disputed_payment",
				slog.String("payment_intent_id", paymentIntentID),
				slog.Any("error", err),
			)
			return 500, fmt.Errorf("failed to flag disputed payment: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		s.logger.Error("failed_to_commit_charge_dispute_created", slog.Any("error", err))
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.metrics.DisputeCreatedTotal.Inc()
	return 200, nil
}

// handleRefundUpdated processes refund.updated webhook events. (S-M3)
//
// Modern Stripe asynchronous refund payment methods (SEPA Direct Debit, Bacs
// Direct Debit, ACH) can succeed initially, fire charge.refunded, and then
// FAIL later — at which point Stripe sends refund.updated with the new
// status='failed'. If we already deducted credits in handleChargeRefunded,
// we now have to reverse that deduction or the user keeps both their cash
// AND their credits.
//
// MVP scope: BrezelScraper currently only enables card payments via Stripe
// Checkout (USD-only). Card refunds are synchronous — once Stripe fires
// charge.refunded for a card refund, the refund cannot fail asynchronously.
// So in production today this handler is a logging stub: we acknowledge the
// event, log the status transition for visibility, and rely on the fact
// that succeeded → failed transitions cannot happen for card refunds.
//
// When non-card payment methods are enabled (a future task), this handler
// must be expanded to:
//  1. Look up the original credit_transactions 'refund' row by reference_id (charge.ID)
//  2. Reverse the deduction by re-crediting the user's balance and the
//     refund_deficit_credits column (mirror of handleChargeRefunded but
//     with sign flipped)
//  3. Insert a 'refund_reversal' credit_transactions row for audit
//  4. Update stripe_payments.status back from 'refunded' to 'succeeded'
//
// Idempotency: gated via processed_webhook_events under SERIALIZABLE.
//
// Stripe references:
//   - https://docs.stripe.com/api/refunds/object (Refund.status enum)
//   - https://docs.stripe.com/refunds#failed-refunds
//   - https://docs.stripe.com/api/events/types#event_types-refund.updated
func (s *Service) handleRefundUpdated(ctx context.Context, event stripe.Event) (int, error) {
	var refund stripe.Refund
	if err := json.Unmarshal(event.Data.Raw, &refund); err != nil {
		s.logger.Error("failed_to_parse_refund_updated", slog.Any("error", err))
		return 400, fmt.Errorf("failed to parse refund: %w", err)
	}

	chargeID := ""
	if refund.Charge != nil {
		chargeID = refund.Charge.ID
	}
	paymentIntentID := ""
	if refund.PaymentIntent != nil {
		paymentIntentID = refund.PaymentIntent.ID
	}

	// Log every refund.updated event so ops has visibility into the refund
	// lifecycle. ERROR level when status is failed (a real money-loss
	// signal), INFO otherwise.
	logFn := s.logger.Info
	if refund.Status == stripe.RefundStatusFailed {
		logFn = s.logger.Error
	}
	logFn("refund_updated",
		slog.String("refund_id", refund.ID),
		slog.String("charge_id", chargeID),
		slog.String("payment_intent_id", paymentIntentID),
		slog.String("status", string(refund.Status)),
		slog.String("failure_reason", string(refund.FailureReason)),
		slog.Int64("amount_cents", refund.Amount),
	)

	// MVP gate: card-only flows do not produce succeeded → failed transitions.
	// When the failed-refund reversal logic is implemented, the gate below
	// is the place to add it.
	if refund.Status == stripe.RefundStatusFailed {
		s.logger.Error("refund_failed_reversal_not_implemented",
			slog.String("refund_id", refund.ID),
			slog.String("charge_id", chargeID),
			slog.String("detail", "card-only MVP does not produce async refund failures; if this fires, ops must manually reconcile credits"),
		)
	}

	// Idempotency gate so Stripe webhook retries don't double-log.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()
	isDuplicate, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}
	if isDuplicate {
		return 200, nil
	}
	if err := tx.Commit(); err != nil {
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return 200, nil
}

// handleCheckoutAsyncPaymentSucceeded processes checkout.session.async_payment_succeeded
// events. (S-M4)
//
// Background: when a Checkout Session is paid via a delayed payment method
// (Bacs Direct Debit, SEPA Direct Debit, ACH, Boleto, OXXO, Konbini, Multibanco),
// the user is redirected to success_url immediately but the actual money
// movement takes hours-to-days. Stripe fires checkout.session.completed
// right away (with PaymentStatus='unpaid' or 'no_payment_required'), then
// fires either checkout.session.async_payment_succeeded OR
// checkout.session.async_payment_failed once the funds clear.
//
// MVP scope: BrezelScraper currently only enables card payments via the
// Stripe Dashboard. Card flows are synchronous — checkout.session.completed
// arrives with PaymentStatus='paid' and the credit grant happens there.
// This handler is a logging stub for the moment a non-card payment method
// is enabled. When that happens, this handler must be expanded to:
//  1. Look up the stripe_payments row by session_id
//  2. Run the same balance-grant + deficit-paydown SQL flow as
//     handleCheckoutSessionCompleted (extract a shared helper)
//  3. Mark stripe_payments.status='succeeded'
//
// Idempotency: gated via processed_webhook_events under SERIALIZABLE.
//
// Stripe references:
//   - https://docs.stripe.com/api/events/types#event_types-checkout.session.async_payment_succeeded
//   - https://docs.stripe.com/payments/checkout/fulfill-orders
func (s *Service) handleCheckoutAsyncPaymentSucceeded(ctx context.Context, event stripe.Event) (int, error) {
	return s.handleAsyncPaymentEvent(ctx, event, "checkout_async_payment_succeeded")
}

// handleCheckoutAsyncPaymentFailed processes checkout.session.async_payment_failed
// events. (S-M4) — see handleCheckoutAsyncPaymentSucceeded for context.
//
// When implemented for non-card flows, this handler must:
//  1. Mark the stripe_payments row as 'failed'
//  2. NOT touch credit_balance (no credits were granted yet — async flows
//     grant on the success event, not on session.completed)
//  3. Optionally trigger a customer notification
func (s *Service) handleCheckoutAsyncPaymentFailed(ctx context.Context, event stripe.Event) (int, error) {
	return s.handleAsyncPaymentEvent(ctx, event, "checkout_async_payment_failed")
}

// handleAsyncPaymentEvent is the shared logging-stub implementation for both
// checkout.session.async_payment_* events. It parses the session, logs the
// async payment lifecycle event, runs through the idempotency gate, and
// returns 200 — without granting or revoking credits. Card-only MVP only.
func (s *Service) handleAsyncPaymentEvent(ctx context.Context, event stripe.Event, logKey string) (int, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		s.logger.Error("failed_to_parse_async_payment_session", slog.Any("error", err), slog.String("event_type", string(event.Type)))
		return 400, fmt.Errorf("failed to parse session: %w", err)
	}

	s.logger.Info(logKey,
		slog.String("session_id", session.ID),
		slog.String("event_id", event.ID),
		slog.String("payment_status", string(session.PaymentStatus)),
		slog.String("detail", "MVP is card-only; async payment events should not fire in production. Investigate if seen."),
	)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Warn("tx_rollback_failed", slog.Any("error", rbErr))
		}
	}()
	isDuplicate, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}
	if isDuplicate {
		return 200, nil
	}
	if err := tx.Commit(); err != nil {
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return 200, nil
}

// StartWebhookEventCleanup starts a background goroutine that deletes old
// processed_webhook_events rows. It runs daily and on first call, deleting
// rows older than retentionDays (from WEBHOOK_EVENT_RETENTION_DAYS, default 90).
// Deletes in batches of 1000 to avoid long-running transactions.
// Safe to call as a goroutine: recovers from panics and stops when ctx is done.
func (s *Service) StartWebhookEventCleanup(ctx context.Context, retentionDays int) {
	if retentionDays <= 0 {
		retentionDays = 90
	}

	cleanup := func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("webhook_event_cleanup_panic", slog.Any("panic", r))
			}
		}()

		total := 0
		for {
			result, err := s.db.ExecContext(ctx,
				`DELETE FROM processed_webhook_events WHERE event_id IN (
					SELECT event_id FROM processed_webhook_events
					WHERE processed_at < NOW() - INTERVAL '1 day' * $1
					LIMIT 1000
				)`, retentionDays)
			if err != nil {
				s.logger.Error("webhook_event_cleanup_error", slog.Any("error", err))
				break
			}
			n, raErr := result.RowsAffected()
			if raErr != nil {
				s.logger.Warn("webhook_event_cleanup_rows_affected_error", slog.Any("error", raErr))
				break
			}
			total += int(n)
			if n < 1000 {
				break // no more rows to delete in this batch
			}
		}
		if total > 0 {
			s.logger.Info("webhook_event_cleanup_done", slog.Int("deleted", total), slog.Int("retention_days", retentionDays))
		}
	}

	// Run immediately on startup, then daily.
	cleanup()

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
