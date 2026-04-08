package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stripe/stripe-go/v82"
)

// openTestDB opens a DB connection from PG_TEST_DSN and skips the test if not set.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("Skipping: PG_TEST_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("failed to ping db: %v", err)
	}
	return db
}

// makeTestEvent builds a stripe.Event with the given eventID wrapping a CheckoutSession.
func makeCheckoutCompletedEvent(eventID, sessionID, userID string, credits int) stripe.Event {
	sessionData := map[string]any{
		"id":             sessionID,
		"payment_status": "paid",
		"metadata": map[string]string{
			"user_id":  userID,
			"credits":  fmt.Sprintf("%d", credits),
			"currency": "USD",
		},
	}
	raw, _ := json.Marshal(sessionData)
	return stripe.Event{
		ID:   eventID,
		Type: "checkout.session.completed",
		Data: &stripe.EventData{Raw: json.RawMessage(raw)},
	}
}

// makeCheckoutCompletedEventWithPI is like makeCheckoutCompletedEvent but
// includes a payment_intent field in the session, matching what Stripe
// sends in real checkout.session.completed webhooks.
func makeCheckoutCompletedEventWithPI(eventID, sessionID, paymentIntentID, userID string, credits int) stripe.Event {
	sessionData := map[string]any{
		"id":             sessionID,
		"payment_status": "paid",
		"payment_intent": paymentIntentID, // unexpanded string form as Stripe sends it
		"metadata": map[string]string{
			"user_id":  userID,
			"credits":  fmt.Sprintf("%d", credits),
			"currency": "USD",
		},
	}
	raw, _ := json.Marshal(sessionData)
	return stripe.Event{
		ID:   eventID,
		Type: "checkout.session.completed",
		Data: &stripe.EventData{Raw: json.RawMessage(raw)},
	}
}

