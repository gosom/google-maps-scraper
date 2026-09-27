package webrunner

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"log"
	"strings"
	"time"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/web"
)

// steadyRunner is collector v2 as the job runner drives it (gmaps.SteadyCollector; tests fake it).
type steadyRunner interface {
	Run(ctx context.Context, queries []gmaps.SteadyQuery, emit func(gmaps.SteadyQuery, []*gmaps.Entry) error,
		observe func(gmaps.SearchRun)) error
	Close()
}

func defaultNewSteady(cfg gmaps.SteadyConfig, proxies []string) (steadyRunner, error) {
	return gmaps.NewSteadyCollector(cfg, proxies)
}

// steadyQueries are a job's searches for v2, each tagged with its own words as v1's are, so its
// rows' input_id is the search.
func steadyQueries(keywords []string) []gmaps.SteadyQuery {
	out := make([]gmaps.SteadyQuery, 0, len(keywords))

	for _, line := range strings.Split(taggedSearches(keywords), "\n") {
		text, id, ok := strings.Cut(line, "#!#")
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}

		out = append(out, gmaps.SteadyQuery{ID: id, Text: text})
	}

	return out
}

// v2Bound is the longest a v2 job may run: every search's budget and gap, or the job's own
// "Stop after" when that is shorter.
func v2Bound(job *web.Job, searches int) time.Duration {
	bound := job.Data.SteadyConfig().JobBound(searches)
	if job.Data.MaxTime > 0 && job.Data.MaxTime < bound {
		bound = job.Data.MaxTime
	}

	return bound
}

// scrapeV2 runs a job on collector v2: one search at a time, each search's rows written as soon as
// it ends, inside the job's bound. Running out of the bound is "partial"; what was written is kept.
func (w *webrunner) scrapeV2(ctx context.Context, job *web.Job, out io.Writer) error {
	queries := steadyQueries(job.Data.Keywords)
	cfg := job.Data.SteadyConfig()
	bound := v2Bound(job, len(queries))

	proxies := w.cfg.Proxies
	if len(proxies) == 0 {
		proxies = job.Data.Proxies
	}

	newSteady := w.newSteady
	if newSteady == nil {
		newSteady = defaultNewSteady
	}

	collector, err := newSteady(cfg, proxies)
	if err != nil {
		job.Status = web.StatusFailed

		return errors.Join(err, w.svc.Update(ctx, job))
	}
	defer collector.Close()

	w.startLedger(ctx, job, len(queries), bound)

	log.Printf("running job %s on v2: %d searches, at most %s (%d page(s) a search, %s apart, %s a search)",
		job.ID, len(queries), bound, cfg.Pages, cfg.Delay, cfg.QueryBudget)

	runCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	w.setCurrent(job.ID, cancel)
	w.setBounded(true)

	defer func() {
		w.setBounded(false)
		w.setCurrent("", nil)
	}()

	cw := csv.NewWriter(out)
	if err := cw.Write((&gmaps.Entry{}).CsvHeaders()); err != nil {
		return err
	}

	cw.Flush()

	emit := func(_ gmaps.SteadyQuery, entries []*gmaps.Entry) error {
		for _, e := range entries {
			if err := cw.Write(e.CsvRow()); err != nil {
				return err
			}
		}

		cw.Flush()

		return cw.Error()
	}

	observe := func(r gmaps.SearchRun) {
		if w.leads == nil {
			return
		}

		// The job's own context: a search that ended because the job did is still recorded.
		if err := w.leads.RecordSearchRun(context.WithoutCancel(ctx), job.ID, &r); err != nil {
			log.Printf("job %s: recording search %q: %v", job.ID, r.Search, err)
		}
	}

	err = collector.Run(runCtx, queries, emit, observe)

	switch {
	case err == nil, errors.Is(err, context.Canceled), ctx.Err() != nil:
		job.Status = web.StatusOK // a stop by the pause window, yield or shutdown is requeued by afterJob
	case errors.Is(err, context.DeadlineExceeded):
		job.Status = web.StatusPartial
	default:
		job.Status = web.StatusFailed

		return errors.Join(err, w.svc.Update(ctx, job))
	}

	return w.svc.Update(ctx, job)
}

// setBounded marks the running job as one that bounds its own time (v2): the stall watchdog
// leaves it alone, since a run of empty searches writes no rows for a while by design.
func (w *webrunner) setBounded(b bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.currentBounded = b
}
