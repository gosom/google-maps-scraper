package billing

import (
	"github.com/shopspring/decimal"
)

// computeRefundSplit computes the proportional credit deduction for a Stripe
// refund and splits it across (deductFromBalance, deductFromDeficit). It is
// pure: no DB access, no globals, no time. The split rule is:
//
//	creditsToDeduct  = (refundedCents / amountCents) * creditsGranted, rounded to 6 dp
//	deductFromBalance = min(creditsToDeduct, balance)
//	deductFromDeficit = creditsToDeduct - deductFromBalance
//
// All arithmetic is performed in decimal.Decimal to avoid float64 drift on
// proportional refunds against non-round dollar amounts. Round-to-6 matches
// the database column scale (NUMERIC(18,6)).
//
// Edge cases:
//   - amountCents <= 0 or creditsGranted.IsZero(): returns (0, 0). The caller
//     should not have invoked the function in this case but the guard is
//     defensive.
//   - balance < 0 is impossible by DB CHECK constraint, but the math would
//     still produce a valid (deduct=0, deficit=creditsToDeduct) split if it
//     ever did.
//
// (S-H2)
func computeRefundSplit(
	balance, creditsGranted decimal.Decimal,
	amountCents, refundedCents int64,
) (deductFromBalance, deductFromDeficit decimal.Decimal) {
	if amountCents <= 0 || creditsGranted.IsZero() {
		return decimal.Zero, decimal.Zero
	}
	refunded := decimal.NewFromInt(refundedCents)
	original := decimal.NewFromInt(amountCents)
	creditsToDeduct := refunded.Div(original).Mul(creditsGranted).Round(6)

	if creditsToDeduct.LessThanOrEqual(balance) {
		return creditsToDeduct, decimal.Zero
	}
	return balance, creditsToDeduct.Sub(balance)
}
