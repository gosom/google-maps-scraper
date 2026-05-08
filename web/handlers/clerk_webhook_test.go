package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"go.uber.org/goleak"

	"github.com/gosom/google-maps-scraper/postgres"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// testClerkSecret is a valid whsec_-prefixed base64 secret for unit tests.
// The decoded value is "testsecret16byte" (16 bytes), satisfying the minimum
// key-length check added in C2 hardening (clerkWebhookMinKeyBytes = 16).
// base64("testsecret16byte") = "dGVzdHNlY3JldDE2Ynl0ZQ=="
const testClerkSecret = "whsec_dGVzdHNlY3JldDE2Ynl0ZQ=="

// ---------------------------------------------------------------------------
// Fake Provisioner — no DB needed for pure-unit tests.
// ---------------------------------------------------------------------------

type fakeProvisioner struct {
	mu        sync.Mutex
	callCount int
	lastID    string
	lastEmail string
	err       error // if non-nil, Provision returns this error
}

func (f *fakeProvisioner) Provision(_ context.Context, userID, email string) (postgres.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	f.lastID = userID
	f.lastEmail = email
	if f.err != nil {
		return postgres.User{}, f.err
	}
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
	h, err := NewClerkWebhookHandler(db, []string{testClerkSecret}, prov, slog.Default())
	if err != nil {
		t.Fatalf("NewClerkWebhookHandler: %v", err)
	}
	return h
}

// ---------------------------------------------------------------------------
// Test 1 — Bad signature → 401 (pure unit)
// ---------------------------------------------------------------------------

func TestClerkWebhook_BadSignature_401(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// ---------------------------------------------------------------------------
// signedRequestWithMsgID is like signedRequest but uses a caller-supplied
// msgID instead of generating a fresh one. Use this to simulate Svix
// redelivery of the same message (same svix-id, same signature).
// ---------------------------------------------------------------------------

func signedRequestWithMsgID(t *testing.T, body []byte, secret, msgID string) *http.Request {
	t.Helper()
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
	return req
}

// ---------------------------------------------------------------------------
// Test 8 — Provisioning failure → 503 + dedupe row released (Integration)
//
// H1 fix: when Provision returns an error after the dedupe row was claimed,
// the handler must delete the row and respond 503 so Svix retries the delivery.
// ---------------------------------------------------------------------------

func TestClerkWebhook_ProvisioningFailure_Returns503AndReleasesDedupe(t *testing.T) {
	t.Parallel()
	db := openClerkTestDB(t)
	prov := &fakeProvisioner{err: errors.New("db down")}
	h := newTestClerkHandler(t, db, prov)

	userID := "user_" + uuid.NewString()
	emailAddrID := "idn_" + uuid.NewString()
	email := "failtest+" + uuid.NewString() + "@example.com"

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
	// Cleanup is best-effort; the handler should have deleted the row already.
	t.Cleanup(func() {
		db.Exec("DELETE FROM processed_webhook_events WHERE event_id = $1", msgID) //nolint:errcheck
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Expect 503 so Svix triggers a redelivery.
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}

	// Provisioner must have been called exactly once.
	callCount, _, _ := prov.snapshot()
	if callCount != 1 {
		t.Errorf("expected provisioner called once, got %d", callCount)
	}

	// Dedupe row must have been deleted so a redelivery can re-process.
	var rowCount int
	err := db.QueryRow("SELECT COUNT(*) FROM processed_webhook_events WHERE event_id = $1", msgID).Scan(&rowCount)
	if err != nil {
		t.Fatalf("query dedupe row: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("expected dedupe row deleted after provisioning failure, got count=%d", rowCount)
	}
}

// ---------------------------------------------------------------------------
// H8 — Unit tests for primaryEmailFromClerkPayload fallback paths
// ---------------------------------------------------------------------------

func TestPrimaryEmailFromClerkPayload(t *testing.T) {
	t.Parallel()

	// emailEntry mirrors clerkUserCreatedData.EmailAddresses element type.
	type emailEntry = struct {
		ID           string `json:"id"`
		EmailAddress string `json:"email_address"`
	}

	tests := []struct {
		name string
		in   clerkUserCreatedData
		want string
	}{
		{
			name: "primary_id matches an entry",
			in: clerkUserCreatedData{
				PrimaryEmailAddressID: "idn_a",
				EmailAddresses: []emailEntry{
					{ID: "idn_a", EmailAddress: "a@example.com"},
					{ID: "idn_b", EmailAddress: "b@example.com"},
				},
			},
			want: "a@example.com",
		},
		{
			name: "primary_id set but no match — fallback to first",
			in: clerkUserCreatedData{
				PrimaryEmailAddressID: "idn_missing",
				EmailAddresses: []emailEntry{
					{ID: "idn_b", EmailAddress: "b@example.com"},
				},
			},
			want: "b@example.com",
		},
		{
			name: "no primary_id — fallback to first",
			in: clerkUserCreatedData{
				EmailAddresses: []emailEntry{
					{ID: "idn_c", EmailAddress: "c@example.com"},
				},
			},
			want: "c@example.com",
		},
		{
			name: "no emails at all",
			in:   clerkUserCreatedData{},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := primaryEmailFromClerkPayload(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M13 — Concurrent dedupe: two simultaneous deliveries of the same svix-id
// must result in exactly one Provision call (Integration)
// ---------------------------------------------------------------------------

func TestClerkWebhook_DedupesByMessageID_Concurrent(t *testing.T) {
	t.Parallel()
	db := openClerkTestDB(t)
	fp := &fakeProvisioner{}
	h := newTestClerkHandler(t, db, fp)

	userID := "user_" + uuid.NewString()
	emailAddrID := "idn_" + uuid.NewString()
	email := "concurrent_dedupe+" + uuid.NewString() + "@example.com"

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

	// Generate a single msgID shared by both requests to simulate concurrent
	// Svix redelivery of the same message.
	msgID := "msg_" + uuid.NewString()
	t.Cleanup(func() {
		db.Exec("DELETE FROM processed_webhook_events WHERE event_id = $1", msgID) //nolint:errcheck
	})

	rec1, rec2 := httptest.NewRecorder(), httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.ServeHTTP(rec1, signedRequestWithMsgID(t, body, testClerkSecret, msgID))
	}()
	go func() {
		defer wg.Done()
		h.ServeHTTP(rec2, signedRequestWithMsgID(t, body, testClerkSecret, msgID))
	}()
	wg.Wait()

	// Both responses must be 200 (one is a fresh process, the other a dedupe hit).
	if rec1.Code != http.StatusOK || rec2.Code != http.StatusOK {
		t.Errorf("both must be 200, got %d / %d", rec1.Code, rec2.Code)
	}
	// Provisioner must have been called exactly once despite the race.
	count, _, _ := fp.snapshot()
	if count != 1 {
		t.Errorf("provisioner must be called exactly once under concurrent dedupe, got %d", count)
	}
}
