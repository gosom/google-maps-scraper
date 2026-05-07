package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/google/uuid"
	svix "github.com/svix/svix-webhooks/go"

	"github.com/gosom/google-maps-scraper/postgres"
)

// testClerkSecret is a valid whsec_-prefixed base64 secret for unit tests.
// base64("testsecret") = "dGVzdHNlY3JldA=="
const testClerkSecret = "whsec_dGVzdHNlY3JldA=="

// ---------------------------------------------------------------------------
// Fake Provisioner — no DB needed for pure-unit tests.
// ---------------------------------------------------------------------------

type fakeProvisioner struct {
	mu        sync.Mutex
	callCount int
	lastID    string
	lastEmail string
}

func (f *fakeProvisioner) Provision(_ context.Context, userID, email string) (postgres.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	f.lastID = userID
	f.lastEmail = email
	return postgres.User{ID: userID, Email: email}, nil
}

// snapshot returns a consistent copy of the recorded call state for assertions.
func (f *fakeProvisioner) snapshot() (count int, id, email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount, f.lastID, f.lastEmail
}

// ---------------------------------------------------------------------------
// signedRequest builds an *http.Request with a valid Svix signature over body.
// Returns the request and the generated svix-id for dedupe cleanup.
// Uses VerifyIgnoringTimestamp-compatible Sign so the handler's Verify passes.
// ---------------------------------------------------------------------------

