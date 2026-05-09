package gmaps

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/assert"
)

// fakeExiter is a minimal exiter.Exiter that records IncrSeedCompleted
// calls so tests can assert the fetch-error path correctly counts a failed
// seed as "done" (the bug at the heart of the half-hour-stuck-job incident).
type fakeExiter struct {
	seedCompleted   atomic.Int64
	mu              sync.Mutex
	lastSeedError   error
	lastSeedOutcome exiter.SeedOutcome // capture for assertions
}

func (f *fakeExiter) SetSeedCount(int)                 {}
func (f *fakeExiter) SetMaxResults(int)                {}
func (f *fakeExiter) GetMaxResults() int               { return 0 }
func (f *fakeExiter) GetSeedProgress() (int, int)      { return int(f.seedCompleted.Load()), 0 }
func (f *fakeExiter) GetResultsWritten() int           { return 0 }
func (f *fakeExiter) SetCancelFunc(context.CancelFunc) {}
func (f *fakeExiter) IncrSeedCompleted(n int)          { f.seedCompleted.Add(int64(n)) }
func (f *fakeExiter) IncrPlacesFound(int)              {}
func (f *fakeExiter) IncrPlacesCompleted(int)          {}
func (f *fakeExiter) IncrResultsWritten(int)           {}
func (f *fakeExiter) IsCancellationTriggered() bool    { return false }
func (f *fakeExiter) Run(context.Context)              {}
func (f *fakeExiter) RecordSeedError(err error) {
	if err == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSeedError = err
}
func (f *fakeExiter) LastSeedError() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastSeedError
}

func (f *fakeExiter) RecordSeedOutcome(o exiter.SeedOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSeedOutcome = o
	f.seedCompleted.Add(1)
	if o.Err != nil {
		f.lastSeedError = o.Err
	}
}

// TestGmapJob_ProcessOnFetchError_True locks in the contract that GmapJob
// opts in to receiving Process() calls even when the BrowserActions/fetch
// step failed. Without this, scrapemate discards the failed job before
// reaching Process() and the exit monitor's seedCompleted counter never
// ticks — the job then hangs until the webrunner's 61-minute backup timeout
// fires (see the May 2026 prod incident: ERR_PROXY_CONNECTION_FAILED →
// status stuck in "scraping" for half an hour).
func TestGmapJob_ProcessOnFetchError_True(t *testing.T) {
	t.Parallel()
	j := &GmapJob{}
	if !j.ProcessOnFetchError() {
		t.Errorf("ProcessOnFetchError: want true (so failed seeds reach Process and increment seedCompleted), got false")
	}
}

// TestGmapJob_Process_FetchError_IncrementsSeedCompleted reproduces the
// production bug: when BrowserActions fails (resp.Error != nil), Process
// MUST increment the exit monitor's seedCompleted counter and propagate
// the error back. Otherwise the exit monitor's isDone() check
// (seedCompleted < seedCount) returns false forever and mate.Start hangs
// until the 61-minute backup timeout.
func TestGmapJob_Process_FetchError_IncrementsSeedCompleted(t *testing.T) {
	t.Parallel()

	exiter := &fakeExiter{}
	j := &GmapJob{ExitMonitor: exiter}
	j.ID = "job_test_fetch_error"

	resp := &scrapemate.Response{
		URL:   "https://www.google.com/maps/search/Cafe+Mitte+Berlin?hl=en",
		Error: errors.New("playwright: net::ERR_PROXY_CONNECTION_FAILED"),
	}

	result, next, err := j.Process(context.Background(), resp)

	// Error must propagate so scrapemate logs the seed as failed.
	if err == nil {
		t.Errorf("err: want non-nil (the fetch error should propagate), got nil")
	}
	// No results, no follow-up jobs.
	if result != nil {
		t.Errorf("result: want nil on fetch error, got %v", result)
	}
	if next != nil {
		t.Errorf("next: want nil on fetch error, got %v", next)
	}
	// Critical assertion: seedCompleted must have ticked exactly once so
	// the exit monitor's seedCompleted >= seedCount check eventually trips.
	if got := exiter.seedCompleted.Load(); got != 1 {
		t.Errorf("seedCompleted: want 1 (failed seed counted as done), got %d", got)
	}
	// The raw fetch error must also be captured on the exit monitor so the
	// wrapping webrunner can sanitize and surface it as failure_reason
	// (otherwise the user sees the generic "context canceled" catch-all).
	if got := exiter.LastSeedError(); got == nil || got.Error() != resp.Error.Error() {
		t.Errorf("LastSeedError: want %v (the fetch error must be recorded), got %v", resp.Error, got)
	}

	// Verify the SeedOutcome is correctly populated via the new typed API.
	gotOutcome := exiter.lastSeedOutcome
	assert.Same(t, resp.Error, gotOutcome.Err, "RecordSeedOutcome must carry the raw fetch error")
	assert.True(t, gotOutcome.IsTerminalFailure(),
		"fetch error after retries with 0 places → terminal failure (fail-fast)")
}

// TestGmapJob_Process_FetchError_NilExitMonitor verifies the fetch-error
// path does not panic when ExitMonitor is unset (CLI/lambda mode).
func TestGmapJob_Process_FetchError_NilExitMonitor(t *testing.T) {
	t.Parallel()

	j := &GmapJob{} // no ExitMonitor
	j.ID = "job_test_no_exit_monitor"

	resp := &scrapemate.Response{
		URL:   "https://www.google.com/maps/search/x",
		Error: errors.New("network unreachable"),
	}

	result, next, err := j.Process(context.Background(), resp)

	if err == nil {
		t.Errorf("err: want non-nil, got nil")
	}
	if result != nil || next != nil {
		t.Errorf("result/next: want nil/nil, got %v/%v", result, next)
	}
}
