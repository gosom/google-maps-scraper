package gmaps

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/gosom/scrapemate"
)

// fakeExiter is a minimal exiter.Exiter that records IncrSeedCompleted
// calls so tests can assert the fetch-error path correctly counts a failed
// seed as "done" (the bug at the heart of the half-hour-stuck-job incident).
type fakeExiter struct {
	seedCompleted atomic.Int64
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
