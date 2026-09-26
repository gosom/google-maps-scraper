package webrunner

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/AxioIntel/Axio-CRED/deduper"
	"github.com/AxioIntel/Axio-CRED/exiter"
	"github.com/AxioIntel/Axio-CRED/grid"
	"github.com/AxioIntel/Axio-CRED/runner"
	"github.com/AxioIntel/Axio-CRED/tlmt"
	"github.com/AxioIntel/Axio-CRED/web"
	"github.com/AxioIntel/Axio-CRED/web/leads"
	"github.com/AxioIntel/Axio-CRED/web/saleshandy"
	"github.com/AxioIntel/Axio-CRED/web/sqlite"
	"github.com/AxioIntel/Axio-CRED/web/whatsapp"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	"golang.org/x/sync/errgroup"
)

type webrunner struct {
	srv       *web.Server
	svc       *web.Service
	cfg       *runner.Config
	leads     *leads.Store
	setupMate func(context.Context, io.Writer, *web.Job) (mateRunner, error)

	// The running job, for the stall watchdog.
	mu            sync.Mutex
	currentID     string
	cancelCurrent context.CancelFunc
	stalled       map[string]bool
	attempts      map[string]int
	// paused are jobs stopped because the pause window opened; they go back in the queue as they
	// were, without counting as a failed try.
	paused map[string]bool

	// pause is LEADS_PAUSE_WINDOW (nil: never paused); now is the clock it is read against.
	pause *pauseWindow
	// yield is LEADS_YIELD_FILE (nil: never yielding); see yield.go.
	yield *yieldGate
	now   func() time.Time

	// watchEvery and stallAfter override the watchdog's timing (tests); zero is the default.
	watchEvery, stallAfter time.Duration

	// restartAfterJob is set when a finished job's browsers would not close; exit is how the
	// process then ends (os.Exit; tests replace it) so the container restarts clean.
	restartAfterJob bool
	closeGrace      time.Duration
	exit            func(int)
}

// A job whose run is stopped by the watchdog is queued again at most this many times.
const maxStallRetries = 2

// browserUA is what the dashboard's browser says it is: the Chromium the image actually runs
// (playwright chromium v1228 = Chrome 149, on Linux). Without it scrapemate presents a hard-coded
// Chrome 91 from 2021, a years-old browser on a current engine, which is its own reason for a site
// to distrust the visit. Update it with the image's Chromium.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

type mateRunner interface {
	Start(context.Context, ...scrapemate.IJob) error
	Close() error
}

func New(cfg *runner.Config) (runner.Runner, error) {
	if cfg.DataFolder == "" {
		return nil, fmt.Errorf("data folder is required")
	}

	if err := os.MkdirAll(cfg.DataFolder, os.ModePerm); err != nil {
		return nil, err
	}

	const dbfname = "jobs.db"

	dbpath := filepath.Join(cfg.DataFolder, dbfname)

	repo, err := sqlite.New(dbpath)
	if err != nil {
		return nil, err
	}

	svc := web.NewService(repo, cfg.DataFolder)

	// Every job's results also land in one deduplicated lead list, which outlives the jobs.
	leadStore, err := leads.Open(filepath.Join(cfg.DataFolder, "leads.db"))
	if err != nil {
		return nil, err
	}

	// Saleshandy: the API key comes from the environment (the machine's env file), and the
	// sequences leads may go into from saleshandy.json beside the jobs. Without a key the
	// dashboard simply has no Saleshandy button.
	shConfigPath := os.Getenv("SALESHANDY_CONFIG")
	if shConfigPath == "" {
		shConfigPath = filepath.Join(cfg.DataFolder, "saleshandy.json")
	}

	shConfig, err := saleshandy.LoadConfig(shConfigPath)
	if err != nil {
		return nil, fmt.Errorf("saleshandy config %s: %w", shConfigPath, err)
	}

	srv, err := web.New(svc, cfg.Addr, web.WithLeads(leadStore),
		web.WithSaleshandy(saleshandy.New(os.Getenv("SALESHANDY_API_KEY")), shConfig),
		web.WithWhatsApp(whatsapp.New(os.Getenv("WHATSAPP_TOKEN"), os.Getenv("WHATSAPP_PHONE_NUMBER_ID"), os.Getenv("WHATSAPP_BUSINESS_ACCOUNT_ID"))))
	if err != nil {
		return nil, err
	}

	pause, err := parsePauseWindow(os.Getenv("LEADS_PAUSE_WINDOW"))
	if err != nil {
		return nil, err
	}

	yieldPath := os.Getenv("LEADS_YIELD_FILE")
	if yieldPath == "" {
		yieldPath = filepath.Join(cfg.DataFolder, "YIELD")
	}

	ans := webrunner{
		srv:       srv,
		svc:       svc,
		cfg:       cfg,
		leads:     leadStore,
		setupMate: defaultSetupMate(cfg),
		pause:     pause,
		yield:     newYieldGate(yieldPath),
	}

	return &ans, nil
}

