package billing

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxCreditsPerCheckoutSession is the hard ceiling on a single Stripe
// checkout session. This is a fraud-control limit, not a pricing limit —
// customers who legitimately need more credits must issue multiple sessions.
// Aligned with Stripe's own pattern (list API limits capped at 100;
// GitHub's per_page at 100; every large public billable API ships a ceiling).
const MaxCreditsPerCheckoutSession = 10_000

// parseCreditsStrict converts a user-supplied credits string to an int.
// Unlike fmt.Sscan it rejects trailing garbage (e.g. "1000 garbage"), decimal
// values ("10.5"), and leading/trailing whitespace is trimmed but inner
// whitespace is rejected.
func parseCreditsStrict(s string) (int, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("credits is required")
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("credits must be a whole positive integer")
	}
	if n <= 0 {
		return 0, fmt.Errorf("credits must be > 0")
	}
	if n > MaxCreditsPerCheckoutSession {
		return 0, fmt.Errorf("credits exceeds maximum of %d per checkout session", MaxCreditsPerCheckoutSession)
	}
	return n, nil
}
