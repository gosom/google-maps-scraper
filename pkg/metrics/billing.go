// Package metrics exposes Prometheus counters and gauges for the billing subsystem.
//
// Registered metrics:
//
//   - refund_deficit_applied_total: incremented each time a charge.refunded event
//     creates a refund deficit (the user had already consumed more credits than the
//     refund amount, so the uncollectable remainder is written to
//     users.refund_deficit_credits and will be paid down on the next purchase).
//
// Runbook — refund_deficit_applied_total spike:
//
//  1. Any non-zero rate indicates users buying, consuming, then refunding. This
//     is possibly benign churn (genuine dissatisfaction) or possibly fraud
//     (buy → consume → refund loop). Investigate proportionally.
//
//  2. Query users currently carrying a deficit:
//
//     SELECT u.id, u.email, u.refund_deficit_credits, u.credit_balance
//     FROM users u
//     WHERE u.refund_deficit_credits > 0
//     ORDER BY u.refund_deficit_credits DESC
//     LIMIT 50;
//
//  3. Query the audit ledger for recent deficit events to see the charge trail:
//
//     SELECT u.id, u.email, ct.description, ct.created_at, ct.reference_id
//     FROM credit_transactions ct JOIN users u ON u.id = ct.user_id
//     WHERE ct.type = 'refund_deficit'
//     ORDER BY ct.created_at DESC LIMIT 50;
//
//  4. Cross-reference stripe_payments where status = 'refund_deficit_applied' to
//     find the affected payment intent IDs and the gap between cash refunded
//     and credits deducted from spendable balance.
//
//  5. Decide whether to manually adjust (force-pay the deficit from another
//     source, issue a statement credit, or accept the loss) on a per-user basis.
//     The self-correcting paydown pipeline handles the common case on the next
//     purchase automatically.
//
//  6. If a single user triggers most events, check for Stripe retry storms or
//     an intentional buy → consume → refund abuse pattern.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// BillingMetrics holds Prometheus counters for billing events.
type BillingMetrics struct {
	// RefundDeficitAppliedTotal counts how many times a charge.refunded event
	// created a refund deficit. Each increment corresponds to one Stripe
	// charge.refunded event where the credits owed exceeded the user's
	// remaining spendable balance, and the uncollectable remainder was
	// written to users.refund_deficit_credits for next-purchase paydown.
	RefundDeficitAppliedTotal prometheus.Counter
}

// NewBillingMetrics registers and returns a BillingMetrics instance using the
// provided Prometheus registerer. Passing nil uses the default registry.
// If a metric with the same name is already registered (e.g. because billing.New
// is called more than once in the same process), the existing collector is reused.
func NewBillingMetrics(reg prometheus.Registerer) *BillingMetrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	refundDeficit := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "refund_deficit_applied_total",
		Help: "Total count of charge.refunded events where the credits owed exceeded current balance and a refund deficit was created. Any non-zero rate indicates users buying, consuming, then refunding — possibly benign churn or possibly fraud. Investigate via the ops query in the runbook.",
	})
	if err := reg.Register(refundDeficit); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			// Metric already registered by a prior billing.New call — reuse it.
			refundDeficit = are.ExistingCollector.(prometheus.Counter)
		} else {
			panic(err)
		}
	}
	return &BillingMetrics{
		RefundDeficitAppliedTotal: refundDeficit,
	}
}
