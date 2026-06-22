package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/scrapemateapp"
)

// ─── jobQueue ─────────────────────────────────────────────────────────────────
// Implements scrapemate.JobProvider via a buffered channel so the persistent
// engine can receive jobs submitted by HTTP handlers.

type jobQueue struct {
	ch    chan scrapemate.IJob
	errCh chan error
}

func newJobQueue(buf int) *jobQueue {
	return &jobQueue{
		ch:    make(chan scrapemate.IJob, buf),
		errCh: make(chan error, 1),
	}
}

func (q *jobQueue) Jobs(_ context.Context) (<-chan scrapemate.IJob, <-chan error) {
	return q.ch, q.errCh
}

// Push satisfies scrapemate.JobProvider — called by the framework for child jobs.
func (q *jobQueue) Push(ctx context.Context, job scrapemate.IJob) error {
	select {
	case q.ch <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *jobQueue) submit(ctx context.Context, job scrapemate.IJob) error {
	select {
	case q.ch <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ─── resultRouter ─────────────────────────────────────────────────────────────
// Fans results from the persistent engine out to waiting HTTP handlers.
// Each PlaceJob result is keyed by placeID (job.GetParentID()); multiple
// concurrent callers for the same key all receive the result.

type resultRouter struct {
	mu      sync.Mutex
	waiters map[string][]chan *googlePlace
}

func newResultRouter() *resultRouter {
	return &resultRouter{waiters: make(map[string][]chan *googlePlace)}
}

// register creates a buffered result channel for key and stores it.
// Caller must defer unregister(key, ch) to clean up.
func (r *resultRouter) register(key string) chan *googlePlace {
	ch := make(chan *googlePlace, 1)
	r.mu.Lock()
	r.waiters[key] = append(r.waiters[key], ch)
	r.mu.Unlock()
	return ch
}

func (r *resultRouter) unregister(key string, ch chan *googlePlace) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.waiters[key]
	for i, c := range list {
		if c == ch {
			r.waiters[key] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(r.waiters[key]) == 0 {
		delete(r.waiters, key)
	}
}

// Run satisfies scrapemate.ResultWriter.
func (r *resultRouter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		r.dispatch(result)
	}
	return nil
}

func (r *resultRouter) dispatch(result scrapemate.Result) {
	job, ok := result.Job.(scrapemate.IJob)
	if !ok {
		return
	}
	key := job.GetParentID()
	if key == "" {
		key = job.GetID()
	}

	entries, err := asEntries(result.Data)
	if err != nil || len(entries) == 0 {
		return
	}
	gp := convertEntry(entries[0], key)

	r.mu.Lock()
	chans := make([]chan *googlePlace, len(r.waiters[key]))
	copy(chans, r.waiters[key])
	r.mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- gp:
		default:
		}
	}
}

// ─── memWriter ────────────────────────────────────────────────────────────────
// One-shot ResultWriter used by scrapeFresh (temporary browser per retry).

type memWriter struct {
	ch chan *googlePlace
}

func (m *memWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		job, ok := result.Job.(scrapemate.IJob)
		if !ok {
			continue
		}
		pid := job.GetParentID()
		if pid == "" {
			pid = job.GetID()
		}
		entries, _ := asEntries(result.Data)
		for _, e := range entries {
			select {
			case m.ch <- convertEntry(e, pid):
			default:
			}
		}
	}
	return nil
}

// ─── searchWriter ─────────────────────────────────────────────────────────────
// Collects []*gmaps.Entry from SearchJob results for scrapeSearch.

type searchWriter struct {
	mu      sync.Mutex
	entries []*gmaps.Entry
}

func (sw *searchWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		entries, ok := result.Data.([]*gmaps.Entry)
		if !ok {
			if raw, ok2 := result.Data.([]any); ok2 {
				for _, v := range raw {
					if e, ok3 := v.(*gmaps.Entry); ok3 {
						entries = append(entries, e)
					}
				}
			}
		}
		if len(entries) > 0 {
			sw.mu.Lock()
			sw.entries = append(sw.entries, entries...)
			sw.mu.Unlock()
		}
	}
	return nil
}

// ─── httpEngine ───────────────────────────────────────────────────────────────
// Long-lived Playwright browser kept alive between requests. A crash-recovery
// loop restarts the cycle automatically; the jobQueue persists across restarts
// so in-flight jobs are not lost.

type httpEngine struct {
	concurrency int
	proxies     string
	inactivity  time.Duration
	queue       *jobQueue
	router      *resultRouter
	cancel      context.CancelFunc
	done        chan struct{}
}

func newHTTPEngine(ctx context.Context, concurrency int, proxies string, inactivity time.Duration) *httpEngine {
	engineCtx, cancel := context.WithCancel(ctx)
	e := &httpEngine{
		concurrency: concurrency,
		proxies:     proxies,
		inactivity:  inactivity,
		queue:       newJobQueue(64),
		router:      newResultRouter(),
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	go e.run(engineCtx)
	return e
}

func (e *httpEngine) run(ctx context.Context) {
	defer close(e.done)
	for {
		if ctx.Err() != nil {
			return
		}
		err := e.runCycle(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[engine] cycle error: %v — restarting in 2s", err)
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
		}
		// normal exit (inactivity timeout) — restart immediately
	}
}

func (e *httpEngine) runCycle(ctx context.Context) error {
	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(e.concurrency),
		// no WithExitOnInactivity — engine runs until context cancelled or crash
		scrapemateapp.WithJS(scrapemateapp.DisableImages(), scrapemateapp.WithUA(randomUA())),
		scrapemateapp.WithPageReuseLimit(20), // rotate contexts every 20 pages to avoid fingerprint buildup
		scrapemateapp.WithProvider(e.queue),
	}
	if e.proxies != "" {
		opts = append(opts, scrapemateapp.WithProxies(strings.Split(e.proxies, ",")))
	}

	matecfg, err := scrapemateapp.NewConfig(
		[]scrapemate.ResultWriter{e.router},
		opts...,
	)
	if err != nil {
		return err
	}
	app, err := scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		return err
	}
	defer app.Close()

	return app.Start(ctx)
}

