//nolint:testpackage // tests the runner's unexported yield gate
package webrunner

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	assertNoLeadJobStarts(t, w, repo, "33333333-3333-3333-3333-333333333333", "medical spa")
}

func TestAJobRunningIsStoppedAndRequeuedWhenTheYieldFileAppears(t *testing.T) {
	w, repo, inside := yieldingRunner(t)

	assertRunningJobStopsAndRequeues(t, w, repo, inside, "44444444-4444-4444-4444-444444444444", "roofing contractor")
}
