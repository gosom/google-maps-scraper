package proxypool

import (
	"testing"
	"time"
)

// TestEndToEnd_AcquireReportLoop_SimulatesProductionScrape walks the full
// production lifecycle of a webrunner scrape from the pool's perspective:
//
//	for each scrape:
//	    lease := pool.Acquire()
//	    ... run job ...
//	    lease.ReportSuccess() | lease.ReportFailure(reason)
//
// It does NOT exercise webrunner.scrapeJob directly (that requires a real
// scrapemate + Postgres + browser context — covered by the manual smoke
// test). Instead, it simulates 50 scrape cycles with a mix of outcomes
// and asserts the terminal pool state is consistent.
//
// Catches refactors that desync Acquire/Report ordering or break the
// cooling → healthy lazy promotion path under realistic load.
func TestEndToEnd_AcquireReportLoop_SimulatesProductionScrape(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://decodo-good-1", "http://decodo-flaky", "http://decodo-good-2"},
		WithClock(clk),
		WithThresholds(3, 10, time.Minute, 30*time.Minute),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Outcome script: the flaky proxy SoftRejects every 5th scrape it
	// handles; the others always succeed. This mirrors a single bad
	// Decodo IP in the rotation while the rest are fine.
	flakyCount := 0
	for range 50 {
		lease, err := p.Acquire()
		if err != nil {
			t.Fatalf("Acquire returned ErrPoolExhausted during a 50-scrape run with 2 healthy proxies — pool selection broken")
		}
		switch lease.URL {
		case "http://decodo-flaky":
			flakyCount++
			if flakyCount%5 == 0 {
				lease.ReportSuccess()
			} else {
				lease.ReportFailure(SoftReject)
			}
		default:
			lease.ReportSuccess()
		}
		// Mimic real wall-clock advancing between jobs so cooling timers
		// expire over the course of the simulation.
		clk.Advance(45 * time.Second)
	}

	s := p.Stats()

	// The two good proxies must remain healthy with non-zero successes.
	for _, e := range s.Entries {
		if e.Host == "decodo-good-1" || e.Host == "decodo-good-2" {
			if e.State != "healthy" {
				t.Errorf("%s should stay healthy across 50 scrapes; got state=%s", e.Host, e.State)
			}
			if e.TotalSuccesses == 0 {
				t.Errorf("%s never served a scrape; rotation skipped it", e.Host)
			}
		}
	}

	// The flaky proxy must NOT be healthy at the end. Whether it cooled
	// or quarantined depends on the exact failure count under round-robin
	// — both outcomes prove the pool is doing its job.
	for _, e := range s.Entries {
		if e.Host == "decodo-flaky" && e.State == "healthy" {
			t.Errorf("decodo-flaky stayed healthy despite repeated SoftRejects (cons=%d cum=%d)",
				e.ConsecutiveFails, e.CumulativeFails)
		}
	}
}

// TestEndToEnd_PoolExhaustion_RecoversAfterClockAdvance proves the
// recovery path under realistic timing: every proxy is cooled (not
// quarantined — that's permanent), then time advances past all cooling
// deadlines, and Acquire starts handing them out again.
//
// This is the "all proxies temporarily struggling" scenario operators
// see during transient outages. The pool must NOT permanently take
// itself offline.
func TestEndToEnd_PoolExhaustion_RecoversAfterClockAdvance(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://a", "http://b"},
		WithClock(clk),
		WithThresholds(3, 100, time.Minute, 30*time.Minute), // high quarantine threshold so we only cool
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Cool both proxies via 3 consecutive failures each.
	failed := map[string]int{"http://a": 0, "http://b": 0}
	for failed["http://a"] < 3 || failed["http://b"] < 3 {
		lease, err := p.Acquire()
		if err != nil {
			break // both cooling
		}
		if failed[lease.URL] < 3 {
			lease.ReportFailure(SoftReject)
			failed[lease.URL]++
		} else {
			lease.ReportSuccess()
		}
	}

	// Right after: both cooling → Acquire should return ErrPoolExhausted.
	if _, err := p.Acquire(); err == nil {
		t.Fatal("expected ErrPoolExhausted with both entries cooling")
	}

	// Advance past the cooling deadline.
	clk.Advance(2 * time.Minute)

	// Recovery: both should be reacquirable now.
	lease, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire after cooling expiry: %v", err)
	}
	lease.ReportSuccess()

	// And the second one too.
	lease, err = p.Acquire()
	if err != nil {
		t.Fatalf("second Acquire after cooling expiry: %v", err)
	}
	lease.ReportSuccess()
}
