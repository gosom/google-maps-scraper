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
	"time"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/grid"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/gosom/google-maps-scraper/web/sqlite"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/adapters/writers/jsonwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	"golang.org/x/sync/errgroup"
)

type webrunner struct {
	srv       *web.Server
	svc       *web.Service
	cfg       *runner.Config
	setupMate func(context.Context, io.Writer, *web.Job) (mateRunner, error)
}

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

	srv, err := web.New(svc, cfg.Addr)
	if err != nil {
		return nil, err
	}

	ans := webrunner{
		srv:       srv,
		svc:       svc,
		cfg:       cfg,
		setupMate: defaultSetupMate(cfg),
	}

	return &ans, nil
}

func (w *webrunner) Run(ctx context.Context) error {
	egroup, ctx := errgroup.WithContext(ctx)

	egroup.Go(func() error {
		return w.work(ctx)
	})

	egroup.Go(func() error {
		return w.srv.Start(ctx)
	})

	return egroup.Wait()
}

func (w *webrunner) Close(context.Context) error {
	return nil
}

func (w *webrunner) work(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			jobs, err := w.svc.SelectPending(ctx)
			if err != nil {
				return err
			}

			for i := range jobs {
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

	fileExt := ".csv"
	if job.Data.EffectiveOutputFormat() == web.OutputFormatJSON {
		fileExt = ".json"
	}

	outpath := filepath.Join(w.cfg.DataFolder, job.ID+fileExt)

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

	mate, err := setupMate(ctx, outfile, job)
	if err != nil {
		job.Status = web.StatusFailed

		err2 := w.svc.Update(ctx, job)
		if err2 != nil {
			log.Printf("failed to update job status: %v", err2)
		}

		return err
	}

	defer mate.Close()

	coords := job.Data.GeoCoordinates()

	dedup := deduper.New()
	exitMonitor := exiter.New()

	var seedJobs []scrapemate.IJob

	if job.Data.GridBBox != "" {
		if job.Data.FastMode {
			return fmt.Errorf("grid scraping cannot be used together with fast mode")
		}

		bbox, bboxErr := grid.ParseBoundingBox(job.Data.GridBBox)
		if bboxErr != nil {
			return fmt.Errorf("invalid grid bounding box: %w", bboxErr)
		}

		cellKm := job.Data.GridCellKm
		if cellKm <= 0 {
			cellKm = 1.0
		}

		seedJobs, err = runner.CreateGridSeedJobs(
			job.Data.Lang,
			strings.NewReader(strings.Join(job.Data.Keywords, "\n")),
			job.Data.Depth,
			job.Data.Email,
			bbox,
			cellKm,
			job.Data.Zoom,
			dedup,
			exitMonitor,
			w.cfg.ExtraReviews || job.Data.ExtraReviews,
		)
	} else {
		seedJobs, err = runner.CreateSeedJobs(
			job.Data.FastMode,
			job.Data.Lang,
			strings.NewReader(strings.Join(job.Data.Keywords, "\n")),
			job.Data.Depth,
			job.Data.Email,
			coords,
			job.Data.Zoom,
			func() float64 {
				if job.Data.Radius <= 0 {
					return 10000 // 10 km
				}

				return float64(job.Data.Radius)
			}(),
			dedup,
			exitMonitor,
			w.cfg.ExtraReviews || job.Data.ExtraReviews,
		)
	}
	if err != nil {
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

		exitMonitor.SetCancelFunc(cancel)

		go exitMonitor.Run(mateCtx)

		err = mate.Start(mateCtx, seedJobs...)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			cancel()

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

func defaultSetupMate(cfg *runner.Config) func(context.Context, io.Writer, *web.Job) (mateRunner, error) {
	return func(_ context.Context, writer io.Writer, job *web.Job) (mateRunner, error) {
		concurrency := cfg.Concurrency
		if job.Data.Concurrency > 0 {
			concurrency = job.Data.Concurrency
		}

		opts := []func(*scrapemateapp.Config) error{
			scrapemateapp.WithConcurrency(concurrency),
			scrapemateapp.WithExitOnInactivity(time.Minute * 3),
		}

		if !job.Data.FastMode {
			opts = append(opts,
				scrapemateapp.WithJS(scrapemateapp.DisableImages()),
			)
		} else {
			opts = append(opts,
				scrapemateapp.WithStealth("firefox"),
			)
		}

		if job.Data.PagesPerBrowser > 1 {
			opts = append(opts, scrapemateapp.WithMaxPagesPerBrowser(job.Data.PagesPerBrowser))
		}

		if job.Data.BrowserPoolSize > 0 {
			opts = append(opts, scrapemateapp.WithBrowserPoolSize(job.Data.BrowserPoolSize))
		}

		if job.Data.PagesPerBrowser <= 1 && job.Data.BrowserPoolSize <= 0 {
			opts = runner.AppendBrowserCapacityOptions(opts, cfg)
		}

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

		pageReuseDisabled := cfg.DisablePageReuse || job.Data.DisablePageReuse

		if !pageReuseDisabled {
			opts = append(opts,
				scrapemateapp.WithPageReuseLimit(2),
				scrapemateapp.WithBrowserReuseLimit(200),
			)
		}

		log.Printf("job %s has proxy: %v", job.ID, hasProxy)

		var resultsWriter scrapemate.ResultWriter

		if job.Data.EffectiveOutputFormat() == web.OutputFormatJSON {
			resultsWriter = jsonwriter.NewJSONWriter(writer)
		} else {
			resultsWriter = csvwriter.NewCsvWriter(csv.NewWriter(writer))
		}

		writers := []scrapemate.ResultWriter{resultsWriter}

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
