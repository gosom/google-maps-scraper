package scrapemate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/gosom/kit/logging"
)

// New creates a new scrapemate
func New(options ...func(*ScrapeMate) error) (*ScrapeMate, error) {
	s := &ScrapeMate{}

	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	if s.jobProvider == nil {
		return nil, ErrorNoJobProvider
	}

	if s.httpFetcher == nil {
		return nil, ErrorNoHTMLFetcher
	}
	// here we can set default options
	s.results = make(chan Result)

	if s.ctx == nil {
		s.ctx, s.cancelFn = context.WithCancelCause(context.Background())
	}

	if s.cancelFn == nil {
		s.ctx, s.cancelFn = context.WithCancelCause(s.ctx)
	}

	if s.log == nil {
		s.log = logging.Get().With("component", "scrapemate")
		s.log.Debug("using default logger")
	}

	if s.concurrency == 0 {
		s.concurrency = 1
	}

	return s, nil
}

func WithExitBecauseOfInactivity(duration time.Duration) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		s.exitOnInactivity = duration > 0
		s.exitOnInactivityDuration = duration

		return nil
	}
}

// WithFailed sets the failed jobs channel for the scrapemate
func WithFailed() func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		s.failedJobs = make(chan IJob)
		return nil
	}
}

// WithContext sets the context for the scrapemate
func WithContext(ctx context.Context, cancelFn context.CancelCauseFunc) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		if ctx == nil {
			return ErrorNoContext
		}

		s.ctx = ctx
		s.cancelFn = cancelFn

		return nil
	}
}

// WithLogger sets the logger for the scrapemate
func WithLogger(log logging.Logger) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		if log == nil {
			return ErrorNoLogger
		}

		s.log = log

		return nil
	}
}

// WithJobProvider sets the job provider for the scrapemate
func WithJobProvider(provider JobProvider) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		if provider == nil {
			return errors.New("job provider is nil")
		}

		s.jobProvider = provider

		return nil
	}
}

// WithConcurrency sets the concurrency for the scrapemate
func WithConcurrency(concurrency int) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		if concurrency < 1 {
			return ErrorConcurrency
		}

		s.concurrency = concurrency

		return nil
	}
}

// WithHTTPFetcher sets the http fetcher for the scrapemate
func WithHTTPFetcher(client HTTPFetcher) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		if client == nil {
			return ErrorNoHTMLFetcher
		}

		s.httpFetcher = client

		return nil
	}
}

// WithHTMLParser sets the html parser for the scrapemate
func WithHTMLParser(parser HTMLParser) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		if parser == nil {
			return ErrorNoHTMLParser
		}

		s.htmlParser = parser

		return nil
	}
}

// WithCache sets the cache for the scrapemate
func WithCache(cache Cacher) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		if cache == nil {
			return ErrorNoCacher
		}

		s.cache = cache

		return nil
	}
}

// WithInitJob sets the first job to be processed
// It will be processed before the jobs from the job provider
// It is useful if you want to start the scraper with a specific job
// instead of the first one from the job provider
// A real use case is when you want to obtain some cookies before starting
// the scraping process (e.g. login)
// Important: The results from these job will be discarded !
func WithInitJob(job IJob) func(*ScrapeMate) error {
	return func(s *ScrapeMate) error {
		s.initJob = job

		return nil
	}
}

// Scrapemate contains unexporter fields
type ScrapeMate struct {
	log         logging.Logger
	ctx         context.Context
	cancelFn    context.CancelCauseFunc
	jobProvider JobProvider
	concurrency int
	httpFetcher HTTPFetcher
	htmlParser  HTMLParser
	cache       Cacher
	results     chan Result
	failedJobs  chan IJob
	initJob     IJob

	stats                    stats
	exitOnInactivity         bool
	exitOnInactivityDuration time.Duration
}

