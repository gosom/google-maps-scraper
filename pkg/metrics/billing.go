// Package metrics exposes Prometheus counters and gauges for the billing subsystem.
//
// Registered metrics:
//
//   - refund_cap_applied_total: incremented each time the refund credit cap fires
//     (user had fewer credits than the refund amount, so they retain free credits).
//
// Runbook — refund_cap_applied_total spike:
//
//  1. Query: SELECT u.id, u.email, ct.amount, ct.created_at
//     FROM credit_transactions ct JOIN users u ON u.id = ct.user_id
//     WHERE ct.type = 'refund' AND ct.description LIKE '%partial%'
//     ORDER BY ct.created_at DESC LIMIT 50;
//
//  2. Cross-reference stripe_payments where status = 'refund_partial_cap' to find
//     the affected payment intent IDs and the uncollectable credit gap.
//
//  3. Decide whether to manually reconcile (deduct remaining credits, issue statement
//     credit, or accept the loss) on a per-user basis.
//
//  4. If a single user triggers most events, check for Stripe retry storms or abuse.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// BillingMetrics holds Prometheus counters for billing events.
type BillingMetrics struct {
	// RefundCapAppliedTotal counts how many times the refund credit cap fired.
	// Each increment corresponds to one charge.refunded Stripe event where the user's
	// remaining credit balance was less than the credits owed for the refund.
	RefundCapAppliedTotal prometheus.Counter
}

// NewBillingMetrics registers and returns a BillingMetrics instance using the
// provided Prometheus registerer. Passing nil uses the default registry.
// If a metric with the same name is already registered (e.g. because billing.New
// is called more than once in the same process), the existing collector is reused.
func NewBillingMetrics(reg prometheus.Registerer) *BillingMetrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	refundCap := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "refund_cap_applied_total",
		Help: "Total number of refund events where the credit deduction was capped " +
			"because the user's balance was lower than the refund credit amount. " +
			"A sustained spike indicates users retaining credits they should not have.",
	})
	if err := reg.Register(refundCap); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			// Metric already registered by a prior billing.New call — reuse it.
			refundCap = are.ExistingCollector.(prometheus.Counter)
		} else {
			panic(err)
		}
	}
	return &BillingMetrics{
		RefundCapAppliedTotal: refundCap,
	}
}
