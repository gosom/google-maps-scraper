package billing

import (
	"testing"
	"time"
)

func TestCheckoutIdempotencyKey_StableWithinHour(t *testing.T) {
	base := time.Date(2026, 4, 9, 14, 0, 0, 0, time.UTC)
	a := checkoutIdempotencyKey("u1", 100, "USD", base)
	b := checkoutIdempotencyKey("u1", 100, "USD", base.Add(45*time.Minute))
	if a != b {
		t.Errorf("expected stable key within the same hour, got %q vs %q", a, b)
	}
}

func TestCheckoutIdempotencyKey_DifferentAcrossHourBoundary(t *testing.T) {
	a := checkoutIdempotencyKey("u1", 100, "USD", time.Date(2026, 4, 9, 14, 59, 59, 0, time.UTC))
	b := checkoutIdempotencyKey("u1", 100, "USD", time.Date(2026, 4, 9, 15, 0, 0, 0, time.UTC))
	if a == b {
		t.Errorf("expected key to change across hour boundary, got identical %q", a)
	}
}

func TestCheckoutIdempotencyKey_DifferentForDifferentUsers(t *testing.T) {
	base := time.Date(2026, 4, 9, 14, 0, 0, 0, time.UTC)
	a := checkoutIdempotencyKey("u1", 100, "USD", base)
	b := checkoutIdempotencyKey("u2", 100, "USD", base)
	if a == b {
		t.Errorf("expected different keys for different users")
	}
}

func TestCheckoutIdempotencyKey_DifferentForDifferentCredits(t *testing.T) {
	base := time.Date(2026, 4, 9, 14, 0, 0, 0, time.UTC)
	a := checkoutIdempotencyKey("u1", 100, "USD", base)
	b := checkoutIdempotencyKey("u1", 200, "USD", base)
	if a == b {
		t.Errorf("expected different keys for different credit amounts")
	}
}

func TestCheckoutIdempotencyKey_DifferentForDifferentCurrency(t *testing.T) {
	base := time.Date(2026, 4, 9, 14, 0, 0, 0, time.UTC)
	a := checkoutIdempotencyKey("u1", 100, "USD", base)
	b := checkoutIdempotencyKey("u1", 100, "EUR", base)
	if a == b {
		t.Errorf("expected different keys for different currencies")
	}
}

func TestCheckoutIdempotencyKey_FormatStable(t *testing.T) {
	// Lock the format so any change is intentional and forces a test update.
	base := time.Date(2026, 4, 9, 14, 30, 0, 0, time.UTC)
	got := checkoutIdempotencyKey("user-abc", 100, "USD", base)
	// Bucket unix timestamp = time.Date(2026, 4, 9, 14, 0, 0, 0, time.UTC).Unix()
	// — verified by running the test and pasting the actual value.
	want := "checkout:user-abc:100:USD:1775743200"
	if got != want {
		t.Errorf("format drift: got %q, want %q", got, want)
	}
}
