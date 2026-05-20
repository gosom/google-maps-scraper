package proxypool

import (
	"strings"
	"testing"
	"time"
)

func TestStats_ReflectsLiveState(t *testing.T) {
	clk := newFakeClock()
	p, err := New(
		[]string{"http://user:pw@a:1", "http://user:pw@b:2", "http://user:pw@c:3"},
		WithClock(clk),
		WithThresholds(2, 5, time.Minute, 30*time.Minute),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Cool "a" via 2 failures. Round-robin makes the lease land on a
	// every other call, so we loop on a counter to be robust.
	failuresOnA := 0
	for failuresOnA < 2 {
		l, _ := p.Acquire()
		if l.URL == "http://user:pw@a:1" {
			l.ReportFailure(SoftReject)
			failuresOnA++
		} else {
			l.ReportSuccess()
		}
	}

	// Quarantine "b" via BlockedByTarget.
	for {
		l, _ := p.Acquire()
		if l.URL == "http://user:pw@b:2" {
			l.ReportFailure(BlockedByTarget)
			break
		}
		l.ReportSuccess()
	}

	s := p.Stats()
	if s.TotalProxies != 3 {
		t.Errorf("TotalProxies: got %d, want 3", s.TotalProxies)
	}
	if s.Cooling != 1 || s.Quarantined != 1 || s.Healthy != 1 {
		t.Errorf("state counts: healthy=%d cooling=%d quarantined=%d; want 1/1/1",
			s.Healthy, s.Cooling, s.Quarantined)
	}

	// Credential stripping in Host: the URL form "user:pass@host:port"
	// becomes "host:port" — the '@' separator is the unambiguous signal
	// that userinfo survived. ':' alone is fine (port separator).
	for _, e := range s.Entries {
		if e.Host == "" {
			t.Errorf("Host empty for entry: %+v", e)
		}
		if strings.Contains(e.Host, "@") {
			t.Errorf("Host %q leaked credentials (contains @)", e.Host)
		}
	}
}

func TestHostOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"http://gate.decodo.com:10001", "gate.decodo.com:10001"},
		{"http://user:secret@gate.decodo.com:10001", "gate.decodo.com:10001"},
		{"http://user:secret%3Dpw@gate.decodo.com:10001", "gate.decodo.com:10001"},
		{"://malformed", "invalid"},
	}
	for _, c := range cases {
		got := HostOf(c.in)
		if got != c.want {
			t.Errorf("HostOf(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("credentials leaked for input %q: %q", c.in, got)
		}
	}
}
