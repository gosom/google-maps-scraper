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

// TestWebhookIdempotency_ConcurrentSameEvent asserts that two concurrent calls
// with the same Stripe event ID grant credits exactly once.
// Requires PG_TEST_DSN to be set.
func TestWebhookIdempotency_ConcurrentSameEvent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx := context.Background()

	// Use unique IDs per test run so tests are repeatable
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eventID := "evt_test_" + suffix
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

	// Both must return 200 (one granted, one idempotent no-op)
	for i, code := range codes {
		if code != 200 {
			t.Errorf("goroutine %d: expected HTTP 200, got %d", i, code)
		}
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

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	eventID := "evt_test_seq_" + suffix
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
