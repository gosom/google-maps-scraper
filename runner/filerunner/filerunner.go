package filerunner

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/grid"
	"github.com/gosom/google-maps-scraper/leadsdb"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/runner/resume"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/adapters/writers/jsonwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
)

type fileRunner struct {
	cfg     *runner.Config
	input   io.Reader
	writers []scrapemate.ResultWriter
	app     *scrapemateapp.ScrapemateApp
	outfile *os.File

	resumeIDs      *resume.IdentitySet
	resumeDedup    *resume.IdentitySet
	resumeState    *resume.State
	resumeProgress *resume.ProgressTracker
}

func New(cfg *runner.Config) (runner.Runner, error) {
	if cfg.RunMode != runner.RunModeFile {
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}

	if cfg.Resume {
		if cfg.FastMode {
			return nil, fmt.Errorf("-resume does not support fast mode")
		}

		if cfg.ResultsFile == "stdout" {
			return nil, fmt.Errorf("-resume requires -results to be a file path")
		}

		if err := validateResumeFiles(cfg.ResultsFile); err != nil {
			return nil, err
		}

		if cfg.CustomWriter != "" {
			return nil, fmt.Errorf("-resume does not support custom writers")
		}

		if cfg.LeadsDBAPIKey != "" {
			return nil, fmt.Errorf("-resume does not support LeadsDB output")
		}
	}

	ans := &fileRunner{
		cfg: cfg,
	}

	if err := ans.setInput(); err != nil {
		return nil, err
	}

	if err := ans.setWriters(); err != nil {
		return nil, err
	}

	if err := ans.setApp(); err != nil {
		return nil, err
	}

	return ans, nil
}

func (r *fileRunner) Run(ctx context.Context) (err error) {
	var seedJobs []scrapemate.IJob

	t0 := time.Now().UTC()

	defer func() {
		elapsed := time.Now().UTC().Sub(t0)
		params := map[string]any{
			"job_count": len(seedJobs),
			"duration":  elapsed.String(),
		}

		if err != nil {
			params["error"] = err.Error()
		}

		evt := tlmt.NewEvent("file_runner", params)

		_ = runner.Telemetry().Send(ctx, evt)
	}()

	dedup := deduper.New()

	if r.cfg.Resume {
		dedup = r.resumeDedup
	}

	exitMonitor := exiter.New()
	seedOpts := r.seedJobOptions()

	if r.cfg.GridBBox != "" {
		if r.cfg.FastMode {
			return fmt.Errorf("-fast-mode cannot be used together with -grid-bbox")
		}

		bbox, bboxErr := grid.ParseBoundingBox(r.cfg.GridBBox)
		if bboxErr != nil {
			return fmt.Errorf("invalid -grid-bbox: %w", bboxErr)
		}

		cellCount := grid.EstimateCellCount(bbox, r.cfg.GridCellKm)
		fmt.Fprintf(os.Stderr, "grid scraping: ~%d cells (%.2f km each)\n", cellCount, r.cfg.GridCellKm)

		seedJobs, err = runner.CreateGridSeedJobs(
			r.cfg.LangCode,
			r.input,
			r.cfg.MaxDepth,
			r.cfg.Email,
			bbox,
			r.cfg.GridCellKm,
			r.cfg.Zoom,
			dedup,
			exitMonitor,
			r.cfg.ExtraReviews,
			seedOpts...,
		)
	} else {
		seedJobs, err = runner.CreateSeedJobs(
			r.cfg.FastMode,
			r.cfg.LangCode,
			r.input,
			r.cfg.MaxDepth,
			r.cfg.Email,
			r.cfg.GeoCoordinates,
			r.cfg.Zoom,
			r.cfg.Radius,
			dedup,
			exitMonitor,
			r.cfg.ExtraReviews,
			seedOpts...,
		)
	}

	if err != nil {
		return err
	}

	exitMonitor.SetSeedCount(len(seedJobs))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	exitMonitor.SetCancelFunc(cancel)

	go exitMonitor.Run(ctx)

	err = r.app.Start(ctx, seedJobs...)

	return err
}

func (r *fileRunner) Close(context.Context) error {
	if r.app != nil {
		return r.app.Close()
	}

	if r.input != nil {
		if closer, ok := r.input.(io.Closer); ok {
			return closer.Close()
		}
	}

	if r.outfile != nil {
		return r.outfile.Close()
	}

	return nil
}

func (r *fileRunner) seedJobOptions() []runner.SeedJobOption {
	if !r.cfg.Resume {
		return nil
	}

	return []runner.SeedJobOption{
		runner.WithDeterministicSeedIDs(),
		runner.WithCompletedInputSkipper(r.resumeState.IsInputCompleted),
		runner.WithCompletionTracker(r.resumeProgress),
	}
}

