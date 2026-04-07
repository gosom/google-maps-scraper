package services

import "testing"

func TestIsAllowedBillingHistoryType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  bool
	}{
		// Empty = "no filter" is explicitly allowed
		{"empty means no filter", "", true},

		// All 5 values from the DB CHECK constraint (migration 000012)
		{"purchase is allowed", "purchase", true},
		{"consumption is allowed", "consumption", true},
		{"bonus is allowed", "bonus", true},
		{"refund is allowed", "refund", true},
		{"adjustment is allowed", "adjustment", true},

		// Rejections
		{"unknown token rejected", "foo", false},
		{"uppercase rejected (case sensitive)", "PURCHASE", false},
		{"plural rejected (matches frontend foot-gun check)", "purchases", false},
		{"whitespace rejected", " bonus", false},
		{"sql injection attempt rejected", "'; DROP TABLE credit_transactions; --", false},
		{"unicode rejected", "bönus", false},
		{"null byte rejected", "bonus\x00", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsAllowedBillingHistoryType(tc.input)
			if got != tc.want {
				t.Errorf("IsAllowedBillingHistoryType(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestAllowedTypesMatchEnum is a contract test: the in-memory allowlist
// must exactly match the DB CHECK constraint on credit_transactions.type
// (see scripts/migrations/000012_add_credit_system.up.sql line 35).
// If a new type is added to the DB enum, this test forces a matching
// update here — otherwise that type would be silently rejected.
func TestAllowedTypesMatchEnum(t *testing.T) {
	t.Parallel()

	// The DB enum, verbatim from migration 000012.
	expected := []string{"purchase", "consumption", "bonus", "refund", "adjustment"}

	if len(allowedBillingHistoryTypes) != len(expected) {
		t.Fatalf("allowedBillingHistoryTypes has %d entries, expected %d (see migration 000012)",
			len(allowedBillingHistoryTypes), len(expected))
	}

	for _, tp := range expected {
		if _, ok := allowedBillingHistoryTypes[tp]; !ok {
			t.Errorf("allowedBillingHistoryTypes missing %q — is migration 000012 out of sync?", tp)
		}
	}
}
