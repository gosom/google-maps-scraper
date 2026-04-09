package billing

import (
	"strings"
	"testing"

	"github.com/stripe/stripe-go/v82"
)

// makeLineItemList builds a *stripe.LineItemList from a slice of quantities.
// Each quantity becomes one LineItem; the rest of the fields are zero-valued
// because verifyLineItemsQuantity only reads .Quantity.
func makeLineItemList(quantities ...int64) *stripe.LineItemList {
	items := make([]*stripe.LineItem, 0, len(quantities))
	for _, q := range quantities {
		items = append(items, &stripe.LineItem{Quantity: q})
	}
	return &stripe.LineItemList{Data: items}
}

func TestVerifyLineItemsQuantity_Match(t *testing.T) {
	t.Parallel()
	if err := verifyLineItemsQuantity(makeLineItemList(100), 100); err != nil {
		t.Errorf("expected nil for matching quantity, got: %v", err)
	}
}

func TestVerifyLineItemsQuantity_MultipleLineItems(t *testing.T) {
	t.Parallel()
	// 30 + 40 + 30 = 100
	if err := verifyLineItemsQuantity(makeLineItemList(30, 40, 30), 100); err != nil {
		t.Errorf("expected nil for matching summed quantity, got: %v", err)
	}
}

func TestVerifyLineItemsQuantity_StripeOverDB(t *testing.T) {
	t.Parallel()
	// Stripe says 200, DB says 100 — Stripe charged the user MORE than we
	// recorded. Bail out before granting credits.
	err := verifyLineItemsQuantity(makeLineItemList(200), 100)
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected error to mention 'mismatch', got: %v", err)
	}
	if !strings.Contains(err.Error(), "db=100") || !strings.Contains(err.Error(), "stripe=200") {
		t.Errorf("expected error to include both db= and stripe= values, got: %v", err)
	}
}

func TestVerifyLineItemsQuantity_StripeUnderDB(t *testing.T) {
	t.Parallel()
	// Stripe says 50, DB says 100 — we recorded MORE than Stripe charged.
	// Equally bad: granting 100 credits would over-credit the user.
	err := verifyLineItemsQuantity(makeLineItemList(50), 100)
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
}

func TestVerifyLineItemsQuantity_NilList(t *testing.T) {
	t.Parallel()
	// Nil LineItemList is treated as "no information" → no error. The
	// caller only invokes this after AddExpand("line_items"), so a nil
	// result would be a separate bug surfaced elsewhere.
	if err := verifyLineItemsQuantity(nil, 100); err != nil {
		t.Errorf("expected nil for nil LineItemList, got: %v", err)
	}
}

func TestVerifyLineItemsQuantity_EmptyData(t *testing.T) {
	t.Parallel()
	if err := verifyLineItemsQuantity(&stripe.LineItemList{}, 100); err != nil {
		t.Errorf("expected nil for empty Data slice, got: %v", err)
	}
}

func TestVerifyLineItemsQuantity_ZeroExpected(t *testing.T) {
	t.Parallel()
	// Edge case: DB recorded 0 credits (shouldn't happen due to CHECK
	// constraint, but verify the math doesn't divide-by-zero or off-by-one).
	// Should mismatch if Stripe has any line items.
	err := verifyLineItemsQuantity(makeLineItemList(1), 0)
	if err == nil {
		t.Fatal("expected mismatch error for db=0 stripe=1, got nil")
	}
}