func (w *webrunner) Run(ctx context.Context) error {
	egroup, ctx := errgroup.WithContext(ctx)

	egroup.Go(func() error {
		return w.work(ctx)
	})

	egroup.Go(func() error {
		w.watchdog(ctx)

		return nil
	})

	egroup.Go(func() error {
		w.pauseWatch(ctx)

		return nil
	})

	egroup.Go(func() error {
		return w.srv.Start(ctx)
	})

	return egroup.Wait()
}

func (w *webrunner) Close(context.Context) error {
	if w.leads != nil {
		return w.leads.Close()
	}

	return nil
}

// ingest folds a finished job's CSV into the lead list. A job that failed part-way is ingested for
// what it found. Failures are logged, never fatal: the CSV stays and the next start retries it.
func (w *webrunner) ingest(ctx context.Context, job *web.Job) {
	if w.leads == nil {
		return
	}

	path := filepath.Join(w.cfg.DataFolder, job.ID+".csv")
	if _, err := os.Stat(path); err != nil {
		return
	}

	n, err := w.leads.IngestCSV(ctx, job.ID, path)
	if err != nil {
		log.Printf("job %s: adding its results to the lead list failed: %v", job.ID, err)

		return
	}

	log.Printf("job %s: %d result(s) added to the lead list", job.ID, n)
}

// requeueInterrupted puts back in the queue any job a restart cut off mid-run (left "working").
// What it had found so far is added to the lead list first, because the rerun starts its CSV
// afresh; the lead list merges the two runs' results. Without this, a job stopped by a restart
// stayed "working" forever and the rest of its searches never ran.
func (w *webrunner) requeueInterrupted(ctx context.Context) {
	jobs, err := w.svc.All(ctx)
	if err != nil {
		log.Printf("requeueing interrupted jobs: %v", err)

		return
	}

	for i := range jobs {
		if jobs[i].Status != web.StatusWorking {
			continue
		}

		w.ingest(ctx, &jobs[i])

		jobs[i].Status = web.StatusPending
		if err := w.svc.Update(ctx, &jobs[i]); err != nil {
			log.Printf("job %s: could not requeue after a restart: %v", jobs[i].ID, err)

			continue
		}

		log.Printf("job %s was cut off by a restart; its results so far are kept and it runs again", jobs[i].ID)
	}
}

// backfill adds every finished job not yet in the lead list: the jobs run before the lead list
// existed, and any whose ingest failed.
func (w *webrunner) backfill(ctx context.Context) {
	if w.leads == nil {
		return
	}

	jobs, err := w.svc.All(ctx)
	if err != nil {
		log.Printf("lead list backfill: %v", err)

		return
	}

	for i := range jobs {
		if jobs[i].Status != web.StatusOK && jobs[i].Status != web.StatusFailed {
			continue
		}

		if done, err := w.leads.Ingested(ctx, jobs[i].ID); err == nil && !done {
			w.ingest(ctx, &jobs[i])
		}
	}
}