// Start starts the scraper
func (s *ScrapeMate) Start() error {
	s.log.Info("starting scrapemate")

	defer func() {
		close(s.results)

		if s.failedJobs != nil {
			close(s.failedJobs)
		}

		s.log.Info("scrapemate exited")
	}()

	exitChan := make(chan os.Signal, 1)

	signal.Notify(exitChan, os.Interrupt, syscall.SIGTERM)
	s.waitForSignal(exitChan)

	if err := s.processInitJob(s.ctx); err != nil {
		return err
	}

	wg := sync.WaitGroup{}
	wg.Add(s.concurrency)

	for i := 0; i < s.concurrency; i++ {
		go func() {
			defer wg.Done()

			s.startWorker(s.ctx)
		}()
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

		startTime := time.Now().UTC()
		tickerDur := time.Minute

		const (
			divider          = 2
			secondsPerMinute = 60
		)

		if s.exitOnInactivity && s.exitOnInactivityDuration < tickerDur {
			tickerDur = s.exitOnInactivityDuration / divider
		}

		ticker := time.NewTicker(tickerDur)
		defer ticker.Stop()

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				numOfJobsCompleted, numOfJobsFailed, lastActivityAt := s.stats.getStats()
				perMinute := float64(numOfJobsCompleted) / time.Now().UTC().Sub(startTime).Seconds() * secondsPerMinute

				s.log.Info("scrapemate stats",
					"numOfJobsCompleted", numOfJobsCompleted,
					"numOfJobsFailed", numOfJobsFailed,
					"lastActivityAt", lastActivityAt,
					"speed", fmt.Sprintf("%.2f jobs/min", perMinute),
				)

				if s.exitOnInactivity && time.Now().UTC().Sub(lastActivityAt) > s.exitOnInactivityDuration {
					err := fmt.Errorf("%w: %s", ErrInactivityTimeout, lastActivityAt.Format(time.RFC3339))

					s.log.Info("exiting because of inactivity", "error", err)
					s.cancelFn(err)

					return
				}
			}
		}
	}()

	wg.Wait()

	<-s.Done()

	return s.Err()
}

func (s *ScrapeMate) Close() error {
	_ = s.httpFetcher.Close()

	return nil
}

// Concurrency returns how many workers are running in parallel
func (s *ScrapeMate) Concurrency() int {
	return s.concurrency
}

// Results returns a channel containing the results
func (s *ScrapeMate) Results() <-chan Result {
	return s.results
}

// Failed returns the chanell that contains the jobs that failed. It's nil if
// you don't use the WithFailed option
func (s *ScrapeMate) Failed() <-chan IJob {
	return s.failedJobs
}

// DoJob scrapes a job and returns it's result
func (s *ScrapeMate) DoJob(ctx context.Context, job IJob) (result any, next []IJob, err error) {
	ctx = ContextWithLogger(ctx, s.log.With("jobid", job.GetID()))
	startTime := time.Now().UTC()

	s.log.Debug("starting job", "job", job)

	var resp Response

	defer func() {
		args := []any{
			"job", job,
		}

		if r := recover(); r != nil {
			args = append(args, "error", r, "status", "failed")
			stack := string(debug.Stack())
			err = fmt.Errorf("panic while executing job: %s", stack)
			args = append(args, "error", err)
			s.log.Error("job finished", args...)

			return
		}

		if resp.Error != nil {
			args = append(args, "error", resp.Error, "status", "failed")
		} else {
			args = append(args, "status", "success")
		}

		args = append(args, "duration", time.Now().UTC().Sub(startTime))

		s.log.Info("job finished", args...)
	}()

	var cached bool

	cacheKey := job.GetCacheKey()

	if s.cache != nil {
		var errCache error

		resp, errCache = s.cache.Get(ctx, cacheKey)
		if errCache == nil {
			cached = true
		}
	}

	switch {
	case cached:
		s.log.Debug("using cached response", "job", job)
	default:
		resp = s.doFetch(ctx, job)
		if !job.ProcessOnFetchError() && resp.Error != nil {
			err = resp.Error

			return nil, nil, err
		}

		// check if resp.Error is valid because we may ProcessOnFetchError
		if resp.Error == nil && s.cache != nil {
			if errCache := s.cache.Set(ctx, cacheKey, &resp); errCache != nil {
				s.log.Error("error while caching response", "error", errCache, "job", job)
			}
		}
	}

	// process the response if we have a html parser and the resp has no error
	if resp.Error == nil && s.htmlParser != nil {
		resp.Document, err = s.htmlParser.Parse(ctx, resp.Body)
		if err != nil {
			s.log.Error("error while setting document", "error", err)

			return nil, nil, err
		}
	}

	result, next, err = job.Process(ctx, &resp)
	if err != nil {
		// TODO shall I retry?
		s.log.Error("error while processing job", "error", err)

		return nil, nil, err
	}

	return result, next, nil
}

func (s *ScrapeMate) doFetch(ctx context.Context, job IJob) (ans Response) {
	var ok bool
	defer func() {
		if !ok && ans.Error == nil {
			ans.Error = fmt.Errorf("status code %d", ans.StatusCode)
		}
	}()

	maxRetries := s.getMaxRetries(job)

	const defaultMilliseconds = 100

	delay := time.Millisecond * defaultMilliseconds
	retryPolicy := job.GetRetryPolicy()
	retry := 0

	for {
		ans = s.httpFetcher.Fetch(ctx, job)
		ok = job.DoCheckResponse(&ans)

		if ok {
			return ans
		}

		if retryPolicy == DiscardJob {
			s.log.Warn("discarding job because of policy")

			return ans
		}

		if retryPolicy == StopScraping {
			s.log.Warn("stopping scraping because of policy")
			s.cancelFn(errors.New("stopping scraping because of policy"))

			return ans
		}

		if retry >= maxRetries {
			return ans
		}

		retry++

		switch retryPolicy {
		case RetryJob:
			time.Sleep(delay)

			if delay > job.GetMaxRetryDelay() {
				delay = job.GetMaxRetryDelay()
			} else {
				delay *= 2
			}
		case RefreshIP: // TODO Implement
		}
	}
}

