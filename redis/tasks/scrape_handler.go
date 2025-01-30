package tasks

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Vector/vector-leads-scraper/deduper"
	"github.com/Vector/vector-leads-scraper/exiter"
	"github.com/Vector/vector-leads-scraper/runner"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	"github.com/hibiken/asynq"
)

// CreateScrapeTask creates a new scrape task with the given payload
func CreateScrapeTask(payload *ScrapePayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal scrape payload: %w", err)
	}
	return asynq.NewTask(TypeScrapeGMaps, data), nil
}

func (h *Handler) processScrapeTask(ctx context.Context, task *asynq.Task) error {
	var payload ScrapePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal scrape payload: %w", err)
	}

	if len(payload.Keywords) == 0 {
		return fmt.Errorf("no keywords provided")
	}

	outpath := filepath.Join(h.dataFolder, payload.JobID+".csv")
	outfile, err := os.Create(outpath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outfile.Close()

	mate, err := h.setupMate(ctx, outfile, &payload)
	if err != nil {
		return fmt.Errorf("failed to setup scrapemate: %w", err)
	}
	defer mate.Close()

	var coords string
	if payload.Lat != "" && payload.Lon != "" {
		coords = payload.Lat + "," + payload.Lon
	}

	dedup := deduper.New()
	exitMonitor := exiter.New()

	seedJobs, err := runner.CreateSeedJobs(
		payload.FastMode,
		payload.Lang,
		strings.NewReader(strings.Join(payload.Keywords, "\n")),
		payload.Depth,
		payload.Email,
		coords,
		payload.Zoom,
		func() float64 {
			if payload.Radius <= 0 {
				return 10000 // 10 km
			}
			return float64(payload.Radius)
		}(),
		dedup,
		exitMonitor,
	)
	if err != nil {
		return fmt.Errorf("failed to create seed jobs: %w", err)
	}

	if len(seedJobs) > 0 {
		exitMonitor.SetSeedCount(len(seedJobs))

		allowedSeconds := max(60, len(seedJobs)*10*payload.Depth/50+120)
		if payload.MaxTime > 0 {
			if payload.MaxTime.Seconds() < 180 {
				allowedSeconds = 180
			} else {
				allowedSeconds = int(payload.MaxTime.Seconds())
			}
		}

		log.Printf("running job %s with %d seed jobs and %d allowed seconds", payload.JobID, len(seedJobs), allowedSeconds)

		jobCtx, jobCancel := context.WithTimeout(ctx, time.Duration(allowedSeconds)*time.Second)
		defer jobCancel()

		exitMonitor.SetCancelFunc(jobCancel)
		go exitMonitor.Run(jobCtx)

		if err := mate.Start(jobCtx, seedJobs...); err != nil {
			if err != context.DeadlineExceeded && err != context.Canceled {
				return fmt.Errorf("failed to run scraping: %w", err)
			}
		}
	}

	return nil
}

func (h *Handler) setupMate(_ context.Context, writer io.Writer, payload *ScrapePayload) (*scrapemateapp.ScrapemateApp, error) {
	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(h.concurrency),
		scrapemateapp.WithExitOnInactivity(time.Minute * 3),
	}

	if !payload.FastMode {
		opts = append(opts,
			scrapemateapp.WithJS(scrapemateapp.DisableImages()),
		)
	} else {
		opts = append(opts,
			scrapemateapp.WithStealth("firefox"),
		)
	}

	hasProxy := false

	if len(h.proxies) > 0 {
		opts = append(opts, scrapemateapp.WithProxies(h.proxies))
		hasProxy = true
	} else if len(payload.Proxies) > 0 {
		opts = append(opts,
			scrapemateapp.WithProxies(payload.Proxies),
		)
		hasProxy = true
	}

	if !h.disableReuse {
		opts = append(opts,
			scrapemateapp.WithPageReuseLimit(2),
			scrapemateapp.WithPageReuseLimit(200),
		)
	}

	log.Printf("job %s has proxy: %v", payload.JobID, hasProxy)

	csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(writer))
	writers := []scrapemate.ResultWriter{csvWriter}

	matecfg, err := scrapemateapp.NewConfig(
		writers,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	return scrapemateapp.NewScrapeMateApp(matecfg)
} 