func (w *webrunner) work(ctx context.Context) error {
	w.requeueInterrupted(ctx)
	w.backfill(ctx)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if w.pausedNow() {
				continue
			}

			jobs, err := w.svc.SelectPending(ctx)
			if err != nil {
				return err
			}

			for i := range jobs {
				if w.pausedNow() {
					break
				}

				select {
				case <-ctx.Done():
					return nil
				default:
					t0 := time.Now().UTC()
					if err := w.scrapeJob(ctx, &jobs[i]); err != nil {
						params := map[string]any{
							"job_count": len(jobs[i].Data.Keywords),
							"duration":  time.Now().UTC().Sub(t0).String(),
							"error":     err.Error(),
						}

						evt := tlmt.NewEvent("web_runner", params)

						_ = runner.Telemetry().Send(ctx, evt)

						log.Printf("error scraping job %s: %v", jobs[i].ID, err)
					} else {
						params := map[string]any{
							"job_count": len(jobs[i].Data.Keywords),
							"duration":  time.Now().UTC().Sub(t0).String(),
						}

						_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("web_runner", params))

						log.Printf("job %s scraped successfully", jobs[i].ID)
					}

					w.ingest(ctx, &jobs[i])
					w.afterJob(ctx, &jobs[i])

					if w.restartAfterJob {
						log.Printf("job %s: its browsers would not close; restarting the dashboard to clear them", jobs[i].ID)

						exit := w.exit
						if exit == nil {
							exit = os.Exit
						}

						exit(exitBrowsersHung)

						return nil
					}
				}
			}
		}
	}
}

func (w *webrunner) scrapeJob(ctx context.Context, job *web.Job) error {
	job.Status = web.StatusWorking

	err := w.svc.Update(ctx, job)
	if err != nil {
		return err
	}

	if len(job.Data.Keywords) == 0 {
		job.Status = web.StatusFailed

		return w.svc.Update(ctx, job)
	}

	outpath := filepath.Join(w.cfg.DataFolder, job.ID+".csv")

	outfile, err := os.Create(outpath)
	if err != nil {
		return err
	}

	defer func() {
		_ = outfile.Close()
	}()

	setupMate := w.setupMate
	if setupMate == nil {
		setupMate = defaultSetupMate(w.cfg)
	}

	// written is closed when every result has been written to the CSV (see signalWriter).
	written := make(chan struct{})

	mate, err := setupMate(context.WithValue(ctx, writtenKey{}, written), outfile, job)
	if err != nil {
		job.Status = web.StatusFailed

		err2 := w.svc.Update(ctx, job)
		if err2 != nil {
			log.Printf("failed to update job status: %v", err2)
		}

		return err
	}

	defer mate.Close()

	var coords string
	if job.Data.Lat != "" && job.Data.Lon != "" {
		coords = job.Data.Lat + "," + job.Data.Lon
	}

	dedup := deduper.New()
	exitMonitor := exiter.New()

	seedJobs, err := w.seedJobs(job, coords, dedup, exitMonitor)
	if err != nil {
		job.Status = web.StatusFailed

		err2 := w.svc.Update(ctx, job)
		if err2 != nil {
			log.Printf("failed to update job status: %v", err2)
		}

		return err
	}

	if len(seedJobs) > 0 {
		exitMonitor.SetSeedCount(len(seedJobs))

		allowedSeconds := max(60, len(seedJobs)*10*job.Data.Depth/50+120)

		if job.Data.MaxTime > 0 {
			if job.Data.MaxTime.Seconds() < 180 {
				allowedSeconds = 180
			} else {
				allowedSeconds = int(job.Data.MaxTime.Seconds())
			}
		}

		log.Printf("running job %s with %d seed jobs and %d allowed seconds", job.ID, len(seedJobs), allowedSeconds)

		mateCtx, cancel := context.WithTimeout(ctx, time.Duration(allowedSeconds)*time.Second)
		defer cancel()

		w.setCurrent(job.ID, cancel)
		defer w.setCurrent("", nil)

		exitMonitor.SetCancelFunc(cancel)

		go exitMonitor.Run(mateCtx)

		err = w.startMate(mateCtx, mate, seedJobs, written)
		if errors.Is(err, errCloseHung) {
			w.restartAfterJob = true
			err = nil
		}

		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			cancel()

			job.Status = web.StatusFailed

			err2 := w.svc.Update(ctx, job)
			if err2 != nil {
				log.Printf("failed to update job status: %v", err2)
			}

			return err
		}

		cancel()
	}

	job.Status = web.StatusOK

	return w.svc.Update(ctx, job)
}

