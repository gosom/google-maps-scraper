package exiter

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// RecordSeedOutcome is the typed-event API replacing the older
// IncrSeedCompleted + RecordSeedError split. The new isDone() reads
// the recorded outcomes to fail-fast when every seed terminally failed,
// eliminating the 30s grace period that wasted CPU on a doomed run.

func TestRecordSeedOutcome_AccumulatesCounter(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(3)
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 5})
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 0})
	completed, total := e.GetSeedProgress()
	assert.Equal(t, 2, completed)
	assert.Equal(t, 3, total)
}

func TestRecordSeedOutcome_StoresLastError(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	first := errors.New("first failure")
	last := errors.New("last failure")
	e.RecordSeedOutcome(SeedOutcome{Err: first})
	e.RecordSeedOutcome(SeedOutcome{Err: last})
	assert.ErrorIs(t, e.LastSeedError(), last,
		"LastSeedError must reflect the most recently recorded failure")
}

func TestIsDone_FailFastOnAllSeedsTerminallyFailed(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(2)
	// Both seeds finished, both with terminal errors, no places found.
	// Per the prior code this would wait 30s before isDone()→true. Now: fast.
	e.RecordSeedOutcome(SeedOutcome{Err: errors.New("proxy fail"), RetriesLeft: 0})
	e.RecordSeedOutcome(SeedOutcome{Err: errors.New("dns fail"), RetriesLeft: 0})
	// Force startTime to "just now" so the prior 30s grace WOULD have fired
	// if the new logic didn't short-circuit.
	e.startTime = time.Now()
	assert.True(t, e.isDone(),
		"every seed terminally failed with 0 places — must short-circuit, not wait 30s")
}

func TestIsDone_StillGracesEmptySuccess(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(1)
	// Seed succeeded but produced 0 places — could be a search page that
	// rendered slowly. The 30s grace is preserved for THIS case.
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 0})
	e.startTime = time.Now() // reset grace clock
	assert.False(t, e.isDone(),
		"seed succeeded-but-empty: keep the 30s grace for slow renderers")
}
