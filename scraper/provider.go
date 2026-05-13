package scraper

import (
	"context"
	"fmt"
	"sync"

	"github.com/gosom/scrapemate"
)

var _ scrapemate.JobProvider = (*Provider)(nil)

// Provider is a simple FIFO bridge between River workers and ScrapeMate.
// Root jobs are submitted by the River worker and child jobs are pushed by ScrapeMate.
type Provider struct {
	inChan  chan queuedJob
	jobChan chan scrapemate.IJob
	errChan chan error

	mu sync.Mutex

	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    bool
}

type queuedJob struct {
	job scrapemate.IJob
}

// NewProvider creates a new Provider with the given buffer size.
// If bufferSize <= 0, defaults to 100.
func NewProvider(bufferSize int) *Provider {
	if bufferSize <= 0 {
		bufferSize = 100
	}

	p := &Provider{
		inChan:  make(chan queuedJob, bufferSize*2),
		jobChan: make(chan scrapemate.IJob, bufferSize),
		errChan: make(chan error, 1),
		done:    make(chan struct{}),
	}

	p.wg.Add(1)
	go p.run()

	return p
}

// Jobs returns channels for ScrapeMate to consume jobs and errors.
func (p *Provider) Jobs(_ context.Context) (<-chan scrapemate.IJob, <-chan error) { //nolint:gocritic // unnamedResult: interface implementation requires exact signature
	return p.jobChan, p.errChan
}

// Push adds a child job to the provider queue.
func (p *Provider) Push(ctx context.Context, job scrapemate.IJob) error {
	return p.enqueue(ctx, queuedJob{job: job}, false)
}

// Submit adds a root job to the provider queue.
func (p *Provider) Submit(ctx context.Context, job scrapemate.IJob) error {
	return p.enqueue(ctx, queuedJob{job: job}, true)
}

func (p *Provider) enqueue(ctx context.Context, item queuedJob, isSubmit bool) error {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()

	if closed {
		return fmt.Errorf("provider closed")
	}

	select {
	case <-p.done:
		return fmt.Errorf("provider closed")
	case p.inChan <- item:
		return nil
	case <-ctx.Done():
		if isSubmit {
			return fmt.Errorf("failed to submit job: %w", ctx.Err())
		}

		return ctx.Err()
	}
}

func (p *Provider) run() {
	defer p.wg.Done()

	for {
		select {
		case <-p.done:
			return
		case item := <-p.inChan:
			select {
			case <-p.done:
				return
			case p.jobChan <- item.job:
			}
		}
	}
}

// Close closes the provider and job output channel.
func (p *Provider) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		close(p.done)
		p.wg.Wait()
		close(p.jobChan)
	})
}