// seedJobs are a job's first scrape jobs: one per search, or with a map grid one per search per
// square.
func (w *webrunner) seedJobs(job *web.Job, coords string, dedup deduper.Deduper, exitMonitor exiter.Exiter) ([]scrapemate.IJob, error) {
	extraReviews := w.cfg.ExtraReviews || job.Data.ExtraReviews

	if g := job.Data.Grid; g != nil {
		box, err := grid.ParseBoundingBox(g.BBox)
		if err != nil {
			return nil, err
		}

		return runner.CreateGridSeedJobs(
			job.Data.Lang,
			strings.NewReader(taggedSearches(job.Data.Keywords)),
			job.Data.Depth,
			job.Data.Email,
			box,
			g.CellKm,
			job.Data.Zoom,
			dedup,
			exitMonitor,
			extraReviews,
		)
	}

	radius := 10000.0 // 10 km
	if job.Data.Radius > 0 {
		radius = float64(job.Data.Radius)
	}

	return runner.CreateSeedJobs(
		job.Data.FastMode,
		job.Data.Lang,
		strings.NewReader(taggedSearches(job.Data.Keywords)),
		job.Data.Depth,
		job.Data.Email,
		coords,
		job.Data.Zoom,
		radius,
		dedup,
		exitMonitor,
		extraReviews,
	)
}

func defaultSetupMate(cfg *runner.Config) func(context.Context, io.Writer, *web.Job) (mateRunner, error) {
	return func(ctx context.Context, writer io.Writer, job *web.Job) (mateRunner, error) {
		opts := []func(*scrapemateapp.Config) error{
			scrapemateapp.WithConcurrency(cfg.Concurrency),
			scrapemateapp.WithExitOnInactivity(time.Minute * 3),
		}

		if !job.Data.FastMode {
			opts = append(opts,
				scrapemateapp.WithJS(scrapemateapp.DisableImages(), scrapemateapp.WithUA(browserUA)),
			)
		} else {
			opts = append(opts,
				scrapemateapp.WithStealth("firefox"),
			)
		}

		opts = runner.AppendBrowserCapacityOptions(opts, cfg)

		hasProxy := false

		if len(cfg.Proxies) > 0 {
			opts = append(opts, scrapemateapp.WithProxies(cfg.Proxies))
			hasProxy = true
		} else if len(job.Data.Proxies) > 0 {
			opts = append(opts,
				scrapemateapp.WithProxies(job.Data.Proxies),
			)
			hasProxy = true
		}

		if !cfg.DisablePageReuse {
			// Each browser keeps one proxy for its life. With proxies, a browser is replaced
			// every 25 pages so a proxy Google has started refusing is dropped quickly.
			browserReuse := 200
			if hasProxy {
				browserReuse = 25
			}

			opts = append(opts,
				scrapemateapp.WithPageReuseLimit(2),
				scrapemateapp.WithBrowserReuseLimit(browserReuse),
			)
		}

		log.Printf("job %s has proxy: %v", job.ID, hasProxy)

		csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(writer))

		writers := []scrapemate.ResultWriter{csvWriter}

		if done, ok := ctx.Value(writtenKey{}).(chan struct{}); ok {
			writers[0] = &signalWriter{ResultWriter: csvWriter, done: done}
		}

		matecfg, err := scrapemateapp.NewConfig(
			writers,
			opts...,
		)
		if err != nil {
			return nil, err
		}

		return scrapemateapp.NewScrapeMateApp(matecfg)
	}
}

// taggedSearches is the job's searches as seed lines tagged with their own words ("q#!#q"), so the
// scraper writes each row's search into its input_id: the lead list keeps it, and afterJob can
// tell which searches came back empty.
func taggedSearches(keywords []string) string {
	lines := make([]string, 0, len(keywords))

	for _, k := range keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}

		if strings.Contains(k, "#!#") {
			lines = append(lines, k)

			continue
		}

		lines = append(lines, k+"#!#"+k)
	}

	return strings.Join(lines, "\n")
}