func (e *httpEngine) close() {
	e.cancel()
	<-e.done
}

// ─── scrapePlace with retry ────────────────────────────────────────────────────

// scrapePlace attempts the persistent engine first; on timeout falls back to
// fresh browsers with randomised UAs (max 2 retries), respecting ctx deadline.
// Per-attempt budget is 50s; worst-case across 3 attempts plus delays is ~160s,
// well within the 180s NestJS deadline.
func (e *httpEngine) scrapePlace(ctx context.Context, placeID, langCode string, extractEmail, extraReviews bool) (*googlePlace, error) {
	gp, err := e.scrapeOnce(ctx, placeID, langCode, extractEmail, extraReviews)
	if err == nil {
		return gp, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	log.Printf("[%s] attempt 0 failed: %v — retrying with fresh browser", placeID, err)

	for attempt := 1; attempt <= 2; attempt++ {
		delay := time.Duration(2000+rand.Intn(3000)) * time.Millisecond
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		ua := randomUA()
		log.Printf("[%s] attempt %d ua=%.50s", placeID, attempt, ua)
		gp, err = e.scrapeFresh(ctx, placeID, langCode, extractEmail, extraReviews, ua)
		if err == nil {
			return gp, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Printf("[%s] attempt %d failed: %v", placeID, attempt, err)
	}
	return nil, fmt.Errorf("all 3 attempts failed for %s", placeID)
}

// scrapeOnce submits the job to the persistent engine and waits up to 50s.
func (e *httpEngine) scrapeOnce(ctx context.Context, placeID, langCode string, extractEmail, extraReviews bool) (*googlePlace, error) {
	ch := e.router.register(placeID)
	defer e.router.unregister(placeID, ch)

	attemptCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()

	u := buildPlaceURL(placeID)
	job := gmaps.NewPlaceJob(placeID, langCode, u, extractEmail, extraReviews)
	job.Job.Timeout = 45 * time.Second // raises Playwright page timeout from default 30s

	if err := e.queue.submit(attemptCtx, job); err != nil {
		return nil, err
	}

	select {
	case gp := <-ch:
		return gp, nil
	case <-attemptCtx.Done():
		return nil, attemptCtx.Err()
	}
}

// scrapeFresh boots a one-shot browser with the given user-agent for a retry.
func (e *httpEngine) scrapeFresh(ctx context.Context, placeID, langCode string, extractEmail, extraReviews bool, ua string) (*googlePlace, error) {
	mem := &memWriter{ch: make(chan *googlePlace, 1)}

	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(1),
		scrapemateapp.WithExitOnInactivity(2 * time.Second),
		scrapemateapp.WithJS(scrapemateapp.DisableImages(), scrapemateapp.WithUA(ua)),
		scrapemateapp.WithPageReuseLimit(1),
	}
	if e.proxies != "" {
		opts = append(opts, scrapemateapp.WithProxies(strings.Split(e.proxies, ",")))
	}

	matecfg, err := scrapemateapp.NewConfig([]scrapemate.ResultWriter{mem}, opts...)
	if err != nil {
		return nil, err
	}
	app, err := scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		return nil, err
	}
	defer app.Close()

	u := buildPlaceURL(placeID)
	job := gmaps.NewPlaceJob(placeID, langCode, u, extractEmail, extraReviews)
	job.Job.Timeout = 45 * time.Second // raises Playwright page timeout from default 30s

	attemptCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()

	if err := app.Start(attemptCtx, job); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return nil, err
	}

	select {
	case gp := <-mem.ch:
		return gp, nil
	default:
		return nil, fmt.Errorf("fresh browser returned no result for %s", placeID)
	}
}

// ─── scrapeSearch ─────────────────────────────────────────────────────────────

func (e *httpEngine) scrapeSearch(ctx context.Context, params *gmaps.MapSearchParams) ([]*gmaps.Entry, error) {
	sw := &searchWriter{}
	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(1),
		scrapemateapp.WithExitOnInactivity(30 * time.Second),
		// no WithJS — SearchJob uses plain HTTP fetcher, not Playwright
	}
	if e.proxies != "" {
		opts = append(opts, scrapemateapp.WithProxies(strings.Split(e.proxies, ",")))
	}

	matecfg, err := scrapemateapp.NewConfig([]scrapemate.ResultWriter{sw}, opts...)
	if err != nil {
		return nil, err
	}
	app, err := scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		return nil, err
	}
	defer app.Close()

	searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := app.Start(searchCtx, gmaps.NewSearchJob(params)); err != nil &&
		err != context.Canceled && err != context.DeadlineExceeded {
		return nil, err
	}
	return sw.entries, nil
}

// ─── UA pool ──────────────────────────────────────────────────────────────────

var userAgents = []string{
	// Chrome 136 — released April 2025, widely deployed through 2026
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
	// Chrome 135
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
	// Firefox 128 ESR
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:128.0) Gecko/20100101 Firefox/128.0",
	// Safari 18 on macOS 15
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_2) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15",
}

func randomUA() string {
	return userAgents[rand.Intn(len(userAgents))]
}
