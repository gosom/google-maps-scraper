package webrunner

import (
	"os"
	"time"
)

// The leads scraper and AxioIntel's review collector share one machine. Since a customer's "Pull
// now" waits at most 15 minutes before AxioIntel falls back to Apify, lead jobs must get out of the
// way the moment the collector has a request outstanding, not just at CPU-share priority. The
// collector daemon (collect.sh) writes LEADS_YIELD_FILE the instant it sees a requested place and
// removes it once none is outstanding; a lead job never starts, and a running one is stopped and
// requeued, while the file is present and fresh (owner's decision, 27 Sep 2026).
//
// A file, not a socket or a shared database, because the daemon and this process are separate
// programs on separate schedules with no other channel between them, and a stale file is easy to
// reason about: staleAfter below treats an old one as gone rather than locking leads out forever
// if the daemon crashed while it was present.

// yieldGate reports whether AxioIntel collection is waiting on this machine right now.
type yieldGate struct {
	path       string
	staleAfter time.Duration
	stat       func(string) (os.FileInfo, error) // tests replace this
}

// newYieldGate returns nil for an empty path: no gate, never yielding.
func newYieldGate(path string) *yieldGate {
	if path == "" {
		return nil
	}

	return &yieldGate{path: path, staleAfter: 20 * time.Minute, stat: os.Stat}
}

// active is whether the gate is up at `now`. A missing file, or one the gate cannot stat, is not
// active; a file older than staleAfter is treated as forgotten, not honoured.
func (g *yieldGate) active(now time.Time) bool {
	if g == nil {
		return false
	}

	info, err := g.stat(g.path)
	if err != nil {
		return false
	}

	return now.Sub(info.ModTime()) < g.staleAfter
}

const yieldLabel = "Lead jobs yielding to AxioIntel — a customer's pull is waiting on this machine"
