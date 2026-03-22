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

	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/pkg/metrics"
	"github.com/stripe/stripe-go/v82"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

type Service struct {
	db                *sql.DB
	cfg               *config.Service
	webhookSigningKey string
	logger            *slog.Logger
	metrics           *metrics.BillingMetrics
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

func New(db *sql.DB, cfg *config.Service, stripeSecretKey, webhookSigningKey string) *Service {
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
		logger:            pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "billing"),
		metrics:           metrics.NewBillingMetrics(nil), // uses default Prometheus registry
	}
}

// CreateCheckoutSession creates a Stripe Checkout Session for purchasing credits.
func (s *Service) CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error) {
	if req.UserID == "" {
		return CheckoutResponse{}, fmt.Errorf("missing user id")
	}
	if req.Credits == "" || req.Credits == "0" {
		return CheckoutResponse{}, fmt.Errorf("credits must be > 0")
	}
	// MVP: USD-only
	if req.Currency != "USD" {
		return CheckoutResponse{}, fmt.Errorf("unsupported currency; only USD is enabled in MVP")
	}

	// MVP: fixed $1 per credit
	unitPriceCents := 100

	// Build success/cancel URLs from config (env overrides allowed)
	successURL, _ := s.cfg.GetString(ctx, "stripe_success_url", "https://example.com/success")
	cancelURL, _ := s.cfg.GetString(ctx, "stripe_cancel_url", "https://example.com/cancel")

	// For Stripe MVP: accept only whole credits as quantity
	var creditsInt int
	if _, err := fmt.Sscan(req.Credits, &creditsInt); err != nil || creditsInt <= 0 {
		return CheckoutResponse{}, fmt.Errorf("only whole credits are supported")
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

	userID := ""
	credits := 0
	currency := ""

	// First try to get data from database
	const sel = `SELECT user_id, (credits_purchased)::int, currency FROM stripe_payments WHERE stripe_checkout_session_id=$1 LIMIT 1`
	err := s.db.QueryRowContext(ctx, sel, session.ID).Scan(&userID, &credits, &currency)

	if err != nil && err != sql.ErrNoRows {
		s.logger.Error("database_query_failed", slog.Any("error", err))
		return 500, fmt.Errorf("database query failed: %w", err)
	}

	// Fallback to metadata with proper error handling
	if userID == "" && session.Metadata != nil {
		userID = session.Metadata["user_id"]

		// Safe conversion of credits with error handling
		if creditsStr, exists := session.Metadata["credits"]; exists {
			if parsedCredits, err := strconv.Atoi(creditsStr); err == nil {
				credits = parsedCredits
			} else {
				s.logger.Warn("invalid_credits_in_metadata", slog.String("credits_str", creditsStr))
			}
		}

		currency = session.Metadata["currency"]
	}

	// Validate required data
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
	defer func() { _ = tx.Rollback() }()

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

	// Get current balance before update for accurate transaction records
	var currentBalance float64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			s.logger.Warn("user_not_found", slog.String("user_id", userID))
			return 400, fmt.Errorf("user not found: %s", userID)
		}
		s.logger.Error("failed_to_get_user_balance", slog.Any("error", err))
		return 500, fmt.Errorf("failed to get user balance: %w", err)
	}

	// Update user credit balance
	const updUser = `UPDATE users SET
		credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
		total_credits_purchased = COALESCE(total_credits_purchased, 0) + $1::numeric,
		updated_at=NOW()
		WHERE id=$2`
	result, err := tx.ExecContext(ctx, updUser, credits, userID)
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

	// Insert credit transaction with accurate balances
	const insTxn = `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
					VALUES ($1, 'purchase', $2, $3, $4, $5, $6, 'payment')`
	_, err = tx.ExecContext(ctx, insTxn, userID, credits, currentBalance, currentBalance+float64(credits), "Stripe purchase", session.ID)
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
	defer func() { _ = tx.Rollback() }()

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
	defer func() { _ = tx.Rollback() }()

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

	// Get current balance with row lock
	var currentBalance float64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&currentBalance)
	if err != nil {
		return fmt.Errorf("failed to get user balance: %w", err)
	}

	// Update balances using SQL arithmetic with NULL safety
	const updUser = `UPDATE users SET 
		credit_balance = COALESCE(credit_balance, 0) + $1::numeric, 
		total_credits_purchased = COALESCE(total_credits_purchased, 0) + $1::numeric, 
		updated_at=NOW() 
		WHERE id=$2`
	if _, err := tx.ExecContext(ctx, updUser, credits, userID); err != nil {
		return fmt.Errorf("failed to update user credits: %w", err)
	}

	// Insert credit transaction with accurate balances
	const insTxn = `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type) 
		VALUES ($1, 'purchase', $2, $3, $4, $5, $6, 'payment')`
	if _, err := tx.ExecContext(ctx, insTxn, userID, credits, currentBalance, currentBalance+float64(credits), "Stripe purchase (reconcile)", sessionID); err != nil {
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
	metaJSON, _ := json.Marshal(metadata)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Insert billing event; trigger resolves pricing and totals
	var (
		eventID               string
		unitPrice, totalPrice string // scan as text to preserve precision
	)
	const insEvent = `INSERT INTO billing_events (user_id, job_id, event_type_code, quantity, metadata)
	                  VALUES ($1,$2,$3,$4,$5::jsonb)
	                  ON CONFLICT (job_id, event_type_code, (metadata->>'idempotency_key'))
	                  WHERE (metadata ? 'idempotency_key')
	                  DO UPDATE SET quantity = billing_events.quantity
	                  RETURNING id, unit_price_credits::text, total_price_credits::text`
	if err := tx.QueryRowContext(ctx, insEvent, userID, jobID, eventType, quantity, string(metaJSON)).Scan(&eventID, &unitPrice, &totalPrice); err != nil {
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
		user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type, metadata
	) VALUES (
		$1, 'consumption', -$2::numeric,
		($3::numeric + $2::numeric),
		$3::numeric,
		$4, $5, 'job', jsonb_build_object('billing_event_id', $6::text)
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
	defer func() { _ = tx.Rollback() }() // Rollback if we don't commit

	// Helper function to charge an event within this transaction
	chargeEventInTx := func(eventType string, quantity int, idempotencyKey string) error {
		if quantity <= 0 {
			return nil // Skip if nothing to charge
		}

		metadata := map[string]any{"idempotency_key": idempotencyKey}
		metaJSON, _ := json.Marshal(metadata)

		// Insert billing event
		var eventID, unitPrice, totalPrice string
		const insEvent = `INSERT INTO billing_events (user_id, job_id, event_type_code, quantity, metadata)
			VALUES ($1,$2,$3,$4,$5::jsonb)
			ON CONFLICT (job_id, event_type_code, (metadata->>'idempotency_key'))
			WHERE (metadata ? 'idempotency_key')
			DO UPDATE SET quantity = billing_events.quantity
			RETURNING id, unit_price_credits::text, total_price_credits::text`

		if err := tx.QueryRowContext(ctx, insEvent, userID, jobID, eventType, quantity, string(metaJSON)).Scan(&eventID, &unitPrice, &totalPrice); err != nil {
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
			user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type, metadata
		) VALUES (
			$1, 'consumption', -$2::numeric,
			($3::numeric + $2::numeric),
			$3::numeric,
			$4, $5, 'job', jsonb_build_object('billing_event_id', $6::text)
		)`
		if _, err := tx.ExecContext(ctx, insTxn, userID, totalPrice, newBalance, fmt.Sprintf("Billing charge: %s", eventType), jobID, eventID); err != nil {
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
	var (
		userID         string
		creditsGranted float64
		amountCents    int64
	)

	paymentIntentID := ""
	if charge.PaymentIntent != nil {
		paymentIntentID = charge.PaymentIntent.ID
	}

	if paymentIntentID != "" {
		const sel = `SELECT user_id, credits_purchased::float8, amount_cents FROM stripe_payments WHERE stripe_payment_intent_id = $1 LIMIT 1`
		err := s.db.QueryRowContext(ctx, sel, paymentIntentID).Scan(&userID, &creditsGranted, &amountCents)
		if err != nil && err != sql.ErrNoRows {
			s.logger.Error("failed_to_lookup_payment_for_charge", slog.Any("error", err))
			return 500, fmt.Errorf("failed to lookup payment: %w", err)
		}
	}

	// Fallback: try to look up via customer ID on the users table.
	if userID == "" && charge.Customer != nil && charge.Customer.ID != "" {
		const sel = `SELECT id FROM users WHERE stripe_customer_id = $1 LIMIT 1`
		_ = s.db.QueryRowContext(ctx, sel, charge.Customer.ID).Scan(&userID)
	}

	if userID == "" {
		s.logger.Warn("charge_refunded_no_user_found",
			slog.String("charge_id", charge.ID),
			slog.String("payment_intent_id", paymentIntentID),
		)
		return 200, nil
	}

	// Calculate credits to deduct proportionally.
	// If we couldn't determine creditsGranted or amountCents, skip the deduction.
	var creditsToDeduct float64
	if creditsGranted > 0 && amountCents > 0 {
		creditsToDeduct = (float64(charge.AmountRefunded) / float64(amountCents)) * creditsGranted
	}

	// Begin transaction for idempotency and credit deduction.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		s.logger.Error("failed_to_begin_transaction_charge_refunded", slog.Any("error", err))
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	isDuplicate, err := s.markEventProcessed(ctx, tx, event.ID, string(event.Type))
	if err != nil {
		s.logger.Error("failed_to_mark_charge_refunded_processed", slog.Any("error", err))
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}
	if isDuplicate {
		s.logger.Info("event_already_processed", slog.String("event_id", event.ID))
		return 200, nil
	}

	if creditsToDeduct > 0 {
		// Get current balance with a row lock — scan as text and convert to
		// integer micro-credits to avoid IEEE 754 float rounding errors in
		// monetary comparisons.
		var balanceStr string
		err = tx.QueryRowContext(ctx,
			"SELECT COALESCE(credit_balance, 0)::text FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&balanceStr)
		if err != nil {
			s.logger.Error("failed_to_get_user_balance_for_refund", slog.Any("error", err))
			return 500, fmt.Errorf("failed to get user balance: %w", err)
		}
		balanceFloat, parseErr := strconv.ParseFloat(balanceStr, 64)
		if parseErr != nil {
			s.logger.Error("failed_to_parse_user_balance_for_refund", slog.Any("error", parseErr))
			return 500, fmt.Errorf("failed to parse user balance: %w", parseErr)
		}
		balanceMicro := int64(math.Round(balanceFloat * models.MicroUnit))
		deductMicro := int64(math.Round(creditsToDeduct * models.MicroUnit))

		// Cap deduction at current balance to prevent negative balances.
		//
		// Trade-off: credits may have been consumed between purchase and refund.
		// For example, a user buys 100 credits, spends 95, then refunds $50 (= 50 credits).
		// Their remaining balance is only 5 — so we deduct 5 instead of 50. The remaining
		// 45 credits were already consumed and cannot be reclaimed here.
		//
		// This is financially safe (no negative balances) but creates an invisible discrepancy:
		// Stripe refunds cash, but we can only recover credits proportional to what's left.
		// The difference is tracked via the refund_partial_cap payment status and the warning
		// log below. Ops should monitor refund_cap_applied frequency to detect abuse patterns.
		//
		// Edge case: if the user has 0 credits (fully consumed), actualDeductMicro = 0.
		// This is acceptable — document it as expected and monitor via the warn log.
		//
		// Idempotency: the markEventProcessed gate (processed_webhook_events) at the top of
		// this transaction prevents double-deductions on Stripe webhook retries. The counter
		// below is therefore also idempotency-safe — it is only reached once per Stripe event.
		//
		// Alerting: the refund_cap_applied_total Prometheus counter is incremented below.
		// A Grafana alert fires when this counter exceeds the REFUND_CAP_ALERT_THRESHOLD
		// (default 5 events in 24 h). See pkg/metrics/billing.go for the runbook.
		actualDeductMicro := deductMicro
		capApplied := false
		if actualDeductMicro > balanceMicro {
			s.metrics.RefundCapAppliedTotal.Inc()
			s.logger.Warn("refund_cap_applied",
				slog.String("user_id", userID),
				slog.String("charge_id", charge.ID),
				slog.Float64("expected_deduction", creditsToDeduct),
				slog.Float64("actual_deduction", float64(balanceMicro)/models.MicroUnit),
				slog.Float64("balance", balanceFloat),
			)
			actualDeductMicro = balanceMicro
			capApplied = true
		}

		newBalanceMicro := balanceMicro - actualDeductMicro

		// Convert back to float for DB storage and transaction logging.
		actualDeductFloat := float64(actualDeductMicro) / models.MicroUnit
		newBalanceFloat := float64(newBalanceMicro) / models.MicroUnit

		// Deduct credits from user balance.
		const updUser = `UPDATE users SET credit_balance = $1::numeric, updated_at = NOW() WHERE id = $2`
		if _, err := tx.ExecContext(ctx, updUser, newBalanceFloat, userID); err != nil {
			s.logger.Error("failed_to_deduct_credits_for_refund", slog.Any("error", err))
			return 500, fmt.Errorf("failed to deduct credits: %w", err)
		}

		// Record the refund transaction.
		const insTxn = `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
		                VALUES ($1, 'refund', $2, $3, $4, $5, $6, 'payment')`
		desc := fmt.Sprintf("Stripe refund for charge %s", charge.ID)
		if _, err := tx.ExecContext(ctx, insTxn, userID, -actualDeductFloat, balanceFloat, newBalanceFloat, desc, charge.ID); err != nil {
			s.logger.Error("failed_to_insert_refund_transaction", slog.Any("error", err))
			return 500, fmt.Errorf("failed to insert refund transaction: %w", err)
		}

		// Update stripe_payments record if we have a payment intent ID.
		// If the cap was applied and the uncollectable credit gap is > 20 credits
		// (20_000_000 micro-credits), flag the payment for manual ops review.
		if paymentIntentID != "" {
			paymentStatus := "refunded"
			const capThresholdMicro int64 = 20 * models.MicroUnit
			if capApplied && (deductMicro-actualDeductMicro) > capThresholdMicro {
				// Significant cap: user retained >20 credits they effectively got for free.
				// Flag for manual ops review queue.
				paymentStatus = "refund_partial_cap"
			}
			_, _ = tx.ExecContext(ctx,
				`UPDATE stripe_payments SET status = $1, refunded_amount_cents = $2, updated_at = NOW() WHERE stripe_payment_intent_id = $3`,
				paymentStatus, charge.AmountRefunded, paymentIntentID)
		}
	}

	if err := tx.Commit(); err != nil {
		s.logger.Error("failed_to_commit_charge_refunded", slog.Any("error", err))
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.logger.Info("charge_refunded_processed",
		slog.String("user_id", userID),
		slog.String("charge_id", charge.ID),
		slog.Float64("credits_deducted", creditsToDeduct),
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
		_ = s.db.QueryRowContext(ctx, "SELECT id FROM users WHERE stripe_customer_id = $1 LIMIT 1", charge.Customer.ID).Scan(&userID)
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
			n, _ := result.RowsAffected()
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
