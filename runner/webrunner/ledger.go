package webrunner

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/web"
	"github.com/AxioIntel/Axio-CRED/web/leads"
)

// The run ledger (web/leads/runs.go) as the runner keeps it: a job's run is started when it
// begins, finished once it is ingested and afterJob has decided what happens to it, and a smoke
// job is checked then. Every write is logged on failure and never fails the job.

// runStart is what the ledger needs from the start of the running job.
type runStart struct {
	jobID        string
	started      time.Time
	blocksBefore int
	bound        time.Duration
}

func (w *webrunner) startLedger(ctx context.Context, job *web.Job, searches int, bound time.Duration) {
	start := runStart{jobID: job.ID, started: time.Now().UTC(), blocksBefore: gmaps.Blocks.Stats().Total, bound: bound}

	w.mu.Lock()
	w.lastRun = start
	w.mu.Unlock()

	if w.leads == nil {
		return
	}

	if err := w.leads.StartJobRun(ctx, job.ID, job.Data.CollectorName(), searches, bound, start.started); err != nil {
		log.Printf("job %s: recording its start: %v", job.ID, err)
	}
}

// finishLedger records how the job's run ended, v1's per-search rows, and a smoke job's check.
func (w *webrunner) finishLedger(ctx context.Context, job *web.Job) {
	w.mu.Lock()
	start := w.lastRun
	w.mu.Unlock()

	if w.leads == nil || start.jobID != job.ID {
		return
	}

	finished := time.Now().UTC()
	run := leads.JobRun{
		JobID: job.ID, FinishedAt: finished, Status: job.Status,
		Blocks: gmaps.Blocks.Stats().Total - start.blocksBefore,
	}

	if w.srv != nil {
		run.CPUP50, run.CPUP95, run.MemP50, run.MemP95 = w.srv.TakeJobResources(job.ID)
	}

	csvPath := filepath.Join(w.cfg.DataFolder, job.ID+".csv")

	emails, err := leads.CountCSVEmails(csvPath)
	if err != nil {
		log.Printf("job %s: counting its emails: %v", job.ID, err)
	}

	run.UniqueEmails = emails

	if err := w.leads.FinishJobRun(ctx, &run); err != nil {
		log.Printf("job %s: recording its end: %v", job.ID, err)
	}

	if !job.Data.IsV2() {
		if err := w.leads.RecordRunsFromCSV(ctx, job.ID, csvPath, job.Data.Keywords, start.started, finished); err != nil {
			log.Printf("job %s: recording its searches: %v", job.ID, err)
		}
	}

	if job.Data.Tag == web.TagSmoke && web.Finished(job.Status) {
		w.checkSmoke(ctx, job, &start, finished, run.Blocks, csvPath)
	}
}

// checkSmoke runs a smoke job's checklist and records it.
func (w *webrunner) checkSmoke(ctx context.Context, job *web.Job, start *runStart, finished time.Time, blocks int,
	csvPath string) {
	searches := make([]string, 0, len(job.Data.Keywords))

	for _, k := range job.Data.Keywords {
		if k = strings.TrimSpace(k); k != "" {
			searches = append(searches, k)
		}
	}

	facts, err := leads.ReadCSVFacts(csvPath, searches)
	if err != nil {
		log.Printf("job %s: reading its CSV for the smoke check: %v", job.ID, err)
	}

	runs, err := w.leads.Runs(ctx, job.ID)
	if err != nil {
		log.Printf("job %s: reading its searches for the smoke check: %v", job.ID, err)
	}

	counts, _ := w.leads.JobLeadCounts(ctx)

	in := leads.SmokeInput{
		Collector: job.Data.CollectorName(), JobStatus: job.Status, Wall: finished.Sub(start.started),
		Bound: start.bound, Searches: searches, CSV: facts, LeadsLinked: counts[job.ID], Runs: runs, Blocks: blocks,
	}

	if job.Data.IsV2() {
		cfg := job.Data.SteadyConfig()
		in.QueryBudget, in.Delay = cfg.QueryBudget, cfg.Delay
	}

	checks, passed := leads.EvaluateSmoke(&in)

	err = w.leads.RecordSmokeCheck(ctx, &leads.SmokeCheck{
		Collector: in.Collector, JobID: job.ID, At: finished, Passed: passed, Checks: checks,
	})
	if err != nil {
		log.Printf("job %s: recording its smoke check: %v", job.ID, err)
	}

	verdict := "passed"
	if !passed {
		verdict = "FAILED"
	}

	w.note(in.Collector + " smoke check " + verdict)
}

// bumpLedger adds to a job run's restarts or stalls, or marks it discarded.
func (w *webrunner) bumpLedger(ctx context.Context, jobID, counter string) {
	if w.leads == nil {
		return
	}

	if err := w.leads.BumpJobRun(ctx, jobID, counter); err != nil {
		log.Printf("job %s: recording %s: %v", jobID, counter, err)
	}
}
