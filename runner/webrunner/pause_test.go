//nolint:testpackage // tests the runner's unexported pause window
package webrunner

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/web"
)

func TestPauseWindowFollowsNewYorkTimeThroughDaylightSaving(t *testing.T) {
	p, err := parsePauseWindow("08:00-20:00 America/New_York")
	require.NoError(t, err)

	// September: EDT, UTC-4. 12:00 UTC is 08:00 in New York.
	assert.False(t, p.active(time.Date(2026, 9, 26, 11, 59, 0, 0, time.UTC)))
	assert.True(t, p.active(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)))
	assert.True(t, p.active(time.Date(2026, 9, 26, 23, 59, 0, 0, time.UTC)))
	assert.False(t, p.active(time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)))

	// December: EST, UTC-5. 12:30 UTC is 07:30 in New York; 13:00 UTC is 08:00.
	assert.False(t, p.active(time.Date(2026, 12, 1, 12, 30, 0, 0, time.UTC)))
	assert.True(t, p.active(time.Date(2026, 12, 1, 13, 0, 0, 0, time.UTC)))

	end := p.until(time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC))
	assert.Equal(t, time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC), end.UTC())
	assert.Contains(t, p.label(time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)), "until 20:00 EDT (00:00 UTC)")
}

func TestAPauseWindowCanRunPastMidnight(t *testing.T) {
	p, err := parsePauseWindow("22:00-06:00 UTC")
	require.NoError(t, err)

	assert.True(t, p.active(time.Date(2026, 9, 26, 23, 0, 0, 0, time.UTC)))
	assert.True(t, p.active(time.Date(2026, 9, 26, 5, 59, 0, 0, time.UTC)))
	assert.False(t, p.active(time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC)))
	assert.False(t, p.active(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)))
}

func TestPauseWindowParsing(t *testing.T) {
	p, err := parsePauseWindow("")
	require.NoError(t, err)
	assert.Nil(t, p)
	assert.False(t, p.active(time.Now()), "no window: never paused")

	for _, bad := range []string{"08:00-20:00", "8-20 America/New_York", "08:00-20:00 Mars/Olympus", "09:00-09:00 UTC"} {
		_, err := parsePauseWindow(bad)
		require.Error(t, err, bad)
	}
}

func pausedRunner(t *testing.T) (*webrunner, *memoryJobRepo, *atomic.Bool) {
	t.Helper()

	w, repo, _ := newRecoveryRunner(t)

	p, err := parsePauseWindow("08:00-20:00 America/New_York")
	require.NoError(t, err)

	inside := &atomic.Bool{}
	w.pause = p
	w.now = func() time.Time {
		if inside.Load() {
			return time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC) // noon in New York
		}

		return time.Date(2026, 9, 26, 3, 0, 0, 0, time.UTC) // 23:00 the night before
	}
	w.watchEvery = 20 * time.Millisecond

	return w, repo, inside
}

func TestNoLeadJobStartsInsideThePauseWindow(t *testing.T) {
	w, repo, inside := pausedRunner(t)
	inside.Store(true)

	assertNoLeadJobStarts(t, w, repo, "11111111-1111-1111-1111-111111111111", "dentist")
}

// assertNoLeadJobStarts queues one job on a runner that is already paused or yielding, runs the
// work loop for a while, and checks the job never started and is still pending.
func assertNoLeadJobStarts(t *testing.T, w *webrunner, repo *memoryJobRepo, id, keyword string) {
	t.Helper()

	started := &atomic.Bool{}
	w.setupMate = func(context.Context, io.Writer, *web.Job) (mateRunner, error) {
		started.Store(true)

		return &hungMate{written: make(chan struct{})}, nil
	}

	job := &web.Job{ID: id, Name: keyword, Status: web.StatusPending,
		Date: time.Now().UTC(), Data: web.JobData{Keywords: []string{keyword}, Lang: "en", Depth: 1, MaxTime: time.Hour}}
	require.NoError(t, repo.Create(t.Context(), job))

	ctx, cancel := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer cancel()

	require.NoError(t, w.work(ctx))

	got, err := repo.Get(t.Context(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, web.StatusPending, got.Status)
	assert.False(t, started.Load(), "no lead job may start")
}

// untilCancelled writes nothing and returns when its job is stopped.
type untilCancelled struct{ running chan struct{} }

func (m *untilCancelled) Start(ctx context.Context, _ ...scrapemate.IJob) error {
	close(m.running)
	<-ctx.Done()

	return ctx.Err()
}

func (m *untilCancelled) Close() error { return nil }

func TestAJobRunningWhenThePauseOpensIsStoppedAndQueuedAgainAsItWas(t *testing.T) {
	w, repo, inside := pausedRunner(t)

	assertRunningJobStopsAndRequeues(t, w, repo, inside, "22222222-2222-2222-2222-222222222222", "locksmith")
}

// assertRunningJobStopsAndRequeues starts one job, flips `inside` (the pause window opening, or
// the yield file appearing) while it runs, and checks it goes back to pending without counting as a
// failed try.
func assertRunningJobStopsAndRequeues(t *testing.T, w *webrunner, repo *memoryJobRepo, inside *atomic.Bool, id, keyword string) {
	t.Helper()

	mate := &untilCancelled{running: make(chan struct{})}
	w.setupMate = func(context.Context, io.Writer, *web.Job) (mateRunner, error) { return mate, nil }

	job := &web.Job{ID: id, Name: keyword, Status: web.StatusPending,
		Date: time.Now().UTC(), Data: web.JobData{Keywords: []string{keyword}, Lang: "en", Depth: 1, MaxTime: time.Hour}}
	require.NoError(t, repo.Create(t.Context(), job))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go w.pauseWatch(ctx)

	done := make(chan error, 1)
	go func() { done <- w.work(ctx) }()

	select {
	case <-mate.running:
	case <-time.After(5 * time.Second):
		t.Fatal("the job never started")
	}

	inside.Store(true)

	require.Eventually(t, func() bool {
		got, err := repo.Get(t.Context(), job.ID)

		return err == nil && got.Status == web.StatusPending
	}, 30*time.Second, 20*time.Millisecond) // generous: a loaded machine is slow to finish a job

	cancel()
	<-done

	w.mu.Lock()
	defer w.mu.Unlock()
	assert.Zero(t, w.attempts[job.ID], "stopping for a pause or a yield is not a failed try")
}
