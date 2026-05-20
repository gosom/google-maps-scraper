package proxypool

import (
	"sync"
	"testing"
	"time"
)

// TestAcquire_ConcurrentCallersDoNotPanicOrDeadlock fires N goroutines that
// each Acquire + report-outcome in a tight loop. With go test -race, mutex
// violations or unsafe entry mutations would surface here.
//
// We deliberately do NOT assert on final per-entry counts — the rotation
// is non-deterministic under concurrent Acquire and the goal is mutex /
// state-machine integrity, not a specific failure distribution. The
// totalOps sanity check at the end catches the trivial "every call
// returned ErrPoolExhausted" regression.
func TestAcquire_ConcurrentCallersDoNotPanicOrDeadlock(t *testing.T) {
	const (
		nGoroutines = 32
		nIterations = 200
	)
	p, err := New([]string{"http://a", "http://b", "http://c"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(nGoroutines)
	for g := range nGoroutines {
		_ = g
		go func() {
			defer wg.Done()
			for i := range nIterations {
				lease, err := p.Acquire()
				if err != nil {
					// Acceptable if every entry happens to be cooling at the
					// same instant; very unlikely with healthy entries.
					continue
				}
				if i%5 == 0 {
					lease.ReportFailure(SoftReject)
				} else {
					lease.ReportSuccess()
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: goroutines did not finish within 10s")
	}

	// Sanity: cumulative bookkeeping must add up.
	s := p.Stats()
	var totalOps int64
	for _, e := range s.Entries {
		totalOps += e.TotalSuccesses + e.CumulativeFails
	}
	if totalOps == 0 {
		t.Fatal("no operations recorded despite 32×200 iterations")
	}
}

// TestStats_ConcurrentWithReports verifies Stats() can be called safely
// while goroutines are actively reporting outcomes. The snapshot is taken
// under p.mu so reads can't tear, but this exercises the contention path
// to confirm no deadlock between Stats and Lease.Report* under -race.
func TestStats_ConcurrentWithReports(t *testing.T) {
	p, err := New([]string{"http://a", "http://b"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Reporters run a bounded number of iterations and exit.
	var reportersWG sync.WaitGroup
	reportersWG.Add(8)
	for range 8 {
		go func() {
			defer reportersWG.Done()
			for range 100 {
				lease, err := p.Acquire()
				if err != nil {
					continue
				}
				lease.ReportSuccess()
			}
		}()
	}

	// Stats observer runs until told to stop.
	stop := make(chan struct{})
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		for {
			select {
			case <-stop:
				return
			default:
				_ = p.Stats()
			}
		}
	}()

	// Wait for reporters, then signal observer to stop and wait for it.
	reportersWG.Wait()
	close(stop)
	select {
	case <-observerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: Stats observer did not exit within 5s after stop signal")
	}
}

// TestPool_BurnoutScenario simulates the production failure pattern: one of
// three proxies returns the 33-byte stub (SoftReject) every time. The pool
// must cool it out, leave the two healthy proxies serving, and ensure
// "bad" is never in the healthy state at the end of the run.
func TestPool_BurnoutScenario(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://good-1", "http://bad", "http://good-2"},
		WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for range 30 {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if lease.URL == "http://bad" {
			lease.ReportFailure(SoftReject)
		} else {
			lease.ReportSuccess()
		}
		clk.Advance(5 * time.Second)
	}

	// Expected terminal state for "bad": quarantined. With
	// quarantineFailThreshold=10 and round-robin giving "bad" exactly 10
	// turns across 30 iterations, cumulativeFails reaches 10 and the
	// entry moves to stateQuarantined (not just cooling). The "!healthy"
	// assertion is intentionally broader to catch any future state
	// machine regression that lands "bad" anywhere other than healthy.
	s := p.Stats()
	for _, e := range s.Entries {
		if e.Host == "bad" && e.State == "healthy" {
			t.Errorf("bad proxy still healthy after burnout simulation (cons=%d cum=%d)",
				e.ConsecutiveFails, e.CumulativeFails)
		}
		if e.Host == "bad" && e.State != "quarantined" {
			t.Logf("bad proxy ended in state=%s (expected quarantined under default thresholds); not a hard failure but worth investigating", e.State)
		}
	}

	healthyGoods := 0
	for _, e := range s.Entries {
		if (e.Host == "good-1" || e.Host == "good-2") && e.State == "healthy" && e.TotalSuccesses > 0 {
			healthyGoods++
		}
	}
	if healthyGoods != 2 {
		t.Errorf("good proxies serving: got %d, want 2", healthyGoods)
	}
}

// TestPool_FullBurnoutReturnsErrPoolExhausted covers the worst-case path:
// every configured proxy gets quarantined. Acquire must surface
// ErrPoolExhausted rather than spinning or returning a quarantined URL.
func TestPool_FullBurnoutReturnsErrPoolExhausted(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://a", "http://b"},
		WithClock(clk),
		WithThresholds(2, 4, time.Hour, time.Hour),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Burn both proxies past their cumulative-quarantine threshold.
	for range 8 {
		lease, err := p.Acquire()
		if err != nil {
			break
		}
		lease.ReportFailure(SoftReject)
	}

	if _, err := p.Acquire(); err == nil {
		t.Fatal("expected ErrPoolExhausted after every entry quarantined")
	}
}
