package proxypool

import (
	"errors"
	"testing"
	"time"
)

func TestNew_EmptyURLsReturnsErrEmptyPool(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrEmptyPool) {
		t.Fatalf("nil urls: want ErrEmptyPool, got %v", err)
	}
	if _, err := New([]string{}); !errors.Is(err, ErrEmptyPool) {
		t.Fatalf("empty urls: want ErrEmptyPool, got %v", err)
	}
}

func TestNew_AllEntriesStartHealthy(t *testing.T) {
	urls := []string{"http://a", "http://b", "http://c"}
	p, err := New(urls)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(p.entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(p.entries))
	}
	for i, e := range p.entries {
		if e.state != stateHealthy {
			t.Errorf("entry %d (%s): state = %s, want healthy", i, e.url, e.state)
		}
		if e.consecutiveFails != 0 || e.cumulativeFails != 0 {
			t.Errorf("entry %d: counters should be zero, got cons=%d cum=%d",
				i, e.consecutiveFails, e.cumulativeFails)
		}
	}
}

// fakeClock is a manually-advanced clock for testing cooling timers without
// time.Sleep. Tests construct with newFakeClock and advance via Advance.
type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// TestAcquire_ErrPoolExhaustedAfterAllRemoved locks in the contract that
// Acquire never hands out a non-healthy entry. White-box pattern: the
// state transitions land in Chunk 2 via ReportFailure/ReportSuccess, so
// until then we mutate entry fields directly under p.mu — same lock the
// real transitions will use. Tests do not call t.Parallel(); the
// direct-mutation pattern is intentional and not race-unsafe in serial use.
func TestAcquire_ErrPoolExhaustedAfterAllRemoved(t *testing.T) {
	p, err := New([]string{"http://a"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.mu.Lock()
	p.entries[0].state = stateQuarantined
	p.mu.Unlock()

	if _, err := p.Acquire(); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
}

// TestAcquire_CoolingDeadlineExpired_BecomesUsable exercises the clock
// injection: a cooling entry whose nextOK has elapsed must be handed out
// again (lazy-promotion happens in Chunk 2; for Chunk 1 we only verify
// Acquire selects it). Without this test the entire purpose of WithClock
// is unexercised in Chunk 1.
func TestAcquire_CoolingDeadlineExpired_BecomesUsable(t *testing.T) {
	clk := newFakeClock()
	p, err := New([]string{"http://a"}, WithClock(clk))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.mu.Lock()
	p.entries[0].state = stateCooling
	p.entries[0].nextOK = clk.Now().Add(time.Minute)
	p.mu.Unlock()

	// Before deadline: not usable.
	if _, err := p.Acquire(); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("before deadline: want ErrPoolExhausted, got %v", err)
	}

	clk.Advance(2 * time.Minute)

	// After deadline: handed out.
	lease, err := p.Acquire()
	if err != nil {
		t.Fatalf("after deadline: Acquire: %v", err)
	}
	if lease.URL != "http://a" {
		t.Fatalf("after deadline: got %q, want http://a", lease.URL)
	}
}

// TestAcquire_CursorWrapsOverMixedUsableUnusable proves the ring walk
// correctly skips non-usable entries and lands on the next usable one,
// updating the cursor so the SUBSEQUENT acquire continues from there.
func TestAcquire_CursorWrapsOverMixedUsableUnusable(t *testing.T) {
	p, err := New([]string{"http://a", "http://b", "http://c"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Quarantine a and b; only c is usable.
	p.mu.Lock()
	p.entries[0].state = stateQuarantined
	p.entries[1].state = stateQuarantined
	p.mu.Unlock()

	for i := range 3 {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("call %d: Acquire: %v", i+1, err)
		}
		if lease.URL != "http://c" {
			t.Fatalf("call %d: got %q, want http://c", i+1, lease.URL)
		}
	}
}

func TestAcquire_RoundRobinAcrossHealthyEntries(t *testing.T) {
	urls := []string{"http://a", "http://b", "http://c"}
	p, err := New(urls)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := []string{"http://a", "http://b", "http://c", "http://a", "http://b"}
	for i, expected := range want {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("call %d: Acquire returned error: %v", i+1, err)
		}
		if lease.URL != expected {
			t.Errorf("call %d: got %q, want %q", i+1, lease.URL, expected)
		}
	}
}
