package billing

import (
	"testing"
)

// TestBuildCheckoutSessionParams_SetsClientReferenceID locks in the S-H4
// requirement that the internal user ID is surfaced as the
// client_reference_id on the Stripe Checkout Session, where it appears in
// the Stripe Dashboard for search and reconciliation against our internal DB.
func TestBuildCheckoutSessionParams_SetsClientReferenceID(t *testing.T) {
	t.Parallel()

	req := CheckoutRequest{UserID: "user_test_clientref", Currency: "USD"}
	params := buildCheckoutSessionParams(req, "cus_test", 100, 100, "https://success.example", "https://cancel.example")

	if params.ClientReferenceID == nil {
		t.Fatal("ClientReferenceID must be set, got nil")
	}
	if *params.ClientReferenceID != "user_test_clientref" {
		t.Errorf("ClientReferenceID: want %q, got %q", "user_test_clientref", *params.ClientReferenceID)
	}
	// Stripe enforces a 200-character maximum on client_reference_id; verify
	// our user IDs comfortably fit (Clerk IDs are ~32 chars).
	if len(*params.ClientReferenceID) > 200 {
		t.Errorf("ClientReferenceID exceeds Stripe's 200-char max: %d chars", len(*params.ClientReferenceID))
	}
}

// TestBuildCheckoutSessionParams_SetsPaymentIntentDataMetadata locks in the
// S-H4 requirement that brezel_user_id is set on PaymentIntentData.Metadata
// so it propagates through to the Charge object delivered by charge.refunded
// and charge.dispute.created webhook events. Without this, the dispute
// handler would have to join through stripe_payments to find the user.
func TestBuildCheckoutSessionParams_SetsPaymentIntentDataMetadata(t *testing.T) {
	t.Parallel()

	req := CheckoutRequest{UserID: "user_test_pi_metadata", Currency: "USD"}
	params := buildCheckoutSessionParams(req, "cus_test", 250, 100, "https://success.example", "https://cancel.example")

	if params.PaymentIntentData == nil {
		t.Fatal("PaymentIntentData must be set, got nil")
	}
	if params.PaymentIntentData.Metadata == nil {
		t.Fatal("PaymentIntentData.Metadata must be set, got nil")
	}

	gotUser, ok := params.PaymentIntentData.Metadata["brezel_user_id"]
	if !ok {
		t.Errorf("PaymentIntentData.Metadata missing key %q", "brezel_user_id")
	}
	if gotUser != "user_test_pi_metadata" {
		t.Errorf("brezel_user_id: want %q, got %q", "user_test_pi_metadata", gotUser)
	}

	gotCredits, ok := params.PaymentIntentData.Metadata["credits"]
	if !ok {
		t.Errorf("PaymentIntentData.Metadata missing key %q", "credits")
	}
	if gotCredits != "250" {
		t.Errorf("credits: want %q, got %q", "250", gotCredits)
	}
}

// TestBuildCheckoutSessionParams_SetsPaymentIntentDataDescription verifies
// the human-readable description is populated. Stripe surfaces this in the
// Dashboard payment list view and makes invoice/audit trails clearer.
func TestBuildCheckoutSessionParams_SetsPaymentIntentDataDescription(t *testing.T) {
	t.Parallel()

	req := CheckoutRequest{UserID: "user_test_desc", Currency: "USD"}
	params := buildCheckoutSessionParams(req, "cus_test", 42, 100, "https://success.example", "https://cancel.example")

	if params.PaymentIntentData == nil || params.PaymentIntentData.Description == nil {
		t.Fatal("PaymentIntentData.Description must be set, got nil")
	}
	want := "BrezelScraper Credits x42"
	if *params.PaymentIntentData.Description != want {
		t.Errorf("Description: want %q, got %q", want, *params.PaymentIntentData.Description)
	}
}

// TestBuildCheckoutSessionParams_PreservesBackwardCompatMetadata verifies
// that the top-level Metadata still contains user_id, credits, and currency
// for the existing handleCheckoutSessionCompleted fallback path that reads
// session.Metadata when the stripe_payments DB row is unexpectedly missing.
// Removing this would break the rare-edge-case fallback (S-M1 will eventually
// drop the consumer once we trust the row presence; until then, keep the field).
func TestBuildCheckoutSessionParams_PreservesBackwardCompatMetadata(t *testing.T) {
	t.Parallel()

	req := CheckoutRequest{UserID: "user_test_bc", Currency: "USD"}
	params := buildCheckoutSessionParams(req, "cus_test", 7, 100, "https://success.example", "https://cancel.example")

	if params.Metadata == nil {
		t.Fatal("top-level Metadata must be set, got nil")
	}
	want := map[string]string{
		"user_id":  "user_test_bc",
		"credits":  "7",
		"currency": "USD",
	}
	for k, v := range want {
		if got, ok := params.Metadata[k]; !ok || got != v {
			t.Errorf("Metadata[%q]: want %q, got %q (present=%v)", k, v, got, ok)
		}
	}
}

// TestBuildCheckoutSessionParams_SetsCustomerAndQuantity is a structural
// guard against accidental field deletion in future refactors.
func TestBuildCheckoutSessionParams_SetsCustomerAndQuantity(t *testing.T) {
	t.Parallel()

	req := CheckoutRequest{UserID: "u", Currency: "USD"}
	params := buildCheckoutSessionParams(req, "cus_abc", 50, 100, "https://s", "https://c")

	if params.Customer == nil || *params.Customer != "cus_abc" {
		t.Errorf("Customer: want cus_abc, got %v", params.Customer)
	}
	if len(params.LineItems) != 1 {
		t.Fatalf("expected exactly one line item, got %d", len(params.LineItems))
	}
	li := params.LineItems[0]
	if li.Quantity == nil || *li.Quantity != 50 {
		t.Errorf("Quantity: want 50, got %v", li.Quantity)
	}
	if li.PriceData == nil || li.PriceData.UnitAmount == nil || *li.PriceData.UnitAmount != 100 {
		t.Errorf("UnitAmount: want 100, got %v", li.PriceData)
	}
}
