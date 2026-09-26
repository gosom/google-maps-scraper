//nolint:testpackage // tests the runner's unexported yield gate
package webrunner

import (
	"context"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/web"
)

// stubFileInfo is the smallest os.FileInfo that gives yieldGate.active a ModTime to compare.
type stubFileInfo struct{ modTime time.Time }

func (s stubFileInfo) Name() string       { return "YIELD" }
func (s stubFileInfo) Size() int64        { return 0 }
func (s stubFileInfo) Mode() os.FileMode  { return 0 }
func (s stubFileInfo) ModTime() time.Time { return s.modTime }
func (s stubFileInfo) IsDir() bool        { return false }
func (s stubFileInfo) Sys() any           { return nil }

func TestYieldGateIsActiveOnlyWhileTheFileIsFreshAndPresent(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	g := &yieldGate{path: "YIELD", staleAfter: 20 * time.Minute, stat: func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}}
	assert.False(t, g.active(now), "no file: not yielding")

	g.stat = func(string) (os.FileInfo, error) { return stubFileInfo{modTime: now.Add(-5 * time.Minute)}, nil }
	assert.True(t, g.active(now), "written 5 minutes ago: still yielding")

	g.stat = func(string) (os.FileInfo, error) { return stubFileInfo{modTime: now.Add(-25 * time.Minute)}, nil }
	assert.False(t, g.active(now), "written 25 minutes ago: the daemon likely crashed; do not lock leads out forever")

	var nilGate *yieldGate

	assert.False(t, nilGate.active(now), "no gate configured: never yielding")
}

func TestNewYieldGateOfAnEmptyPathIsNilAndInert(t *testing.T) {
	assert.Nil(t, newYieldGate(""))
}

// yieldingRunner mirrors pausedRunner: `inside` switches the gate between absent and fresh, and
// w.now stays fixed just past the file's fake write time so freshness never depends on wall clock.
func yieldingRunner(t *testing.T) (*webrunner, *memoryJobRepo, *atomic.Bool) {
	t.Helper()

	w, repo, _ := newRecoveryRunner(t)

	inside := &atomic.Bool{}
	writtenAt := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	w.yield = &yieldGate{
		path:       "YIELD",
		staleAfter: 20 * time.Minute,
		stat: func(string) (os.FileInfo, error) {
			if !inside.Load() {
				return nil, os.ErrNotExist
			}

			return stubFileInfo{modTime: writtenAt}, nil
		},
	}
	w.now = func() time.Time { return writtenAt.Add(time.Minute) }
	w.watchEvery = 20 * time.Millisecond

	return w, repo, inside
}

func TestNoLeadJobStartsWhileTheYieldFileIsFresh(t *testing.T) {
	w, repo, inside := yieldingRunner(t)
	inside.Store(true)

	started := &atomic.Bool{}
	w.setupMate = func(context.Context, io.Writer, *web.Job) (mateRunner, error) {
		started.Store(true)

		return &hungMate{written: make(chan struct{})}, nil
	}

	job := &web.Job{ID: "33333333-3333-3333-3333-333333333333", Name: "Med Spas", Status: web.StatusPending,
		Date: time.Now().UTC(), Data: web.JobData{Keywords: []string{"medical spa"}, Lang: "en", Depth: 1, MaxTime: time.Hour}}
	require.NoError(t, repo.Create(t.Context(), job))

	ctx, cancel := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer cancel()

	require.NoError(t, w.work(ctx))

	got, err := repo.Get(t.Context(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, web.StatusPending, got.Status)
	assert.False(t, started.Load(), "AxioIntel is waiting; no lead job should have started")
}

func TestAJobRunningIsStoppedAndRequeuedWhenTheYieldFileAppears(t *testing.T) {
	w, repo, inside := yieldingRunner(t)

	mate := &untilCancelled{running: make(chan struct{})}
	w.setupMate = func(context.Context, io.Writer, *web.Job) (mateRunner, error) { return mate, nil }

	job := &web.Job{ID: "44444444-4444-4444-4444-444444444444", Name: "Roofers", Status: web.StatusPending,
		Date: time.Now().UTC(), Data: web.JobData{Keywords: []string{"roofing contractor"}, Lang: "en", Depth: 1, MaxTime: time.Hour}}
	require.NoError(t, repo.Create(t.Context(), job))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go w.pauseWatch(ctx)

	done := make(chan error, 1)
	go func() { done <- w.work(ctx) }()

	select {
	case <-mate.running:
	case <-time.After(5 * time.Second):
		t.Fatal("the job never started before AxioIntel asked for a place")
	}

	inside.Store(true) // the collector daemon just wrote LEADS_YIELD_FILE

	require.Eventually(t, func() bool {
		got, err := repo.Get(t.Context(), job.ID)

		return err == nil && got.Status == web.StatusPending
	}, 30*time.Second, 20*time.Millisecond) // generous: a loaded machine is slow to finish a job

	cancel()
	<-done

	w.mu.Lock()
	defer w.mu.Unlock()
	assert.Zero(t, w.attempts[job.ID], "yielding to AxioIntel is not a failed try")
}
