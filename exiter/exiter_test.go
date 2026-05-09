package exiter

import (
	"errors"
	"sync"
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

// TestRecordSeedOutcome_NilErrDoesNotMarkTerminallyFailed pins that a
// successful seed outcome does NOT contribute to the terminallyFailed
// counter — without this guard, a successful run with 0 places (legit slow
// renderer) would short-circuit through the fail-fast branch in isDone()
// and skip the 30s grace.
func TestRecordSeedOutcome_NilErrDoesNotMarkTerminallyFailed(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(2)
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 0})
	e.RecordSeedOutcome(SeedOutcome{Err: nil, RetriesLeft: 0, PlacesFound: 0})
	assert.Equal(t, 0, e.terminallyFailed,
		"successful seeds (Err=nil) must NOT increment terminallyFailed")
	e.startTime = time.Now()
	assert.False(t, e.isDone(),
		"all successful but empty: must grace, not fail-fast")
}

// TestRecordSeedOutcome_ErrWithPlacesIsNotTerminalFailure documents the
// nuanced edge case: a seed that errored out AFTER producing place links
// is not a terminal failure — those places should still be processed.
// Without this guard, a partial-extraction error would cause the run to
// fail-fast before its place-jobs ran.
func TestRecordSeedOutcome_ErrWithPlacesIsNotTerminalFailure(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	e.SetSeedCount(1)
	e.RecordSeedOutcome(SeedOutcome{
		Err:         errors.New("connection reset mid-extraction"),
		RetriesLeft: 0,
		PlacesFound: 7, // some places extracted before the error
	})
	assert.Equal(t, 0, e.terminallyFailed,
		"err with places>0 must NOT count as terminal failure (partial work salvaged)")
}

// TestRecordSeedOutcome_ConcurrentWritersAreSafe stresses the mutex under
// the race detector. In production, scrapemate runs N seed workers in
// parallel and each calls RecordSeedOutcome once — any data race here
// would corrupt seedCompleted / lastSeedError / terminallyFailed and
// silently break exit detection.
func TestRecordSeedOutcome_ConcurrentWritersAreSafe(t *testing.T) {
	t.Parallel()
	e := New().(*exiter)
	const workers = 50
	e.SetSeedCount(workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	failErr := errors.New("seed failure")
	for i := range workers {
		go func(idx int) {
			defer wg.Done()
			// Half succeed, half fail — exercises every mutated field.
			if idx%2 == 0 {
				e.RecordSeedOutcome(SeedOutcome{Err: nil, PlacesFound: idx})
			} else {
				e.RecordSeedOutcome(SeedOutcome{Err: failErr, PlacesFound: 0})
			}
		}(i)
	}
	wg.Wait()

	completed, total := e.GetSeedProgress()
	assert.Equal(t, workers, completed, "every concurrent RecordSeedOutcome must increment seedCompleted")
	assert.Equal(t, workers, total)
	assert.Equal(t, workers/2, e.terminallyFailed,
		"exactly half the writers recorded terminal failures — counter must match")
	assert.ErrorIs(t, e.LastSeedError(), failErr,
		"LastSeedError must reflect one of the failure errors (not nil, not garbage)")
}
