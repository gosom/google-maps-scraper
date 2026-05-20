package gmaps

import "testing"

// TestReviewEmptyCount_ReflectsAtomic locks in the contract that the
// exported reader returns the current value of the package-level counter.
// The webrunner reads this at job end; if a refactor accidentally caches
// the value or returns a stale snapshot, proxy-outcome classification
// silently breaks.
func TestReviewEmptyCount_ReflectsAtomic(t *testing.T) {
	ResetReviewCircuitBreaker()
	if got := ReviewEmptyCount(); got != 0 {
		t.Fatalf("after reset: got %d, want 0", got)
	}
	reviewEmptyCount.Add(2)
	if got := ReviewEmptyCount(); got != 2 {
		t.Errorf("after Add(2): got %d, want 2", got)
	}
	ResetReviewCircuitBreaker()
	if got := ReviewEmptyCount(); got != 0 {
		t.Errorf("after second reset: got %d, want 0", got)
	}
}

// TestReviewCircuitBreakerThreshold_Stable confirms the exported
// threshold matches the package-level constant. Locks in the value so a
// silent change to the global doesn't drift away from webrunner's
// classification logic.
func TestReviewCircuitBreakerThreshold_Stable(t *testing.T) {
	if got := ReviewCircuitBreakerThreshold(); got != reviewCircuitBreakerThreshold {
		t.Errorf("ReviewCircuitBreakerThreshold(): got %d, want %d (matching the package-internal constant)",
			got, reviewCircuitBreakerThreshold)
	}
	if got := ReviewCircuitBreakerThreshold(); got != 3 {
		t.Errorf("ReviewCircuitBreakerThreshold(): got %d, want 3 (current production threshold)", got)
	}
}
