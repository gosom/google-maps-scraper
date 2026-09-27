package filerunner

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/runner"
)

// Collector v2 from the command line (`-collector v2`): the queries file, one search at a time,
// rows written as each search ends (CSV, or JSON lines with -json), and one line per search on
// stderr saying how it went. Nothing here starts a browser.

// runV2 runs every query through collector v2 inside its bound.
func (r *fileRunner) runV2(ctx context.Context) error {
	queries, err := runner.ReadSteadyQueries(r.input)
	if err != nil {
		return err
	}

	collector, err := gmaps.NewSteadyCollector(r.cfg.Steady, r.cfg.Proxies)
	if err != nil {
		return err
	}
	defer collector.Close()

	cfg := collector.Config()
	bound := cfg.JobBound(len(queries))

	fmt.Fprintf(os.Stderr, "collector v2: %d searches, at most %s (%d page(s) a search, %d contact page(s), %s apart, %s a search)\n",
		len(queries), bound.Round(time.Second), cfg.Pages, cfg.ContactPages, cfg.Delay, cfg.QueryBudget)

	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	emit, flush, err := r.v2Writer()
	if err != nil {
		return err
	}

	started := time.Now()

	err = collector.Run(ctx, queries, emit, func(run gmaps.SearchRun) {
		fmt.Fprintf(os.Stderr, "%-8s %5.0fs  %3d places  %3d sites  %3d with email  %2d unreadable  %2d skipped  %d refused  %s%s\n",
			run.Status, run.Seconds(), run.PlacesFound, run.Websites, run.EmailsFound, run.EmailFetchFailed,
			run.EmailSkipped, run.Blocks, run.Search, errSuffix(run.Error))
	})

	if flushErr := flush(); flushErr != nil && err == nil {
		err = flushErr
	}

	fmt.Fprintf(os.Stderr, "collector v2: done in %s of %s\n", time.Since(started).Round(time.Second), bound.Round(time.Second))

	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("collector v2 ran out of its %s bound; the searches finished so far are written", bound.Round(time.Second))
	}

	return err
}

func errSuffix(msg string) string {
	if msg == "" {
		return ""
	}

	return " (" + msg + ")"
}

// v2Writer writes v2's places to the results file: CSV with gmaps.Entry's columns, or one JSON
// object a line.
func (r *fileRunner) v2Writer() (emit func(gmaps.SteadyQuery, []*gmaps.Entry) error, flush func() error, err error) {
	var out io.Writer = os.Stdout

	if r.cfg.ResultsFile != "stdout" {
		f, err := os.Create(r.cfg.ResultsFile)
		if err != nil {
			return nil, nil, err
		}

		r.outfile = f
		out = f
	}

	if r.cfg.JSON {
		enc := json.NewEncoder(out)

		return func(_ gmaps.SteadyQuery, entries []*gmaps.Entry) error {
			for _, e := range entries {
				if err := enc.Encode(e); err != nil {
					return err
				}
			}

			return nil
		}, func() error { return nil }, nil
	}

	cw := csv.NewWriter(out)
	if err := cw.Write((&gmaps.Entry{}).CsvHeaders()); err != nil {
		return nil, nil, err
	}

	return func(_ gmaps.SteadyQuery, entries []*gmaps.Entry) error {
			for _, e := range entries {
				if err := cw.Write(e.CsvRow()); err != nil {
					return err
				}
			}

			cw.Flush()

			return cw.Error()
		}, func() error {
			cw.Flush()

			return cw.Error()
		}, nil
}
