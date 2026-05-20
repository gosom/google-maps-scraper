package proxypool

import (
	"testing"
	"time"
)

func TestReportSuccess_ResetsConsecutiveFails(t *testing.T) {
	p, err := New([]string{"http://a"}, WithClock(newFakeClock()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	p.mu.Lock()
	p.entries[0].consecutiveFails = 2
	p.mu.Unlock()

	lease, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lease.ReportSuccess()

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].consecutiveFails; got != 0 {
		t.Errorf("consecutiveFails: got %d, want 0", got)
	}
	if got := p.entries[0].totalSuccesses; got != 1 {
		t.Errorf("totalSuccesses: got %d, want 1", got)
	}
}

// TestReportSuccess_DoesNotPromoteStillCoolingEntry covers the
// most-easily-regressed contract: ReportSuccess MUST keep the entry in
// cooling when now < nextOK. Without this test, a future refactor
// dropping the !now.Before(nextOK) guard would silently break cooling.
func TestReportSuccess_DoesNotPromoteStillCoolingEntry(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Cooling with deadline in the future. isUsableLocked will refuse
	// to hand this out, so we Acquire BEFORE flipping the state to keep
	// the test small. The lease holds the *entry pointer, so the post-
	// hoc state change is still observed by ReportSuccess.
	lease, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	p.mu.Lock()
	p.entries[0].state = stateCooling
	p.entries[0].nextOK = clk.Now().Add(time.Hour) // far in the future
	p.mu.Unlock()

	lease.ReportSuccess()

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].state; got != stateCooling {
		t.Errorf("state after ReportSuccess on still-cooling entry: got %s, want cooling", got)
	}
}

// TestReportSuccess_LeavesQuarantinedUntouched verifies that ReportSuccess
// on a quarantined entry mutates NEITHER counters NOR state. The guard
// prevents metric corruption when multiple leases exist on the same entry
// (race window between BlockedByTarget on one lease and Success on another).
func TestReportSuccess_LeavesQuarantinedUntouched(t *testing.T) {
	p, err := New([]string{"http://a"}, WithClock(newFakeClock()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, _ := p.Acquire()
	p.mu.Lock()
	p.entries[0].state = stateQuarantined
	p.entries[0].consecutiveFails = 7 // seed a non-zero value
	p.mu.Unlock()

	lease.ReportSuccess()

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].state; got != stateQuarantined {
		t.Errorf("state: got %s, want quarantined", got)
	}
	if got := p.entries[0].totalSuccesses; got != 0 {
		t.Errorf("totalSuccesses leaked on quarantined entry: got %d, want 0", got)
	}
	if got := p.entries[0].consecutiveFails; got != 7 {
		t.Errorf("consecutiveFails reset on quarantined entry: got %d, want 7 (unchanged)", got)
	}
}

func TestReportSuccess_PromotesCoolingToHealthy(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Cooling with an already-expired deadline so Acquire hands it out.
	p.mu.Lock()
	p.entries[0].state = stateCooling
	p.entries[0].nextOK = clk.Now().Add(-time.Second)
	p.mu.Unlock()

	lease, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lease.ReportSuccess()

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].state; got != stateHealthy {
		t.Errorf("state after success: got %s, want healthy", got)
	}
}

