package filerunner

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gosom/google-maps-scraper/runner"
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
}

func New(cfg *runner.Config) (runner.Runner, error) {
	if cfg.RunMode != runner.RunModeFile {
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
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

func (r *fileRunner) Run(ctx context.Context) error {
	seedJobs, err := runner.CreateSeedJobs(
		r.cfg.LangCode,
		r.input,
		r.cfg.MaxDepth,
		r.cfg.Email,
		r.cfg.GeoCoordinates,
		r.cfg.Zoom,
	)
	if err != nil {
		return err
	}

	return r.app.Start(ctx, seedJobs...)
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
	if r.cfg.CustomWriter != "" {
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
	} else {
		var resultsWriter io.Writer

		switch r.cfg.ResultsFile {
		case "stdout":
			resultsWriter = os.Stdout
		default:
			f, err := os.Create(r.cfg.ResultsFile)
			if err != nil {
				return err
			}

			r.outfile = f

			resultsWriter = r.outfile
		}

		csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(resultsWriter))

		if r.cfg.JSON {
			r.writers = append(r.writers, jsonwriter.NewJSONWriter(resultsWriter))
		} else {
			r.writers = append(r.writers, csvWriter)
		}
	}

	return nil
}

func (r *fileRunner) setApp() error {
	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(r.cfg.Concurrency),
		scrapemateapp.WithExitOnInactivity(r.cfg.ExitOnInactivityDuration),
	}

	if r.cfg.Debug {
		opts = append(opts, scrapemateapp.WithJS(
			scrapemateapp.Headfull(),
			scrapemateapp.DisableImages(),
		),
		)
	} else {
		opts = append(opts, scrapemateapp.WithJS(scrapemateapp.DisableImages()))
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
