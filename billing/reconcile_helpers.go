package billing

import (
	"fmt"

	"github.com/stripe/stripe-go/v82"
)

// verifyLineItemsQuantity is a defense-in-depth check used by ReconcileSession.
// It sums the quantity across all expanded line_items returned by Stripe and
// compares against the credits_purchased value recorded locally in
// stripe_payments. A mismatch is a signal that either the DB row was tampered
// with or the original CreateCheckoutSession wrote inconsistent values to
// Stripe vs the DB. Pure function so it can be unit-tested without mocking
// the Stripe SDK.
//
// nil-safe: a nil LineItemList or empty Data slice is treated as "no
// information" and returns nil. The reconcile flow only calls this when
// expand=line_items was set on the Get call, so an empty result means the
// session genuinely has no line items (which would be a different bug
// surfaced elsewhere).
//
// (S-M2)
func verifyLineItemsQuantity(lineItems *stripe.LineItemList, expectedCredits int) error {
	if lineItems == nil || len(lineItems.Data) == 0 {
		return nil
	}
	var totalQty int64
	for _, li := range lineItems.Data {
		totalQty += li.Quantity
	}
	if totalQty != int64(expectedCredits) {
		return fmt.Errorf("line item quantity mismatch: db=%d stripe=%d", expectedCredits, totalQty)
	}
	return nil
}