func (s *ScrapeMate) getMaxRetries(job IJob) int {
	const maxRetriesDefault = 5

	maxRetries := job.GetMaxRetries()
	if maxRetries > maxRetriesDefault {
		maxRetries = maxRetriesDefault
	}

	return maxRetries
}

// Done returns a channel  that's closed when the work is done
func (s *ScrapeMate) Done() <-chan struct{} {
	return s.ctx.Done()
}

// Err returns the error that caused scrapemate's context cancellation
func (s *ScrapeMate) Err() error {
	err := context.Cause(s.ctx)
	if errors.Is(err, ErrInactivityTimeout) {
		return nil
	}

	return err
}

func (s *ScrapeMate) waitForSignal(sigChan <-chan os.Signal) {
	go func() {
		<-sigChan
		s.log.Info("received signal, shutting down")
		s.cancelFn(ErrorExitSignal)
	}()
}

func (s *ScrapeMate) processInitJob(ctx context.Context) error {
	if s.initJob == nil {
		return nil
	}

	s.log.Info("processing init", "job", s.initJob)
	defer s.log.Info("init job finished", "job", s.initJob)

	var stack []IJob

	if s.initJob != nil {
		stack = append(stack, s.initJob)
	}

	var job IJob

	for len(stack) > 0 {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		job, stack = stack[0], stack[1:]

		_, next, err := s.DoJob(ctx, job)
		if err != nil {
			return err
		}

		stack = append(stack, next...)
	}

	return nil
}

func (s *ScrapeMate) startWorker(ctx context.Context) {
	jobc, errc := s.jobProvider.Jobs(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errc:
			if ctx.Err() == context.Canceled {
				return
			}

			s.log.Error("error while getting jobs...going to wait a bit", "error", err)

			time.Sleep(1 * time.Second)

			jobc, errc = s.jobProvider.Jobs(ctx)

			s.log.Info("restarted job provider")
		case job := <-jobc:
			ans, next, err := s.DoJob(ctx, job)
			if err != nil {
				s.log.Error("error while processing job", "error", err)

				s.pushToFailedJobs(job)
			} else {
				if err := s.finishJob(ctx, job, ans, next); err != nil {
					s.log.Error("error while finishing job", "error", err)

					s.pushToFailedJobs(job)
				}
			}
		}
	}
}

func (s *ScrapeMate) pushToFailedJobs(job IJob) {
	s.stats.incJobsFailed()

	if s.failedJobs != nil {
		const defaultTimeout = 5 * time.Second

		pushCtx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()

		select {
		case <-pushCtx.Done():
			return
		case s.failedJobs <- job:
		}
	}
}

func (s *ScrapeMate) finishJob(ctx context.Context, job IJob, ans any, next []IJob) error {
	s.stats.incJobsCompleted()

	if err := s.pushJobs(ctx, next); err != nil {
		return fmt.Errorf("%w: while pushing jobs", err)
	}

	if job.UseInResults() {
		s.results <- Result{
			Job:  job,
			Data: ans,
		}
	}

	return nil
}

func (s *ScrapeMate) pushJobs(ctx context.Context, jobs []IJob) error {
	for i := range jobs {
		if err := s.jobProvider.Push(ctx, jobs[i]); err != nil {
			return err
		}
	}

	return nil
}

type stats struct {
	l                  sync.RWMutex
	numOfJobsCompleted int64
	numOfJobsFailed    int64
	lastActivityAt     time.Time
}

func (o *stats) getStats() (completed, failed int64, lastActivityAt time.Time) {
	o.l.RLock()
	defer o.l.RUnlock()

	return o.numOfJobsCompleted, o.numOfJobsFailed, o.lastActivityAt
}

func (o *stats) incJobsCompleted() {
	o.l.Lock()
	defer o.l.Unlock()

	o.numOfJobsCompleted++
	o.lastActivityAt = time.Now().UTC()
}

func (o *stats) incJobsFailed() {
	o.l.Lock()
	defer o.l.Unlock()

	o.numOfJobsFailed++
	o.lastActivityAt = time.Now().UTC()
}