func TestLease_ReportSuccessTwiceIsNoOp(t *testing.T) {
	p, err := New([]string{"http://a"}, WithClock(newFakeClock()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, _ := p.Acquire()
	lease.ReportSuccess()
	lease.ReportSuccess()

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].totalSuccesses; got != 1 {
		t.Errorf("double-report counted: totalSuccesses = %d, want 1", got)
	}
}

func TestReportFailure_BelowCoolingThresholdStaysHealthy(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for range 2 {
		lease, _ := p.Acquire()
		lease.ReportFailure(SoftReject)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].state; got != stateHealthy {
		t.Errorf("after 2 failures (threshold=3): state = %s, want healthy", got)
	}
	if got := p.entries[0].consecutiveFails; got != 2 {
		t.Errorf("consecutiveFails: got %d, want 2", got)
	}
}

func TestReportFailure_AtThresholdCools(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for range 3 {
		lease, _ := p.Acquire()
		lease.ReportFailure(SoftReject)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].state; got != stateCooling {
		t.Errorf("at threshold: state = %s, want cooling", got)
	}
	wantNextOK := clk.Now().Add(time.Minute) // baseCool = 1 min
	if !p.entries[0].nextOK.Equal(wantNextOK) {
		t.Errorf("nextOK: got %v, want %v", p.entries[0].nextOK, wantNextOK)
	}
}

func TestReportFailure_BlockedByTargetJumpsToQuarantine(t *testing.T) {
	p, err := New([]string{"http://a"}, WithClock(newFakeClock()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lease, _ := p.Acquire()
	lease.ReportFailure(BlockedByTarget)

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].state; got != stateQuarantined {
		t.Errorf("BlockedByTarget: state = %s, want quarantined", got)
	}
	// Counter side-effects: ReportFailure increments BOTH counters before
	// the reason branch runs, so even an immediate-quarantine event is
	// recorded as one failure. This is intentional — the entry has failed
	// once, regardless of which path took it off-rotation.
	if got := p.entries[0].consecutiveFails; got != 1 {
		t.Errorf("consecutiveFails after BlockedByTarget: got %d, want 1", got)
	}
	if got := p.entries[0].cumulativeFails; got != 1 {
		t.Errorf("cumulativeFails after BlockedByTarget: got %d, want 1", got)
	}
	if got := p.entries[0].lastFailureReason; got != BlockedByTarget {
		t.Errorf("lastFailureReason: got %s, want blocked_by_target", got)
	}
}

func TestReportFailure_CumulativeQuarantine(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Alternate failure-cycles + clock-advance + success cycles until we
	// accumulate 10 cumulative failures. Each cycle: 3 fails → cooling →
	// clock past deadline → success (resets consecutive but cumulative
	// keeps climbing).
	for range 4 {
		for range 3 {
			lease, err := p.Acquire()
			if err != nil {
				break
			}
			lease.ReportFailure(SoftReject)
		}
		clk.Advance(time.Hour) // past any cooling deadline
		lease, err := p.Acquire()
		if err != nil {
			break
		}
		lease.ReportSuccess()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].cumulativeFails; got < 10 {
		t.Fatalf("cumulativeFails: got %d, want ≥10", got)
	}
	if got := p.entries[0].state; got != stateQuarantined {
		t.Errorf("after %d cumulative fails: state = %s, want quarantined",
			p.entries[0].cumulativeFails, got)
	}
}

// TestAcquire_CoolingEntryReacquirableAfterDeadline (Task 6) is a
// regression test for the cool-then-reacquire path: an entry that was
// cooled by ReportFailure must become acquirable again once enough clock
// time has passed.
func TestAcquire_CoolingEntryReacquirableAfterDeadline(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a", "http://b"}, WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Keep acquiring until we've reported failure on "a" three times.
	// Loop on a counter rather than a fixed iteration count so the test
	// is robust to rotation order.
	failuresOnA := 0
	for failuresOnA < 3 {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("unexpected Acquire error while cooling 'a': %v", err)
		}
		if lease.URL == "http://a" {
			lease.ReportFailure(SoftReject)
			failuresOnA++
		} else {
			lease.ReportSuccess()
		}
	}

	// Right after: only "b" should be acquirable.
	for range 5 {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lease.URL == "http://a" {
			t.Fatalf("cooling entry handed out (URL=%s)", lease.URL)
		}
		lease.ReportSuccess()
	}

	// Advance past the cooling deadline.
	clk.Advance(2 * time.Minute)

	// Now "a" should come back into rotation.
	saw := false
	for range 10 {
		lease, _ := p.Acquire()
		if lease.URL == "http://a" {
			saw = true
		}
		lease.ReportSuccess()
	}
	if !saw {
		t.Fatal("cooling entry never reacquired after clock advance past deadline")
	}
}

// TestCoolDuration_ExponentialBackoffCappedAtMax covers Task 5's overflow
// guard. With baseCool=30s, max=30min, the duration should double each
// cycle until it hits the cap. The guard prevents bit-shift overflow when
// consecutiveFails grows large (overshoot > 30 would wrap int64).
func TestCoolDuration_ExponentialBackoffCappedAtMax(t *testing.T) {
	p := &Pool{
		coolingFailThreshold: 3,
		baseCoolDuration:     30 * time.Second,
		maxCoolDuration:      30 * time.Minute,
	}
	cases := []struct {
		consecutiveFails int
		want             time.Duration
	}{
		{0, 30 * time.Second},    // below threshold → base
		{3, 30 * time.Second},    // at threshold → base (overshoot=0)
		{4, 1 * time.Minute},     // base * 2
		{5, 2 * time.Minute},     // base * 4
		{6, 4 * time.Minute},     // base * 8
		{8, 16 * time.Minute},    // base * 32
		{9, 30 * time.Minute},    // base * 64 = 32min > max → cap (size-cap path)
		{65, 30 * time.Minute},   // overshoot=62 — shift produces large positive → size-cap
		{66, 30 * time.Minute},   // overshoot=63 — shift wraps sign bit → d<=0 path
		{67, 30 * time.Minute},   // overshoot=64 — shift wraps further → d<=0 path
		{1000, 30 * time.Minute}, // far past — same d<=0 path
	}
	for _, c := range cases {
		if got := p.coolDuration(c.consecutiveFails); got != c.want {
			t.Errorf("coolDuration(%d) = %v, want %v", c.consecutiveFails, got, c.want)
		}
	}
}

func TestLease_ReportFailureThenSuccessIsNoOp(t *testing.T) {
	p, err := New([]string{"http://a"}, WithClock(newFakeClock()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lease, _ := p.Acquire()
	lease.ReportFailure(SoftReject)
	lease.ReportSuccess() // must be ignored — failure already recorded

	p.mu.Lock()
	defer p.mu.Unlock()
	if got := p.entries[0].consecutiveFails; got != 1 {
		t.Errorf("ReportSuccess after Failure mutated counters: consecutiveFails = %d, want 1", got)
	}
	if got := p.entries[0].totalSuccesses; got != 0 {
		t.Errorf("ReportSuccess after Failure was not ignored: totalSuccesses = %d, want 0", got)
	}
}