func (w *webrunner) setCurrent(id string, cancel context.CancelFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.currentID, w.cancelCurrent = id, cancel
}

// watchdog stops a running job that has written no new listing for web.StallAfter: a browser
// that hung, or a Google page that never answered. The job's results so far are kept, and
// afterJob queues it again.
func (w *webrunner) watchdog(ctx context.Context) {
	every, stallAfter := 30*time.Second, web.StallAfter
	if w.watchEvery > 0 {
		every = w.watchEvery
	}

	if w.stallAfter > 0 {
		stallAfter = w.stallAfter
	}

	t := time.NewTicker(every)
	defer t.Stop()

	var (
		watching   string
		lastRows   int
		lastChange time.Time
	)

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			w.mu.Lock()
			id, cancel := w.currentID, w.cancelCurrent
			w.mu.Unlock()

			if id == "" || cancel == nil {
				watching = ""

				continue
			}

			rows := w.svc.CountRows(id)

			if id != watching || rows != lastRows {
				watching, lastRows, lastChange = id, rows, now

				continue
			}

			if now.Sub(lastChange) < stallAfter {
				continue
			}

			w.mu.Lock()
			if w.stalled == nil {
				w.stalled = map[string]bool{}
			}

			w.stalled[id] = true
			w.mu.Unlock()

			log.Printf("job %s: no new listing for %s; stopping it to start again", id, web.StallAfter)
			w.note(fmt.Sprintf("stalled %s with no new listing; restarting the job", web.StallAfter))
			cancel()

			watching = ""
		}
	}
}

// afterJob is what happens once a job's run ends: a run the watchdog stopped goes back in the
// queue (at most maxStallRetries times), and searches that returned nothing get one retry job.
func (w *webrunner) afterJob(ctx context.Context, job *web.Job) {
	w.mu.Lock()
	paused := w.paused[job.ID]
	delete(w.paused, job.ID)
	w.mu.Unlock()

	if paused {
		job.Status = web.StatusPending
		if err := w.svc.Update(ctx, job); err != nil {
			log.Printf("job %s: could not requeue after the pause window opened: %v", job.ID, err)
		}

		w.note(fmt.Sprintf("%q stopped for the pause window; it continues when lead jobs resume", job.Name))

		return
	}

	w.mu.Lock()
	stalled := w.stalled[job.ID]
	delete(w.stalled, job.ID)

	if stalled {
		if w.attempts == nil {
			w.attempts = map[string]int{}
		}

		w.attempts[job.ID]++
	}

	tries := w.attempts[job.ID]
	w.mu.Unlock()

	if stalled {
		if tries <= maxStallRetries {
			job.Status = web.StatusPending
			w.note(fmt.Sprintf("%q queued again after a stall (%d of %d)", job.Name, tries, maxStallRetries))
		} else {
			job.Status = web.StatusFailed
			w.note(fmt.Sprintf("%q failed: stalled %d times", job.Name, tries))
		}

		if err := w.svc.Update(ctx, job); err != nil {
			log.Printf("job %s: could not requeue after a stall: %v", job.ID, err)
		}

		return
	}

	if job.Status != web.StatusOK || strings.HasPrefix(job.Name, retryPrefix) || job.Data.Grid != nil {
		return
	}

	empty := emptySearches(filepath.Join(w.cfg.DataFolder, job.ID+".csv"), job.Data.Keywords)
	if len(empty) == 0 {
		return
	}

	retry := *job
	retry.ID = uuid.New().String()
	retry.Name = retryPrefix + job.Name
	retry.Date = time.Now().UTC()
	retry.Status = web.StatusPending
	retry.Data.Keywords = empty

	if err := w.svc.Create(ctx, &retry); err != nil {
		log.Printf("job %s: could not queue a retry of %d empty searches: %v", job.ID, len(empty), err)

		return
	}

	w.note(fmt.Sprintf("%d search(es) in %q came back empty; queued once more", len(empty), job.Name))
}

