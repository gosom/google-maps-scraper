package gmaps

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExiter is a minimal exiter.Exiter that records the SeedOutcome events
// emitted by GmapJob.Process so tests can assert the fetch-error path
// correctly counts a failed seed (the half-hour-stuck-job incident) AND the
// success path emits a non-error outcome with PlacesFound. seedCompleted is
// kept atomic so concurrent goroutines (none in current tests, but cheap
// insurance) can read it without racing the captured outcome.
type fakeExiter struct {
	seedCompleted    atomic.Int64
	placesFoundCalls atomic.Int64
	placesFoundTotal atomic.Int64
	mu               sync.Mutex
	lastSeedError    error
	lastSeedOutcome  exiter.SeedOutcome // capture for assertions
}

func (f *fakeExiter) SetSeedCount(int)                 {}
func (f *fakeExiter) SetMaxResults(int)                {}
func (f *fakeExiter) GetMaxResults() int               { return 0 }
func (f *fakeExiter) GetSeedProgress() (int, int)      { return int(f.seedCompleted.Load()), 0 }
func (f *fakeExiter) GetResultsWritten() int           { return 0 }
func (f *fakeExiter) SetCancelFunc(context.CancelFunc) {}
func (f *fakeExiter) IncrSeedCompleted(n int)          { f.seedCompleted.Add(int64(n)) }
func (f *fakeExiter) IncrPlacesFound(n int) {
	f.placesFoundCalls.Add(1)
	f.placesFoundTotal.Add(int64(n))
}
func (f *fakeExiter) IncrPlacesCompleted(int)       {}
func (f *fakeExiter) IncrResultsWritten(int)        {}
func (f *fakeExiter) IsCancellationTriggered() bool { return false }
func (f *fakeExiter) Run(context.Context)           {}
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

	require.Error(t, err, "fetch error must propagate even without an ExitMonitor")
	assert.Nil(t, result, "no result on fetch error")
	assert.Nil(t, next, "no follow-up jobs on fetch error")
}

// TestGmapJob_Process_PlaceURL_SuccessBranch covers the happy path companion
// to the fetch-error tests above. When BrowserActions succeeds and the seed
// URL points at a /maps/place/ page, Process emits a PlaceJob and reports
// the seed as terminally successful (Err=nil, PlacesFound=1) so isDone()
// can later flush the run cleanly. Without coverage on this branch, a
// regression that swapped the success-side RecordSeedOutcome call would
// silently hang the exit monitor — symmetric to the original bug.
func TestGmapJob_Process_PlaceURL_SuccessBranch(t *testing.T) {
	t.Parallel()

	exit := &fakeExiter{}
	j := &GmapJob{ExitMonitor: exit}
	j.ID = "job_test_place_success"

	// /maps/place/ branch skips the goquery-feed selector entirely — a
	// minimal empty document is enough; the URL drives the dispatch.
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<html></html>"))
	require.NoError(t, err)
	resp := &scrapemate.Response{
		URL:      "https://www.google.com/maps/place/Cafe+Mitte/@52.5,13.4,17z",
		Document: doc,
	}

	result, next, err := j.Process(context.Background(), resp)

	require.NoError(t, err, "success branch must not return an error")
	assert.Nil(t, result, "GmapJob never returns a result row directly — only follow-up PlaceJobs")
	require.Len(t, next, 1, "place URL must spawn exactly one PlaceJob")

	assert.Equal(t, int64(1), exit.seedCompleted.Load(),
		"successful seed must tick seedCompleted exactly once")
	assert.Equal(t, int64(1), exit.placesFoundCalls.Load(),
		"IncrPlacesFound must be called exactly once on success")
	assert.Equal(t, int64(1), exit.placesFoundTotal.Load(),
		"IncrPlacesFound must report 1 place for a /maps/place/ seed")

	got := exit.lastSeedOutcome
	assert.NoError(t, got.Err, "success outcome must carry no error")
	assert.Equal(t, 1, got.PlacesFound, "success outcome must report 1 place")
	assert.True(t, got.IsTerminal(), "RetriesLeft=0 → terminal")
	assert.False(t, got.IsTerminalFailure(), "successful outcome is not a terminal failure")
}

// TestGmapJob_Process_CancelledBeforeParse pins that a context cancelled
// after a successful fetch but before parsing returns ctx.Err() with no
// outcome side-effects — important so a user-cancel mid-run does not
// double-count the seed (it would have already been counted once Process
// returns; the outer scrapemate worker handles the cancel path).
func TestGmapJob_Process_CancelledBeforeParse(t *testing.T) {
	t.Parallel()

	exit := &fakeExiter{}
	j := &GmapJob{ExitMonitor: exit}
	j.ID = "job_test_ctx_cancelled"

	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<html></html>"))
	require.NoError(t, err)
	resp := &scrapemate.Response{
		URL:      "https://www.google.com/maps/place/X",
		Document: doc,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	result, next, err := j.Process(ctx, resp)

	assert.ErrorIs(t, err, context.Canceled,
		"cancelled context must surface as ctx.Err() so the outer worker classifies it as user/system cancel")
	assert.Nil(t, result)
	assert.Nil(t, next)
	assert.Equal(t, int64(0), exit.seedCompleted.Load(),
		"on early cancel return, RecordSeedOutcome must NOT have been called yet "+
			"(prevents double-counting when the seed retries on a fresh ctx)")
}