func signedRequest(t *testing.T, body []byte, secret string) (*http.Request, string) {
	t.Helper()
	msgID := "msg_" + uuid.NewString()
	ts := time.Now()
	wh, err := svix.NewWebhook(secret)
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	sig, err := wh.Sign(msgID, ts, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", bytes.NewReader(body))
	req.Header.Set("svix-id", msgID)
	req.Header.Set("svix-timestamp", strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set("svix-signature", sig)
	return req, msgID
}

// ---------------------------------------------------------------------------
// openClerkTestDB opens DB from PG_TEST_DSN; skips if unset.
// ---------------------------------------------------------------------------

func openClerkTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("Skipping: PG_TEST_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestClerkHandler builds a ClerkWebhookHandler suitable for tests.
// Panics if construction fails (misconfigured test).
func newTestClerkHandler(t *testing.T, db *sql.DB, prov Provisioner) *ClerkWebhookHandler {
	t.Helper()
	h, err := NewClerkWebhookHandler(db, testClerkSecret, prov, slog.Default())
	if err != nil {
		t.Fatalf("NewClerkWebhookHandler: %v", err)
	}
	return h
}

// ---------------------------------------------------------------------------
// Test 1 — Bad signature → 401 (pure unit)
// ---------------------------------------------------------------------------

func TestClerkWebhook_BadSignature_401(t *testing.T) {
	prov := &fakeProvisioner{}
	h := newTestClerkHandler(t, nil, prov)

	body := []byte(`{"type":"user.created","data":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", bytes.NewReader(body))
	req.Header.Set("svix-id", "msg_"+uuid.NewString())
	req.Header.Set("svix-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("svix-signature", "v1,badsignature==")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	count, _, _ := prov.snapshot()
	if count != 0 {
		t.Errorf("expected provisioner not called, got %d calls", count)
	}
}

// ---------------------------------------------------------------------------
// Test 2 — Missing svix-* headers → 401 (pure unit)
// ---------------------------------------------------------------------------

func TestClerkWebhook_MissingSvixHeaders_401(t *testing.T) {
	prov := &fakeProvisioner{}
	h := newTestClerkHandler(t, nil, prov)

	body := []byte(`{"type":"user.created","data":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", bytes.NewReader(body))
	// Intentionally no svix-* headers

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	count, _, _ := prov.snapshot()
	if count != 0 {
		t.Errorf("expected provisioner not called, got %d calls", count)
	}
}

// ---------------------------------------------------------------------------
// Test 3 — Malformed JSON after valid signature → 400 + dedupe row persisted
// (Integration-gated)
//
// Trade-off: we persist the dedupe row BEFORE parsing JSON to avoid double
// processing on the success path. This means a malformed-body redelivery
// returns 200 (dedupe hit), but malformed JSON from Clerk is not a real-world
// failure mode for a stable provider, so this is the right trade-off.
// ---------------------------------------------------------------------------

func TestClerkWebhook_MalformedJSON_400_AfterVerify(t *testing.T) {
	db := openClerkTestDB(t)
	prov := &fakeProvisioner{}
	h := newTestClerkHandler(t, db, prov)

	body := []byte(`not-valid-json`)
	req, msgID := signedRequest(t, body, testClerkSecret)
	t.Cleanup(func() {
		db.Exec("DELETE FROM processed_webhook_events WHERE event_id = $1", msgID) //nolint:errcheck
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	callCount, _, _ := prov.snapshot()
	if callCount != 0 {
		t.Errorf("expected provisioner not called, got %d calls", callCount)
	}

	// Verify the dedupe row IS persisted (a redelivery would be a no-op 200).
	var rowCount int
	err := db.QueryRow("SELECT COUNT(*) FROM processed_webhook_events WHERE event_id = $1", msgID).Scan(&rowCount)
	if err != nil {
		t.Fatalf("query dedupe row: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("expected dedupe row to be persisted (count=1), got %d", rowCount)
	}
}

// ---------------------------------------------------------------------------
// Test 4 — user.created happy path → 200 + provisioner called (Integration)
// ---------------------------------------------------------------------------

func TestClerkWebhook_UserCreated_HappyPath_200(t *testing.T) {
	db := openClerkTestDB(t)
	prov := &fakeProvisioner{}
	h := newTestClerkHandler(t, db, prov)

	userID := "user_" + uuid.NewString()
	emailAddrID := "idn_" + uuid.NewString()
	email := "testuser+" + uuid.NewString() + "@example.com"

	payload := map[string]interface{}{
		"type": "user.created",
		"data": map[string]interface{}{
			"id":                       userID,
			"primary_email_address_id": emailAddrID,
			"email_addresses": []map[string]interface{}{
				{"id": emailAddrID, "email_address": email},
			},
		},
	}
	body, _ := json.Marshal(payload)
	req, msgID := signedRequest(t, body, testClerkSecret)
	t.Cleanup(func() {
		db.Exec("DELETE FROM processed_webhook_events WHERE event_id = $1", msgID) //nolint:errcheck
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	count, lastID, lastEmail := prov.snapshot()
	if count != 1 {
		t.Errorf("expected provisioner called once, got %d", count)
	}
	if lastID != userID {
		t.Errorf("expected provisioner called with userID=%q, got %q", userID, lastID)
	}
	if lastEmail != email {
		t.Errorf("expected provisioner called with email=%q, got %q", email, lastEmail)
	}
}

// ---------------------------------------------------------------------------
// Test 5 — Dedupe: same message twice → provisioner called once (Integration)
// ---------------------------------------------------------------------------

func TestClerkWebhook_DedupesByMessageID(t *testing.T) {
	db := openClerkTestDB(t)
	prov := &fakeProvisioner{}
	h := newTestClerkHandler(t, db, prov)

	userID := "user_" + uuid.NewString()
	emailAddrID := "idn_" + uuid.NewString()
	email := "dedupetest+" + uuid.NewString() + "@example.com"

	payload := map[string]interface{}{
		"type": "user.created",
		"data": map[string]interface{}{
			"id":                       userID,
			"primary_email_address_id": emailAddrID,
			"email_addresses": []map[string]interface{}{
				{"id": emailAddrID, "email_address": email},
			},
		},
	}
	body, _ := json.Marshal(payload)

	// Build both requests with the SAME msgID (simulate Svix redelivery).
	msgID := "msg_" + uuid.NewString()
	ts := time.Now()
	wh, err := svix.NewWebhook(testClerkSecret)
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	sig, err := wh.Sign(msgID, ts, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", bytes.NewReader(body))
		req.Header.Set("svix-id", msgID)
		req.Header.Set("svix-timestamp", strconv.FormatInt(ts.Unix(), 10))
		req.Header.Set("svix-signature", sig)
		return req
	}

	t.Cleanup(func() {
		db.Exec("DELETE FROM processed_webhook_events WHERE event_id = $1", msgID) //nolint:errcheck
	})

	// First delivery.
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, makeReq())
	if rr1.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rr1.Code)
	}
	count, _, _ := prov.snapshot()
	if count != 1 {
		t.Errorf("first request: expected provisioner called once, got %d", count)
	}

	// Second delivery (redelivery of same message).
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, makeReq())
	if rr2.Code != http.StatusOK {
		t.Errorf("second request: expected 200, got %d", rr2.Code)
	}
	count, _, _ = prov.snapshot()
	if count != 1 {
		t.Errorf("second request: provisioner should not be called again, got %d total calls", count)
	}
}

// ---------------------------------------------------------------------------
// Test 6 — Unknown event type → 200 no-op (Integration)
// ---------------------------------------------------------------------------

func TestClerkWebhook_UnknownEventType_200_NoOp(t *testing.T) {
	db := openClerkTestDB(t)
	prov := &fakeProvisioner{}
	h := newTestClerkHandler(t, db, prov)

	payload := map[string]interface{}{
		"type": "session.created",
		"data": map[string]interface{}{"id": "sess_123"},
	}
	body, _ := json.Marshal(payload)
	req, msgID := signedRequest(t, body, testClerkSecret)
	t.Cleanup(func() {
		db.Exec("DELETE FROM processed_webhook_events WHERE event_id = $1", msgID) //nolint:errcheck
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	count, _, _ := prov.snapshot()
	if count != 0 {
		t.Errorf("expected provisioner not called for unknown event type, got %d calls", count)
	}
}

// ---------------------------------------------------------------------------
// Test 7 — user.created with no email addresses → 200 no-op (Integration)
// ---------------------------------------------------------------------------

func TestClerkWebhook_UserCreatedNoEmail_200_NoOp(t *testing.T) {
	db := openClerkTestDB(t)
	prov := &fakeProvisioner{}
	h := newTestClerkHandler(t, db, prov)

	payload := map[string]interface{}{
		"type": "user.created",
		"data": map[string]interface{}{
			"id":                       "user_noemail_" + uuid.NewString(),
			"primary_email_address_id": "",
			"email_addresses":          []interface{}{},
		},
	}
	body, _ := json.Marshal(payload)
	req, msgID := signedRequest(t, body, testClerkSecret)
	t.Cleanup(func() {
		db.Exec("DELETE FROM processed_webhook_events WHERE event_id = $1", msgID) //nolint:errcheck
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	count, _, _ := prov.snapshot()
	if count != 0 {
		t.Errorf("expected provisioner not called when no email, got %d calls", count)
	}
}