// TestWebhookIdempotency_ConcurrentSameEvent asserts that two concurrent calls
// with the same Stripe event ID grant credits exactly once.
// Requires PG_TEST_DSN to be set.
func TestWebhookIdempotency_ConcurrentSameEvent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx := context.Background()

	// Use unique IDs per test run so tests are repeatable.
	// Event IDs must match the chk_event_id_format constraint from migration
	// 000018 which requires ^evt_[a-zA-Z0-9]+$ — no underscores after "evt_".
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eventID := "evt_testconc" + suffix
	sessionID := "cs_test_" + suffix
	userID := "user_test_" + suffix

	// Seed test user with 0 balance
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, credit_balance, email, created_at, updated_at)
		 VALUES ($1, 0, $2, NOW(), NOW())
		 ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@test.invalid")
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(ctx, `DELETE FROM credit_transactions WHERE user_id = $1`, userID)
		db.ExecContext(ctx, `DELETE FROM processed_webhook_events WHERE event_id = $1`, eventID)
		db.ExecContext(ctx, `DELETE FROM stripe_payments WHERE stripe_checkout_session_id = $1`, sessionID)
		db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Seed stripe_payments row so the handler can find the session
	_, err = db.ExecContext(ctx,
		`INSERT INTO stripe_payments (id, user_id, stripe_checkout_session_id, amount_cents, currency, credits_purchased, status)
		 VALUES ($1, $2, $3, 1000, 'USD', 10, 'pending')
		 ON CONFLICT (stripe_checkout_session_id) DO NOTHING`,
		uuid.Must(uuid.NewV7()).String(), userID, sessionID)
	if err != nil {
		t.Fatalf("failed to seed stripe_payments: %v", err)
	}

	svc := &Service{db: db, logger: newTestLogger()}
	event := makeCheckoutCompletedEvent(eventID, sessionID, userID, 10)

	// Launch two goroutines concurrently with the same event
	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			code, _ := svc.handleCheckoutSessionCompleted(ctx, event)
			codes[i] = code
		}()
	}
	wg.Wait()

	// At least one goroutine must succeed with 200. The other may return 200
	// (gracefully deduped via ON CONFLICT) OR 500 (SERIALIZABLE serialization
	// failure SQLSTATE 40001 — which is correct behavior; Stripe will retry
	// the webhook and the retry will hit the idempotency gate cleanly). The
	// invariant we actually care about is "credits granted exactly once",
	// verified by the balance and txn-count assertions below.
	successCount := 0
	for _, code := range codes {
		if code == 200 {
			successCount++
		}
	}
	if successCount == 0 {
		t.Errorf("expected at least one goroutine to return 200, got codes=%v", codes)
	}

	// Credits must be granted exactly once: balance == 10
	var balance float64
	err = db.QueryRowContext(ctx, `SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1`, userID).Scan(&balance)
	if err != nil {
		t.Fatalf("failed to query balance: %v", err)
	}
	if balance != 10 {
		t.Errorf("expected credit_balance=10 (granted exactly once), got %.2f", balance)
	}

	// Exactly one credit_transactions row for this payment
	var txnCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM credit_transactions WHERE user_id = $1 AND reference_id = $2 AND reference_type = 'payment'`,
		userID, sessionID).Scan(&txnCount)
	if err != nil {
		t.Fatalf("failed to query credit_transactions: %v", err)
	}
	if txnCount != 1 {
		t.Errorf("expected 1 credit_transaction row, got %d", txnCount)
	}
}

// TestWebhookIdempotency_SequentialDuplicate verifies that a second call with
// the same event ID after the first succeeds returns 200 with no credit change.
func TestWebhookIdempotency_SequentialDuplicate(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx := context.Background()

	// Event IDs must match the chk_event_id_format constraint from migration
	// 000018 which requires ^evt_[a-zA-Z0-9]+$ — no underscores after "evt_".
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eventID := "evt_testseq" + suffix
	sessionID := "cs_test_seq_" + suffix
	userID := "user_test_seq_" + suffix

	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, credit_balance, email, created_at, updated_at)
		 VALUES ($1, 0, $2, NOW(), NOW())
		 ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@test.invalid")
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(ctx, `DELETE FROM credit_transactions WHERE user_id = $1`, userID)
		db.ExecContext(ctx, `DELETE FROM processed_webhook_events WHERE event_id = $1`, eventID)
		db.ExecContext(ctx, `DELETE FROM stripe_payments WHERE stripe_checkout_session_id = $1`, sessionID)
		db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	_, err = db.ExecContext(ctx,
		`INSERT INTO stripe_payments (id, user_id, stripe_checkout_session_id, amount_cents, currency, credits_purchased, status)
		 VALUES ($1, $2, $3, 1000, 'USD', 10, 'pending')
		 ON CONFLICT (stripe_checkout_session_id) DO NOTHING`,
		uuid.Must(uuid.NewV7()).String(), userID, sessionID)
	if err != nil {
		t.Fatalf("failed to seed stripe_payments: %v", err)
	}

	svc := &Service{db: db, logger: newTestLogger()}
	event := makeCheckoutCompletedEvent(eventID, sessionID, userID, 10)

	// First call: should grant credits
	code1, err1 := svc.handleCheckoutSessionCompleted(ctx, event)
	if code1 != 200 || err1 != nil {
		t.Fatalf("first call: expected 200/nil, got %d/%v", code1, err1)
	}

	// Second call: duplicate, must return 200 and not re-grant
	code2, err2 := svc.handleCheckoutSessionCompleted(ctx, event)
	if code2 != 200 || err2 != nil {
		t.Fatalf("second call: expected 200/nil, got %d/%v", code2, err2)
	}

	var balance float64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("balance query: %v", err)
	}
	if balance != 10 {
		t.Errorf("expected balance=10, got %.2f", balance)
	}
}

// newTestLogger returns a no-op logger for tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestHandleCheckoutSessionCompleted_BackfillsPaymentIntentID verifies that
// when a checkout.session.completed webhook arrives, the session's
// payment_intent ID is written back to the stripe_payments row that was
// created during CreateCheckoutSession. Without this backfill the refund
// handler's primary lookup by stripe_payment_intent_id always misses.
func TestHandleCheckoutSessionCompleted_BackfillsPaymentIntentID(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	// event_id must match ^evt_[a-zA-Z0-9]+$ (chk_event_id_format), so no
	// underscores after the evt_ prefix.
	eventID := "evt_testpi" + suffix
	sessionID := "cs_test_pi_" + suffix
	paymentIntentID := "pi_test_pi_" + suffix
	userID := "user_test_pi_" + suffix

	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, credit_balance, email, created_at, updated_at)
		 VALUES ($1, 0, $2, NOW(), NOW())
		 ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@test.invalid")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(ctx, `DELETE FROM credit_transactions WHERE user_id = $1`, userID)
		db.ExecContext(ctx, `DELETE FROM processed_webhook_events WHERE event_id = $1`, eventID)
		db.ExecContext(ctx, `DELETE FROM stripe_payments WHERE stripe_checkout_session_id = $1`, sessionID)
		db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	_, err = db.ExecContext(ctx,
		`INSERT INTO stripe_payments (id, user_id, stripe_checkout_session_id, amount_cents, currency, credits_purchased, status)
		 VALUES ($1, $2, $3, 1000, 'USD', 10, 'pending')
		 ON CONFLICT (stripe_checkout_session_id) DO NOTHING`,
		uuid.Must(uuid.NewV7()).String(), userID, sessionID)
	if err != nil {
		t.Fatalf("seed stripe_payments: %v", err)
	}

	svc := &Service{db: db, logger: newTestLogger()}
	event := makeCheckoutCompletedEventWithPI(eventID, sessionID, paymentIntentID, userID, 10)

	code, err := svc.handleCheckoutSessionCompleted(ctx, event)
	if err != nil {
		t.Fatalf("handleCheckoutSessionCompleted: %v", err)
	}
	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}

	// Verify the PI ID was backfilled.
	var gotPI sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT stripe_payment_intent_id FROM stripe_payments WHERE stripe_checkout_session_id = $1`,
		sessionID).Scan(&gotPI); err != nil {
		t.Fatalf("query PI: %v", err)
	}
	if !gotPI.Valid {
		t.Fatalf("stripe_payment_intent_id should be backfilled, got NULL")
	}
	if gotPI.String != paymentIntentID {
		t.Errorf("expected PI %q, got %q", paymentIntentID, gotPI.String)
	}
}

