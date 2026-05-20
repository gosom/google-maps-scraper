package proxypool

import (
	"errors"
	"sync"
	"time"
)

// ErrPoolExhausted is returned by Acquire when every entry in the pool is
// either cooling or quarantined. Callers should surface this as a hard
// failure for the current scrape — there is no healthy proxy to use.
var ErrPoolExhausted = errors.New("proxypool: all proxies unavailable")

// ErrEmptyPool is returned by New when constructed with zero URLs. We treat
// this as a programmer error rather than a runtime state because the
// alternative — silently succeeding then returning ErrPoolExhausted on the
// first Acquire — masks misconfiguration.
var ErrEmptyPool = errors.New("proxypool: cannot construct pool with zero URLs")

// Default thresholds. Override via WithThresholds.
const (
	defaultCoolingFailThreshold    = 3
	defaultQuarantineFailThreshold = 10
	defaultBaseCoolDuration        = 30 * time.Second
	defaultMaxCoolDuration         = 30 * time.Minute
)

// Pool is a thread-safe rotating pool of proxy URLs with per-proxy health
// tracking. Construct with New. Callers Acquire a Lease, use the URL, and
// report success/failure exactly once on the Lease before discarding it.
type Pool struct {
	mu      sync.Mutex
	entries []*entry
	cursor  int // round-robin starting index for the next Acquire

	coolingFailThreshold    int
	quarantineFailThreshold int
	baseCoolDuration        time.Duration
	maxCoolDuration         time.Duration

	clock clock
}

// New constructs a Pool over the supplied proxy URLs. Returns ErrEmptyPool
// when urls is nil or empty.
func New(urls []string, opts ...Option) (*Pool, error) {
	if len(urls) == 0 {
		return nil, ErrEmptyPool
	}
	p := &Pool{
		entries:                 make([]*entry, 0, len(urls)),
		coolingFailThreshold:    defaultCoolingFailThreshold,
		quarantineFailThreshold: defaultQuarantineFailThreshold,
		baseCoolDuration:        defaultBaseCoolDuration,
		maxCoolDuration:         defaultMaxCoolDuration,
		clock:                   realClock{},
	}
	for _, opt := range opts {
		opt(p)
	}
	now := p.clock.Now()
	for _, u := range urls {
		p.entries = append(p.entries, &entry{
			url:              u,
			state:            stateHealthy,
			lastTransitionAt: now,
		})
	}
	return p, nil
}

// Acquire returns a Lease for the next healthy proxy in round-robin order,
// skipping cooling and quarantined entries. A cooling entry whose nextOK
// has passed is treated as healthy for selection purposes (lazy promotion
// happens when the lease is reported successful — see Lease.ReportSuccess).
//
// Returns ErrPoolExhausted when no entry is available.
//
// Callers MUST call exactly one of Lease.ReportSuccess or
// Lease.ReportFailure before discarding the returned Lease.
func (p *Pool) Acquire() (*Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// New guarantees len(entries) >= 1 and nothing in the pool's lifecycle
	// removes entries, so the range below always inspects at least one
	// candidate. An empty slice would naturally fall through to the trailing
	// ErrPoolExhausted return — no separate empty-pool branch needed.
	n := len(p.entries)
	now := p.clock.Now()

	// Walk the ring starting at cursor; return the first usable entry.
	for i := range n {
		idx := (p.cursor + i) % n
		e := p.entries[idx]
		if p.isUsableLocked(e, now) {
			p.cursor = (idx + 1) % n
			return &Lease{URL: e.url, pool: p, e: e}, nil
		}
	}
	return nil, ErrPoolExhausted
}

// coolDuration returns the cooling deadline offset for an entry whose
// consecutive failure count just reached the supplied value. The base
// duration doubles per cooling cycle (consecutiveFails - threshold + 1)
// up to maxCoolDuration. Capped via the overflow check so a long bad
// streak can't overflow time.Duration (int64 ns).
//
// Overflow guard explained: time.Duration is int64. A left-shift by 63+
// wraps the sign bit, yielding either zero or a negative value — the
// `d <= 0` case catches both. A shift by a smaller amount can still
// produce a value larger than maxCoolDuration without overflow; the
// `d > p.maxCoolDuration` case caps those. Either way the entry cools
// for at most maxCoolDuration. Go's shift-by-non-constant is defined
// behavior, unlike C, but the result is implementation-bounded — the
// guard makes the intent explicit and self-documenting.
func (p *Pool) coolDuration(consecutiveFails int) time.Duration {
	overshoot := consecutiveFails - p.coolingFailThreshold
	if overshoot < 0 {
		overshoot = 0
	}
	d := p.baseCoolDuration << overshoot
	if d <= 0 || d > p.maxCoolDuration {
		return p.maxCoolDuration
	}
	return d
}

// Stats returns a snapshot of the current pool state. Safe to call
// concurrently with Acquire and lease reporting — the snapshot is
// copy-on-read under p.mu.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()

	s := Stats{
		TotalProxies: len(p.entries),
		Entries:      make([]EntryStats, 0, len(p.entries)),
	}
	for _, e := range p.entries {
		switch e.state {
		case stateHealthy:
			s.Healthy++
		case stateCooling:
			s.Cooling++
		case stateQuarantined:
			s.Quarantined++
		}
		es := EntryStats{
			Host:             HostOf(e.url),
			State:            e.state.String(),
			ConsecutiveFails: e.consecutiveFails,
			CumulativeFails:  e.cumulativeFails,
			TotalSuccesses:   e.totalSuccesses,
			LastTransitionAt: e.lastTransitionAt,
		}
		if e.state == stateCooling {
			es.NextOK = e.nextOK
		}
		if e.cumulativeFails > 0 {
			es.LastFailureReason = e.lastFailureReason.String()
		}
		s.Entries = append(s.Entries, es)
	}
	return s
}

// isUsableLocked reports whether e can be handed out by Acquire. Must be
// called with p.mu held.
func (p *Pool) isUsableLocked(e *entry, now time.Time) bool {
	switch e.state {
	case stateHealthy:
		return true
	case stateCooling:
		return !now.Before(e.nextOK)
	case stateQuarantined:
		return false
	default:
		return false
	}
}
