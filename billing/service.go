package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv" // ADDED: Required for strconv.Atoi on line 195

	"github.com/gosom/google-maps-scraper/config"
	"github.com/stripe/stripe-go/v82"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
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
	// MVP: USD-only
	if req.Currency != "USD" {
		return CheckoutResponse{}, fmt.Errorf("unsupported currency; only USD is enabled in MVP")
	}

	// MVP: fixed $1 per credit
	unitPriceCents := 100

	// Prepare Stripe client
	stripe.Key = s.stripeSecretKey

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
		log.Printf("ERROR: Webhook signing key not configured")
		return 400, errors.New("webhook signing key not configured")
	}

	event, err := webhook.ConstructEvent(payload, signatureHeader, s.webhookSigningKey)
	if err != nil {
		log.Printf("ERROR: Invalid webhook signature: %v", err)
		return 400, fmt.Errorf("invalid signature: %w", err)
	}

	log.Printf("BILLING: Webhook signature verified successfully, event type: %s, event ID: %s", event.Type, event.ID)

	// Idempotency check - prevent duplicate processing
	if s.hasProcessedEvent(ctx, event.ID) {
		log.Printf("BILLING: Event %s already processed, skipping", event.ID)
		return 200, nil
	}

	switch event.Type {
	case "checkout.session.completed":
		return s.handleCheckoutSessionCompleted(ctx, event)
	case "checkout.session.expired":
		return s.handleCheckoutSessionExpired(ctx, event)
	default:
		log.Printf("BILLING: Unhandled event type: %s", event.Type)
		return 200, nil
	}
}

// hasProcessedEvent checks if we've already processed this webhook event
func (s *Service) hasProcessedEvent(ctx context.Context, eventID string) bool {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM processed_webhook_events WHERE event_id = $1)", eventID).Scan(&exists)
	if err != nil {
		log.Printf("ERROR: Failed to check event processing status: %v", err)
		return false // Fail open to allow processing
	}
	return exists
}

// markEventProcessed records that we've processed this webhook event
func (s *Service) markEventProcessed(ctx context.Context, tx *sql.Tx, eventID string) error {
	_, err := tx.ExecContext(ctx, // FIXED: Use passed context instead of context.Background()
		"INSERT INTO processed_webhook_events (event_id, processed_at) VALUES ($1, NOW()) ON CONFLICT (event_id) DO NOTHING",
		eventID)
	return err
}

func (s *Service) handleCheckoutSessionCompleted(ctx context.Context, event stripe.Event) (int, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		log.Printf("ERROR: Failed to parse checkout session: %v", err)
		return 400, fmt.Errorf("failed to parse session: %w", err)
	}

	log.Printf("BILLING: Processing checkout.session.completed for session: %s", session.ID)

	// Verify payment status before processing
	if session.PaymentStatus != "paid" {
		log.Printf("BILLING: Session %s completed but payment status is %s, skipping credit", session.ID, session.PaymentStatus)
		return 200, nil
	}

	// Check if this session was already processed (additional idempotency check)
	var exists bool
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM credit_transactions WHERE reference_id=$1 AND reference_type='payment')",
		session.ID).Scan(&exists)
	if err != nil {
		log.Printf("ERROR: Failed to check transaction existence: %v", err)
		return 500, fmt.Errorf("failed to check transaction existence: %w", err)
	}
	if exists {
		log.Printf("BILLING: Session %s already processed (found in credit_transactions), skipping", session.ID)
		return 200, nil // Already processed
	}

	userID := ""
	credits := 0
	currency := ""

	// First try to get data from database
	const sel = `SELECT user_id, (credits_purchased)::int, currency FROM stripe_payments WHERE stripe_checkout_session_id=$1 LIMIT 1`
	err = s.db.QueryRowContext(ctx, sel, session.ID).Scan(&userID, &credits, &currency)

	if err != nil && err != sql.ErrNoRows {
		log.Printf("ERROR: Database query failed: %v", err)
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
				log.Printf("WARNING: Invalid credits value in metadata: %s", creditsStr)
			}
		}

		currency = session.Metadata["currency"]
	}

	// Validate required data
	if userID == "" {
		log.Printf("WARNING: No user_id found for session %s", session.ID)
		return 200, nil
	}
	if credits <= 0 {
		log.Printf("WARNING: Invalid credits amount %d for session %s", credits, session.ID)
		return 200, nil
	}
	if currency != "USD" {
		log.Printf("WARNING: Unsupported currency %s for session %s", currency, session.ID)
		return 200, nil
	}

	// Process the payment in a transaction with proper isolation level
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		log.Printf("ERROR: Failed to begin transaction: %v", err)
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Get current balance before update for accurate transaction records
	// IMPROVED: Handle potential NULL values with COALESCE
	var currentBalance float64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&currentBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("WARNING: User %s not found", userID)
			return 400, fmt.Errorf("user not found: %s", userID)
		}
		log.Printf("ERROR: Failed to get current user balance: %v", err)
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
		log.Printf("ERROR: Failed to update user credits: %v", err)
		return 500, fmt.Errorf("failed to update user credits: %w", err)
	}

	// Check if user exists
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("ERROR: Failed to get rows affected: %v", err)
		return 500, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		log.Printf("WARNING: No user found with ID %s", userID)
		return 400, fmt.Errorf("user not found: %s", userID)
	}

	// Insert credit transaction with accurate balances
	const insTxn = `INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
					VALUES ($1, 'purchase', $2, $3, $4, $5, $6, 'payment')`
	_, err = tx.ExecContext(ctx, insTxn, userID, credits, currentBalance, currentBalance+float64(credits), "Stripe purchase", session.ID)
	if err != nil {
		log.Printf("ERROR: Failed to insert credit transaction: %v", err)
		return 500, fmt.Errorf("failed to insert credit transaction: %w", err)
	}

	// Mark stripe payment as succeeded
	const updPay = `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`
	_, err = tx.ExecContext(ctx, updPay, session.ID)
	if err != nil {
		log.Printf("ERROR: Failed to update payment status: %v", err)
		return 500, fmt.Errorf("failed to update payment status: %w", err)
	}

	// Mark event as processed for idempotency
	if err := s.markEventProcessed(ctx, tx, event.ID); err != nil {
		log.Printf("ERROR: Failed to mark event as processed: %v", err)
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		log.Printf("ERROR: Failed to commit transaction: %v", err)
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("BILLING: Successfully processed checkout.session.completed for user %s, credits: %d, session: %s", userID, credits, session.ID)
	return 200, nil
}

