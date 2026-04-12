package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/config"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// newBillingHandlersForTest builds a BillingHandlers wired with a real
// billing.Service that has a nil DB and nil userRepo — this is intentional.
// The tests below exercise the credit-validation path which runs before any
// DB writes, user lookups, or Stripe API calls, so neither dependency is
// dereferenced. parseCreditsStrict rejects the test inputs (above-cap,
// trailing garbage, empty) before CreateCheckoutSession reaches the user
// lookup added in S-C3.
//
// STRIPE_SUCCESS_URL / STRIPE_CANCEL_URL are set by the caller so that the
// config.Service hits the env-override path and never touches the nil DB.
func newBillingHandlersForTest() *BillingHandlers {
	cfgSvc := config.New(nil)
	billingSvc := billing.New(nil, cfgSvc, "", nil, nil)
	return &BillingHandlers{
		Deps: Dependencies{
			BillingSvc: billingSvc,
			Auth:       &auth.AuthMiddleware{},
		},
	}
}

func billingPostReq(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/checkout", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestCreateCheckoutSession_RejectsAboveCap verifies that a credits value
// above MaxCreditsPerCheckoutSession is rejected with 400 at the handler
// layer (task S-C1).
func TestCreateCheckoutSession_RejectsAboveCap(t *testing.T) {
	t.Setenv("STRIPE_SUCCESS_URL", "https://example.com/success")
	t.Setenv("STRIPE_CANCEL_URL", "https://example.com/cancel")

	h := newBillingHandlersForTest()
	req := billingPostReq(`{"credits":"100000","currency":"USD"}`)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()

	h.CreateCheckoutSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for above-cap credits, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateCheckoutSession_RejectsTrailingGarbage verifies that credits
// with trailing garbage ("1000 garbage") is rejected with 400 — the old
// fmt.Sscan parser silently accepted this and billed Stripe for 1000 credits.
func TestCreateCheckoutSession_RejectsTrailingGarbage(t *testing.T) {
	t.Setenv("STRIPE_SUCCESS_URL", "https://example.com/success")
	t.Setenv("STRIPE_CANCEL_URL", "https://example.com/cancel")

	h := newBillingHandlersForTest()
	req := billingPostReq(`{"credits":"1000 garbage","currency":"USD"}`)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()

	h.CreateCheckoutSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for trailing-garbage credits, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateCheckoutSession_RejectsEmptyCredits verifies that an empty-string
// credits value is rejected with 400. This case was previously caught by a
// now-deleted dead guard in billing.Service.CreateCheckoutSession; the test
// ensures parseCreditsStrict still catches it post-refactor. Empty is the
// most common bad input (user clicks purchase without entering an amount).
func TestCreateCheckoutSession_RejectsEmptyCredits(t *testing.T) {
	t.Setenv("STRIPE_SUCCESS_URL", "https://example.com/success")
	t.Setenv("STRIPE_CANCEL_URL", "https://example.com/cancel")

	h := newBillingHandlersForTest()
	req := billingPostReq(`{"credits":"","currency":"USD"}`)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()

	h.CreateCheckoutSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty credits, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateCheckoutSession_RejectsUnknownFields verifies the S-L2 hardening:
// requests with extra JSON fields are rejected with 422 instead of being
// silently accepted. This prevents request-smuggling and confusion-attack
// vectors where a client sends fields the server ignores.
func TestCreateCheckoutSession_RejectsUnknownFields(t *testing.T) {
	t.Setenv("STRIPE_SUCCESS_URL", "https://example.com/success")
	t.Setenv("STRIPE_CANCEL_URL", "https://example.com/cancel")

	h := newBillingHandlersForTest()
	// Adds an extra "admin":true field that doesn't exist on
	// checkoutSessionRequest. Pre-S-L2 this was silently dropped.
	req := billingPostReq(`{"credits":"100","currency":"USD","admin":true}`)
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()

	h.CreateCheckoutSession(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for unknown fields, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestReconcile_RejectsUnknownFields covers the same hardening on the
// reconcile endpoint.
func TestReconcile_RejectsUnknownFields(t *testing.T) {
	h := newBillingHandlersForTest()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/reconcile",
		bytes.NewBufferString(`{"session_id":"cs_test","extra_field":"oops"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, "user-1")
	rec := httptest.NewRecorder()

	h.Reconcile(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for unknown fields, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}
