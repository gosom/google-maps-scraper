package billing

import (
	"testing"

	"github.com/shopspring/decimal"
)

func decimalMust(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("decimal.NewFromString(%q): %v", s, err)
	}
	return d
}

func TestComputeRefundSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		balance        string
		creditsGranted string
		amountCents    int64
		refundedCents  int64
		wantDeducted   string // .StringFixed(2)
		wantDeficit    string // .StringFixed(2)
	}{
		{"full_refund_full_balance", "100", "100", 10000, 10000, "100.00", "0.00"},
		{"partial_refund_full_balance", "100", "100", 10000, 5000, "50.00", "0.00"},
		{"full_refund_partial_balance", "5", "100", 10000, 10000, "5.00", "95.00"},
		{"partial_refund_partial_balance", "30", "100", 10000, 5000, "30.00", "20.00"},
		{"zero_balance_creates_full_deficit", "0", "100", 10000, 10000, "0.00", "100.00"},

		// Edge cases
		{"zero_amount_cents_returns_zero", "100", "100", 0, 5000, "0.00", "0.00"},
		{"zero_credits_granted_returns_zero", "100", "0", 10000, 5000, "0.00", "0.00"},
		{"refund_larger_than_original_caps_at_balance", "5", "100", 10000, 20000, "5.00", "195.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			balance := decimalMust(t, tt.balance)
			creditsGranted := decimalMust(t, tt.creditsGranted)
			deducted, deficit := computeRefundSplit(balance, creditsGranted, tt.amountCents, tt.refundedCents)

			if deducted.StringFixed(2) != tt.wantDeducted {
				t.Errorf("deducted: want %s, got %s", tt.wantDeducted, deducted.StringFixed(2))
			}
			if deficit.StringFixed(2) != tt.wantDeficit {
				t.Errorf("deficit: want %s, got %s", tt.wantDeficit, deficit.StringFixed(2))
			}
		})
	}
}

// TestComputeRefundSplit_FractionalCredits exercises a 6-decimal precision
// case where float would drift. 100 credits with a 1/3 refund should produce
// exactly 33.333333 (6dp) deficit, not the IEEE 754 33.33333333333333... approximation.
func TestComputeRefundSplit_FractionalCredits(t *testing.T) {
	t.Parallel()
	balance := decimalMust(t, "0")
	creditsGranted := decimalMust(t, "100")
	// refund 1/3 of original
	deducted, deficit := computeRefundSplit(balance, creditsGranted, 30000, 10000)
	if deducted.StringFixed(6) != "0.000000" {
		t.Errorf("deducted: want 0.000000, got %s", deducted.StringFixed(6))
	}
	if deficit.StringFixed(6) != "33.333333" {
		t.Errorf("deficit: want 33.333333, got %s", deficit.StringFixed(6))
	}
}

// TestComputeRefundSplit_NoFloatDrift verifies that the decimal-based math
// doesn't accumulate float drift on a proportion that's a hard case for
// IEEE 754: 0.1 + 0.2 = 0.30000000000000004 in float, but 0.30 in decimal.
func TestComputeRefundSplit_NoFloatDrift(t *testing.T) {
	t.Parallel()
	balance := decimalMust(t, "0")
	creditsGranted := decimalMust(t, "10")
	// refund 30% of original — float would drift on this
	deducted, deficit := computeRefundSplit(balance, creditsGranted, 1000, 300)
	if deducted.StringFixed(6) != "0.000000" {
		t.Errorf("deducted: want 0.000000, got %s", deducted.StringFixed(6))
	}
	if deficit.StringFixed(6) != "3.000000" {
		t.Errorf("deficit: want 3.000000 (no float drift), got %s", deficit.StringFixed(6))
	}
}