// TestHandleCheckoutSessionCompleted_BackfillIsIdempotent verifies that a
// webhook replay (same event ID) does not double-credit and does not
// corrupt the backfilled PI ID.
func TestHandleCheckoutSessionCompleted_BackfillIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	// event_id must match ^evt_[a-zA-Z0-9]+$ (chk_event_id_format).
	eventID := "evt_testreplay" + suffix
	sessionID := "cs_test_replay_" + suffix
	paymentIntentID := "pi_test_replay_" + suffix
	userID := "user_test_replay_" + suffix

	// seed user
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, credit_balance, email, created_at, updated_at)
		 VALUES ($1, 0, $2, NOW(), NOW())
		 ON CONFLICT (id) DO NOTHING`,
		userID, userID+"@test.invalid")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(ctx, `DELETE FROM credit_transactions WHERE user_id = $1`, userID)
		db.ExecContext(ctx, `DELETE FROM processed_webhook_events WHERE event_id = $1`, eventID)
		db.ExecContext(ctx, `DELETE FROM stripe_payments WHERE stripe_checkout_session_id = $1`, sessionID)
		db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// seed pending payment
	_, err = db.ExecContext(ctx,
		`INSERT INTO stripe_payments (id, user_id, stripe_checkout_session_id, amount_cents, currency, credits_purchased, status)
		 VALUES ($1, $2, $3, 1000, 'USD', 10, 'pending')
		 ON CONFLICT (stripe_checkout_session_id) DO NOTHING`,
		uuid.Must(uuid.NewV7()).String(), userID, sessionID)
	if err != nil {
		t.Fatalf("seed stripe_payments: %v", err)
	}

	svc := &Service{db: db, logger: newTestLogger()}
	event := makeCheckoutCompletedEventWithPI(eventID, sessionID, paymentIntentID, userID, 10)

	if code, err := svc.handleCheckoutSessionCompleted(ctx, event); err != nil || code != 200 {
		t.Fatalf("first call: code=%d err=%v", code, err)
	}
	// Replay
	if code, err := svc.handleCheckoutSessionCompleted(ctx, event); err != nil || code != 200 {
		t.Fatalf("replay call: code=%d err=%v", code, err)
	}

	// Balance still 10, not 20
	var balance float64
	if err := db.QueryRowContext(ctx,
		`SELECT credit_balance::float8 FROM users WHERE id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance != 10 {
		t.Errorf("expected balance=10 after replay, got %f", balance)
	}

	// PI ID still present and correct
	var gotPI sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT stripe_payment_intent_id FROM stripe_payments WHERE stripe_checkout_session_id = $1`,
		sessionID).Scan(&gotPI); err != nil {
		t.Fatalf("query PI: %v", err)
	}
	if !gotPI.Valid || gotPI.String != paymentIntentID {
		t.Errorf("PI ID lost or changed on replay: %v / %q", gotPI.Valid, gotPI.String)
	}
}