// retryPrefix names the one retry job of a job's empty searches; a retry is not retried again.
const retryPrefix = "Retry: "

// emptySearches are the searches with no row in a finished job's CSV (by the input_id every row
// carries). A CSV without input_id (a job from before tagging) reports none.
func emptySearches(csvPath string, keywords []string) []string {
	f, err := os.Open(csvPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil
	}

	col := -1

	for i, h := range header {
		if strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")) == "input_id" {
			col = i
		}
	}

	if col < 0 {
		return nil
	}

	found := map[string]bool{}

	for {
		rec, err := r.Read()
		if err != nil {
			break
		}

		if col < len(rec) {
			found[strings.TrimSpace(rec[col])] = true
		}
	}

	if len(found) == 0 {
		return nil // nothing at all came back: not a few empty searches, but a failed job
	}

	var out []string

	for _, k := range keywords {
		k = strings.TrimSpace(k)
		if k != "" && !found[k] {
			out = append(out, k)
		}
	}

	return out
}

// note shows a message in the dashboard's metrics strip (and nowhere, without a server).
func (w *webrunner) note(msg string) {
	if w.srv != nil {
		w.srv.Note(msg)
	}
}

// A finished job's browsers sometimes never close: playwright's BrowserContext.Close has no
// timeout, and scrapemate calls it after every result is already written. The job is done, so it
// is marked done and ingested, and the process exits for Docker to restart it with no browsers.
const (
	defaultCloseGrace = 90 * time.Second
	exitBrowsersHung  = 75
)

var errCloseHung = errors.New("browsers did not close after the job finished")

type writtenKey struct{}

// signalWriter closes done once its writer has written every result and returned.
type signalWriter struct {
	scrapemate.ResultWriter
	done chan struct{}
}

func (s *signalWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	defer close(s.done)

	return s.ResultWriter.Run(ctx, in)
}

// startMate runs the scrape, and gives up waiting for it closeGrace after its results were all
// written.
func (w *webrunner) startMate(ctx context.Context, mate mateRunner, seeds []scrapemate.IJob, written <-chan struct{}) error {
	done := make(chan error, 1)

	go func() { done <- mate.Start(ctx, seeds...) }()

	select {
	case err := <-done:
		return err
	case <-written:
	}

	grace := w.closeGrace
	if grace <= 0 {
		grace = defaultCloseGrace
	}

	select {
	case err := <-done:
		return err
	case <-time.After(grace):
		return errCloseHung
	}
}

func (w *webrunner) clock() time.Time {
	if w.now != nil {
		return w.now()
	}

	return time.Now()
}

// pausedNow is whether lead jobs are stopped now, by the pause window or the AxioIntel yield gate;
// it keeps the dashboard's banner in step. The pause window's own label wins when both are active,
// since it says when work resumes; the yield gate does not know that.
func (w *webrunner) pausedNow() bool {
	now := w.clock()
	onPause := w.pause.active(now)
	onYield := w.yield.active(now)

	if w.srv != nil {
		switch {
		case onPause:
			w.srv.SetPaused(w.pause.label(now))
		case onYield:
			w.srv.SetPaused(yieldLabel)
		default:
			w.srv.SetPaused("")
		}
	}

	return onPause || onYield
}

// pauseWatch stops the lead job running when the pause window opens or the yield gate goes up;
// afterJob queues it again.
func (w *webrunner) pauseWatch(ctx context.Context) {
	if w.pause == nil && w.yield == nil {
		return
	}

	every := 30 * time.Second
	if w.watchEvery > 0 {
		every = w.watchEvery
	}

	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !w.pausedNow() {
				continue
			}

			w.mu.Lock()
			id, cancel := w.currentID, w.cancelCurrent

			if id != "" && cancel != nil && !w.paused[id] {
				if w.paused == nil {
					w.paused = map[string]bool{}
				}

				w.paused[id] = true
			} else {
				cancel = nil
			}
			w.mu.Unlock()

			if cancel != nil {
				log.Printf("job %s: the pause window opened or AxioIntel is waiting; stopping it until lead jobs resume", id)
				cancel()
			}
		}
	}
}