func (r *fileRunner) setInput() error {
	switch r.cfg.InputFile {
	case "stdin":
		r.input = os.Stdin
	default:
		f, err := os.Open(r.cfg.InputFile)
		if err != nil {
			return err
		}

		r.input = f
	}

	return nil
}

func (r *fileRunner) setWriters() error {
	switch {
	case r.cfg.CustomWriter != "":
		parts := strings.Split(r.cfg.CustomWriter, ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid custom writer format: %s", r.cfg.CustomWriter)
		}

		dir, pluginName := parts[0], parts[1]

		customWriter, err := runner.LoadCustomWriter(dir, pluginName)
		if err != nil {
			return err
		}

		r.writers = append(r.writers, customWriter)
	case r.cfg.LeadsDBAPIKey != "":
		r.writers = append(r.writers, leadsdb.New(r.cfg.LeadsDBAPIKey))
	default:
		if !r.cfg.Resume {
			if err := removeResumeState(r.cfg.ResultsFile); err != nil {
				return err
			}
		}

		var resultsWriter io.Writer

		switch r.cfg.ResultsFile {
		case "stdout":
			resultsWriter = os.Stdout
		default:
			f, err := r.openResultsFile()
			if err != nil {
				return err
			}

			r.outfile = f

			resultsWriter = r.outfile
		}

		switch {
		case r.cfg.Resume:
			if err := r.initResume(); err != nil {
				return err
			}

			if r.cfg.JSON {
				r.writers = append(r.writers, resume.NewJSONLAppendWriter(
					resultsWriter,
					r.resumeIDs,
					r.resumeProgress,
					r.outfile.Sync,
				))
			} else {
				writeHeader, headerErr := r.shouldWriteCSVHeader()
				if headerErr != nil {
					return headerErr
				}

				r.writers = append(r.writers, resume.NewCSVAppendWriter(
					csv.NewWriter(resultsWriter),
					writeHeader,
					r.resumeIDs,
					r.resumeProgress,
					r.outfile.Sync,
				))
			}
		case r.cfg.JSON:
			r.writers = append(r.writers, jsonwriter.NewJSONWriter(resultsWriter))
		default:
			csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(resultsWriter))
			r.writers = append(r.writers, csvWriter)
		}
	}

	return nil
}

func validateResumeFiles(resultsPath string) error {
	if _, err := os.Stat(resultsPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if _, err := os.Stat(resume.DefaultStatePath(resultsPath)); err == nil {
		return fmt.Errorf("resume state exists but results file is missing")
	} else if !os.IsNotExist(err) {
		return err
	}

	return nil
}

func removeResumeState(resultsPath string) error {
	err := os.Remove(resume.DefaultStatePath(resultsPath))
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

func (r *fileRunner) openResultsFile() (*os.File, error) {
	if r.cfg.Resume {
		return os.OpenFile(r.cfg.ResultsFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}

	return os.Create(r.cfg.ResultsFile)
}

func (r *fileRunner) initResume() error {
	ids, err := resume.LoadResultIdentities(r.cfg.ResultsFile, r.cfg.JSON)
	if err != nil {
		return err
	}

	state, err := resume.LoadState(resume.DefaultStatePath(r.cfg.ResultsFile))
	if err != nil {
		return err
	}

	r.resumeIDs = ids
	r.resumeDedup = ids.Clone()
	r.resumeState = state
	r.resumeProgress = resume.NewProgressTracker(state)

	return nil
}

func (r *fileRunner) shouldWriteCSVHeader() (bool, error) {
	info, err := os.Stat(r.cfg.ResultsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}

		return false, err
	}

	return info.Size() == 0, nil
}

func (r *fileRunner) setApp() error {
	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(r.cfg.Concurrency),
		scrapemateapp.WithExitOnInactivity(r.cfg.ExitOnInactivityDuration),
	}

	if len(r.cfg.Proxies) > 0 {
		opts = append(opts,
			scrapemateapp.WithProxies(r.cfg.Proxies),
		)
	}

	if !r.cfg.FastMode {
		if r.cfg.Debug {
			opts = append(opts, scrapemateapp.WithJS(
				scrapemateapp.Headfull(),
				scrapemateapp.DisableImages(),
			))
		} else {
			opts = append(opts, scrapemateapp.WithJS(scrapemateapp.DisableImages()))
		}
	} else {
		opts = append(opts, scrapemateapp.WithStealth("firefox"))
	}

	opts = runner.AppendBrowserCapacityOptions(opts, r.cfg)

	if !r.cfg.DisablePageReuse {
		opts = append(opts,
			scrapemateapp.WithPageReuseLimit(2),
			scrapemateapp.WithBrowserReuseLimit(200),
		)
	}

	matecfg, err := scrapemateapp.NewConfig(
		r.writers,
		opts...,
	)
	if err != nil {
		return err
	}

	r.app, err = scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		return err
	}

	return nil
}
