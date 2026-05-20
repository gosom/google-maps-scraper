package proxypool

import "time"

// Option configures a Pool. See WithThresholds, WithClock.
type Option func(*Pool)

// WithThresholds overrides the failure thresholds and cooling timings. Use
// only in tests or for tuning; the defaults are appropriate for production.
//
//	coolingFails:    consecutive failures before transition to cooling
//	quarantineFails: cumulative failures before permanent quarantine
//	baseCool:        starting cool duration (doubles per consecutive cooling cycle)
//	maxCool:         upper bound on cool duration
func WithThresholds(coolingFails, quarantineFails int, baseCool, maxCool time.Duration) Option {
	return func(p *Pool) {
		p.coolingFailThreshold = coolingFails
		p.quarantineFailThreshold = quarantineFails
		p.baseCoolDuration = baseCool
		p.maxCoolDuration = maxCool
	}
}

// WithClock injects a clock for tests. The default is realClock (time.Now()).
func WithClock(c clock) Option {
	return func(p *Pool) {
		p.clock = c
	}
}

// clock abstracts time.Now() so cooling timer transitions can be tested
// without time.Sleep. Production uses realClock; tests use a fakeClock.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
