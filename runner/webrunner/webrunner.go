// Package webrunner provides a web-based runner for the Google Maps scraper.
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
	"strconv"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/gosom/google-maps-scraper/web/sqlite"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	"golang.org/x/sync/errgroup"
)

type webrunner struct {
	srv *web.Server
	svc *web.Service
	cfg *runner.Config
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
		srv: srv,
		svc: svc,
		cfg: cfg,
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

	outpath := filepath.Join(w.cfg.DataFolder, job.ID+".csv")

	outfile, err := os.Create(outpath)
	if err != nil {
		return err
	}

	defer func() {
		_ = outfile.Close()
	}()

	log.Printf("🔧 Setting up scrapemate for job %s", job.ID)

	mate, eventChan, err := w.setupMate(ctx, outfile, job)
	if err != nil {
		log.Printf("❌ Failed to setup scrapemate for job %s: %v", job.ID, err)
		job.Status = web.StatusFailed

		err2 := w.svc.Update(ctx, job)
		if err2 != nil {
			log.Printf("failed to update job status: %v", err2)
		}

		return err
	}

	log.Printf("✅ Scrapemate setup completed for job %s", job.ID)

	defer func() {
		log.Printf("🧹 File cleanup for job %s", job.ID)
	}()

	var coords string
	if job.Data.Lat != "" && job.Data.Lon != "" {
		coords = job.Data.Lat + "," + job.Data.Lon
	}

	dedup := deduper.New()

	// Phase 8.2: Initialize deduper with existing CIDs for immediate pre-filtering benefit
	if len(job.Data.ExistingCIDs) > 0 {
		ctx := context.Background()

		for _, cid := range job.Data.ExistingCIDs {
			if cid != "" {
				// Convert decimal CID to hex format for DataID matching
				// CID is the decimal representation of the second hex part in DataID
				cidDecimal, err := strconv.ParseUint(cid, 10, 64)
				if err == nil {
					cidHex := fmt.Sprintf("0x%x", cidDecimal)
					// Store the hex format for DataID matching
					dedup.AddIfNotExists(ctx, cidHex)
				}
			}
		}

		log.Printf("🔄 Initialized deduper with %d existing CIDs for pre-filtering", len(job.Data.ExistingCIDs))
	}

	exitMonitor := exiter.New()

	seedJobs, err := runner.CreateSeedJobs(
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
		w.cfg.ExtraReviews,
	)
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

		// Start event translator with server context (NOT job timeout) if streaming plugin is active
		if eventChan != nil {
			go w.translateEvents(ctx, job.ID, eventChan)
			log.Printf("📡 Started event translator for job %s (runs until plugin completion or server shutdown)", job.ID)
		}

		go exitMonitor.Run(mateCtx)

		log.Printf("🚀 Starting scrapemate for job %s with %d seed jobs (concurrency: %d)", job.ID, len(seedJobs), w.cfg.Concurrency)
		log.Printf("📍 Job %s context timeout: %v", job.ID, time.Duration(allowedSeconds)*time.Second)

		err = mate.Start(mateCtx, seedJobs...)

		log.Printf("✅ Scrapemate finished for job %s (error: %v)", job.ID, err)

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

	log.Printf("🧹 Cleaning up scrapemate resources for job %s", job.ID)
	mate.Close()
	log.Printf("✅ Resource cleanup completed for job %s", job.ID)

	job.Status = web.StatusOK

	return w.svc.Update(ctx, job)
}

func (w *webrunner) setupMate(ctx context.Context, writer io.Writer, job *web.Job) (*scrapemateapp.ScrapemateApp, <-chan plugins.StreamEvent, error) {
	log.Printf("🔧 Setting up scrapemate configuration for job %s", job.ID)

	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(w.cfg.Concurrency),
		scrapemateapp.WithExitOnInactivity(time.Minute * 3),
	}

	log.Printf("📋 Base config: concurrency=%d, exit_on_inactivity=3m", w.cfg.Concurrency)

	if !job.Data.FastMode {
		log.Printf("🌐 Job %s: Using JS mode with disabled images", job.ID)

		opts = append(opts,
			scrapemateapp.WithJS(scrapemateapp.DisableImages()),
		)
	} else {
		log.Printf("⚡ Job %s: Using fast mode with firefox stealth", job.ID)

		opts = append(opts,
			scrapemateapp.WithStealth("firefox"),
		)
	}

	hasProxy := false

	if len(w.cfg.Proxies) > 0 {
		opts = append(opts, scrapemateapp.WithProxies(w.cfg.Proxies))
		hasProxy = true
	} else if len(job.Data.Proxies) > 0 {
		opts = append(opts,
			scrapemateapp.WithProxies(job.Data.Proxies),
		)
		hasProxy = true
	}

	if !w.cfg.DisablePageReuse {
		log.Printf("🔄 Job %s: Enabling page reuse with limits", job.ID)

		opts = append(opts,
			scrapemateapp.WithPageReuseLimit(2),
			scrapemateapp.WithPageReuseLimit(200),
		)
	} else {
		log.Printf("🚫 Job %s: Page reuse disabled", job.ID)
	}

	log.Printf("job %s has proxy: %v", job.ID, hasProxy)

	// Setup writers - check for custom plugin or use default CSV writer
	writers, streamChan, err := w.setupWriters(ctx, writer, job)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to setup writers: %w", err)
	}

	log.Printf("🔧 Creating scrapemate config for job %s", job.ID)

	matecfg, err := scrapemateapp.NewConfig(
		writers,
		opts...,
	)
	if err != nil {
		log.Printf("❌ Failed to create scrapemate config for job %s: %v", job.ID, err)
		return nil, nil, err
	}

	log.Printf("🚀 Creating scrapemate app instance for job %s", job.ID)

	app, err := scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		log.Printf("❌ Failed to create scrapemate app for job %s: %v", job.ID, err)
		return nil, nil, err
	}

	log.Printf("✅ Scrapemate app created successfully for job %s", job.ID)

	return app, streamChan, err
}

