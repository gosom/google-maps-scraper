package memory

import (
	"context"

	"github.com/gosom/scrapemate"
)

var _ scrapemate.JobProvider = (*memoryProvider)(nil)

// New creates a new memory provider
func New() scrapemate.JobProvider {
	return &memoryProvider{
		p0: make(chan scrapemate.IJob),
		p1: make(chan scrapemate.IJob),
		p2: make(chan scrapemate.IJob),
	}
}

type memoryProvider struct {
	p0 chan scrapemate.IJob
	p1 chan scrapemate.IJob
	p2 chan scrapemate.IJob
}

// Jobs returns the channel to get jobs from
//
//nolint:gocritic // we need to return a read only channel
func (o *memoryProvider) Jobs(ctx context.Context) (<-chan scrapemate.IJob, <-chan error) {
	out := make(chan scrapemate.IJob)
	errc := make(chan error, 1)

	go func() {
		for {
			var job scrapemate.IJob

			select {
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			case job = <-o.p0:
				out <- job
			default:
				select {
				case <-ctx.Done():
					out <- job
				case job = <-o.p0:
					out <- job
				case job = <-o.p1:
					out <- job
				default:
					select {
					case <-ctx.Done():
						errc <- ctx.Err()
						return
					case job = <-o.p0:
						out <- job
					case job = <-o.p1:
						out <- job
					case job = <-o.p2:
						out <- job
					}
				}
			}
		}
	}()

	return out, errc
}

// Push pushes a job to the job provider
func (o *memoryProvider) Push(ctx context.Context, job scrapemate.IJob) error {
	// I start a gorouting here and don't wait for it to finish
	// not sure if this is a good idea
	go func() {
		var ch chan scrapemate.IJob

		switch job.GetPriority() {
		case scrapemate.PriorityHigh:
			ch = o.p0
		case scrapemate.PriorityMedium:
			ch = o.p1
		case scrapemate.PriorityLow:
			ch = o.p2
		default:
			ch = o.p0
		}

		select {
		case ch <- job:
			return
		case <-ctx.Done():
			return
		}
	}()

	return nil
}
