package filerunner

import (
	"context"
	"fmt"

	"github.com/gosom/scrapemate"
	"golang.org/x/sync/errgroup"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/grid"
)

type gridFilterWriter struct {
	next       scrapemate.ResultWriter
	bounds     grid.BoundingBox
	onExcluded func(*gmaps.Entry) error
}

func (w *gridFilterWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	filtered := make(chan scrapemate.Result)

	var group errgroup.Group

	group.Go(func() error {
		defer cancel()

		return w.next.Run(ctx, filtered)
	})
	group.Go(func() error {
		defer close(filtered)

		return w.forward(ctx, in, filtered)
	})

	return group.Wait()
}

func (w *gridFilterWriter) forward(ctx context.Context, in <-chan scrapemate.Result, out chan<- scrapemate.Result) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result, ok := <-in:
			if !ok {
				return nil
			}

			entry, ok := result.Data.(*gmaps.Entry)
			if !ok || entry == nil {
				return fmt.Errorf("strict grid filter expected a place entry, got %T", result.Data)
			}

			if !w.bounds.Contains(entry.Latitude, entry.Longtitude) {
				if w.onExcluded != nil {
					if err := w.onExcluded(entry); err != nil {
						return fmt.Errorf("record excluded grid place: %w", err)
					}
				}

				continue
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- result:
			}
		}
	}
}

func (r *fileRunner) filterGridWriters() error {
	bounds, err := grid.ParseBoundingBox(r.cfg.GridBBox)
	if err != nil {
		return fmt.Errorf("invalid -grid-bbox: %w", err)
	}

	for i, writer := range r.writers {
		filter := &gridFilterWriter{next: writer, bounds: bounds}
		if r.resumeProgress != nil {
			filter.onExcluded = func(entry *gmaps.Entry) error {
				// Excluded places are intentionally handled, so resume can finish
				// this input even when every discovered place is outside the box.
				return r.resumeProgress.ResultPersisted(entry.ID, r.outfile.Sync)
			}
		}

		r.writers[i] = filter
	}

	return nil
}