// setupWriters sets up the result writers, supporting both custom plugins and default CSV writer
func (w *webrunner) setupWriters(_ context.Context, writer io.Writer, job *web.Job) ([]scrapemate.ResultWriter, <-chan plugins.StreamEvent, error) {
	var writers []scrapemate.ResultWriter

	// Check if custom writer plugin is configured
	if w.cfg.CustomWriter != "" {
		log.Printf("🔌 Setting up custom writer plugin: %s", w.cfg.CustomWriter)

		// Parse plugin configuration (format: "dir:pluginName")
		parts := strings.Split(w.cfg.CustomWriter, ":")
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("invalid custom writer format, expected 'dir:pluginName', got: %s", w.cfg.CustomWriter)
		}

		dir, pluginName := parts[0], parts[1]

		// Create new custom plugin instance using factory pattern
		customWriter, err := runner.CreateCustomWriter(dir, pluginName)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create custom writer plugin: %w", err)
		}

		// Check if this is a streaming plugin that needs event translation
		// Define interface to get event channel from plugin
		type GetEventChannelInterface interface {
			GetEventChannel() <-chan plugins.StreamEvent
		}

		var eventChan <-chan plugins.StreamEvent

		if eventSource, ok := customWriter.(GetEventChannelInterface); ok {
			log.Printf("🔗 Detected streaming plugin, event translation will be started with job timeout")

			eventChan = eventSource.GetEventChannel()
		} else {
			log.Printf("ℹ️ Plugin does not implement GetEventChannel interface")
		}

		// Set existing CIDs if available in job data
		if len(job.Data.ExistingCIDs) > 0 {
			type SetCIDsInterface interface {
				SetExistingCIDs(cids []string)
			}

			if cidSetter, ok := customWriter.(SetCIDsInterface); ok {
				cidSetter.SetExistingCIDs(job.Data.ExistingCIDs)
				log.Printf("🔄 Loaded %d existing CIDs into plugin", len(job.Data.ExistingCIDs))
			}
		}

		// Set review limit configuration
		type SetReviewLimitInterface interface {
			SetReviewLimit(limit int)
		}

		if reviewLimitSetter, ok := customWriter.(SetReviewLimitInterface); ok {
			limit := job.Data.ReviewLimit
			if limit <= 0 {
				limit = 10 // Default review limit
			}

			reviewLimitSetter.SetReviewLimit(limit)
			log.Printf("📊 Set review limit to %d in plugin", limit)
		}

		writers = append(writers, customWriter)

		log.Printf("✅ Custom writer plugin loaded successfully")

		return writers, eventChan, nil
	}

	// Default to CSV writer
	log.Printf("📝 Using default CSV writer")

	csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(writer))
	writers = append(writers, csvWriter)

	return writers, nil, nil
}

// translateEvents reads events from the plugin and re-broadcasts them with the correct web job ID
func (w *webrunner) translateEvents(ctx context.Context, webJobID string, events <-chan plugins.StreamEvent) {
	defer log.Printf("📡 Event translator stopped for job %s", webJobID)

	for {
		select {
		case event, ok := <-events:
			if !ok {
				// Plugin closed its channel, job is done
				return
			}

			// Fix the job ID to use web job ID instead of scrapemate job ID
			event.JobID = webJobID

			// Re-broadcast through server
			if w.srv != nil {
				w.srv.BroadcastEvent(webJobID, event)
			}

			log.Printf("🔄 Translated %s event from plugin job to web job %s", event.Type, webJobID)

		case <-ctx.Done():
			// Context cancelled (server shutdown or job timeout)
			return
		}
	}
}