func (s *Service) handleCheckoutSessionExpired(ctx context.Context, event stripe.Event) (int, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		log.Printf("ERROR: Failed to parse expired session: %v", err)
		return 400, fmt.Errorf("failed to parse session: %w", err)
	}

	log.Printf("BILLING: Processing checkout.session.expired for session: %s", session.ID)

	// Begin transaction for consistency and idempotency
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted, // Less strict isolation for expired sessions
	})
	if err != nil {
		log.Printf("ERROR: Failed to begin transaction for expired session: %v", err)
		return 500, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Update payment status to canceled
	const upd = `UPDATE stripe_payments SET status='canceled', updated_at=NOW() WHERE stripe_checkout_session_id=$1`
	_, err = tx.ExecContext(ctx, upd, session.ID)
	if err != nil {
		log.Printf("ERROR: Failed to update expired payment status: %v", err)
		return 500, fmt.Errorf("failed to update expired payment status: %w", err)
	}

	// Mark event as processed
	if err := s.markEventProcessed(ctx, tx, event.ID); err != nil {
		log.Printf("ERROR: Failed to mark expired event as processed: %v", err)
		return 500, fmt.Errorf("failed to mark event as processed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("ERROR: Failed to commit expired session transaction: %v", err)
		return 500, fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("BILLING: Successfully processed checkout.session.expired for session %s", session.ID)
	return 200, nil
}

// ReconcileSession fetches a Checkout Session from Stripe and applies credits if paid.
// This can be used as a fallback mechanism if webhooks fail.
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
	const sel = `SELECT user_id, (credits_purchased)::int, currency, status FROM stripe_payments WHERE stripe_checkout_session_id=$1 LIMIT 1`
	if err := s.db.QueryRowContext(ctx, sel, sessionID).Scan(&userID, &credits, &currency, &status); err != nil {
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

	// Check if already processed in credit_transactions (idempotency)
	var exists bool
	err = s.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM credit_transactions WHERE reference_id=$1 AND reference_type='payment')",
		sessionID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check transaction existence: %w", err)
	}
	if exists {
		// Update stripe_payments status even if credits already applied
		_, _ = s.db.ExecContext(ctx, `UPDATE stripe_payments SET status='succeeded', completed_at=NOW() WHERE stripe_checkout_session_id=$1`, sessionID)
		return nil
	}

	// Apply credits transactionally
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

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

// CountBillableItems counts reviews, images, and contact details from job results.
// This scans the results table and aggregates counts from JSONB fields.
func (s *Service) CountBillableItems(ctx context.Context, jobID string) (*BillingCounts, error) {
	if s.db == nil {
		return nil, fmt.Errorf("db not configured")
	}

	counts := &BillingCounts{}

	// Query to count billable items from results
	// - user_reviews/user_reviews_extended: JSONB arrays of review objects
	// - images: JSONB array of image objects
	// - emails: TEXT field (non-empty means contact details extracted)
	//
	// IMPORTANT: We must check jsonb_typeof() before calling jsonb_array_length()
	// because jsonb_array_length() will fail if the value is not an array (scalar, object, null, etc.)
	const query = `
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

	err := s.db.QueryRowContext(ctx, query, jobID).Scan(
		&counts.TotalReviews,
		&counts.TotalImages,
		&counts.PlacesWithContacts,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to count billable items: %w", err)
	}

	return counts, nil
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

	// 2. Count and charge for reviews, images, and contacts
	counts, err := s.CountBillableItems(ctx, jobID)
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
