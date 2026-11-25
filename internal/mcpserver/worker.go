package mcpserver

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/grid"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/runner/filerunner"
	"github.com/gosom/google-maps-scraper/runner/resume"
)

const jsonResults = "results.jsonl"
const csvResults = "results.csv"

// Work runs one persisted scrape job independently of the MCP connection.
func Work(ctx context.Context, dir string) error {
	lock := flock.New(filepath.Join(dir, "worker.lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return err
	}
	defer lock.Close()

	s := store{root: filepath.Dir(dir)}

	j, err := s.read(filepath.Base(dir))
	if err != nil {
		return err
	}

	if j.Status != statusQueued {
		return fmt.Errorf("job is %s", j.Status)
	}

	j.Status = statusRunning
	if err := writeState(dir, &j); err != nil {
		return err
	}

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	heartbeat := make(chan struct{})

	done := make(chan struct{})
	go func() {
		defer close(done)

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeat:
				return
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(dir, "cancel")); err == nil {
					cancel()
				}

				now := time.Now()
				_ = os.Chtimes(filepath.Join(dir, "worker.lock"), now, now)
			}
		}
	}()

	runErr := runScrape(runCtx, dir, &j)

	close(heartbeat)
	<-done

	switch {
	case errors.Is(runCtx.Err(), context.Canceled):
		j.Status = statusInterrupted
		if _, err := os.Stat(filepath.Join(dir, "cancel")); err == nil {
			j.Status = statusCancelled
		}
	case runCtx.Err() != nil:
		j.Status, j.Error = statusFailed, runCtx.Err().Error()
	case runErr != nil:
		j.Status, j.Error = statusFailed, runErr.Error()
	default:
		j.Status = statusCompleted
	}

	return errors.Join(runErr, writeState(dir, &j))
}

func runScrape(ctx context.Context, dir string, j *job) error {
	input := filepath.Join(dir, "query.txt")
	if err := os.WriteFile(input, []byte(j.Plan.Query+"\n"), 0o600); err != nil {
		return err
	}

	jsonFile, err := os.OpenFile(filepath.Join(dir, jsonResults), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer jsonFile.Close()

	csvFile, err := os.OpenFile(filepath.Join(dir, csvResults), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer csvFile.Close()

	csvWriter := csv.NewWriter(csvFile)

	headers := (&gmaps.Entry{}).CsvHeaders()
	if err := csvWriter.Write(headers); err != nil {
		return err
	}

	csvWriter.Flush()

	if err := csvWriter.Error(); err != nil {
		return err
	}

	writer := &jobWriter{
		ctx: ctx, dir: dir, job: j, ids: resume.NewIdentitySet(),
		json: json.NewEncoder(jsonFile), csv: csvWriter,
	}
	p := &j.Plan

	var firstScrapeError error

	for index, cell := range p.Cells {
		if err := ctx.Err(); err != nil {
			return err
		}

		writer.cellLimit = (p.Limit*(index+1) + len(p.Cells) - 1) / len(p.Cells)

		scrapeErr := scrapeCell(ctx, input, p, cell, writer)
		if writer.err != nil {
			return errors.Join(scrapeErr, writer.err)
		}

		if scrapeErr != nil && firstScrapeError == nil {
			firstScrapeError = scrapeErr
		}

		j.CellsDone++
		if err := writeState(dir, j); err != nil {
			return err
		}
	}

	return errors.Join(firstScrapeError, writer.writeOverflow())
}

func (p *plan) contains(entry *gmaps.Entry) bool {
	coordinate := point{entry.Latitude, entry.Longtitude}
	if !validPoint(coordinate) {
		return false
	}

	if p.Bounds != nil {
		return p.Bounds.Contains(coordinate.Lat, coordinate.Lon)
	}

	return distanceKM(p.Center, coordinate) <= p.RadiusKM
}

func scrapeCell(ctx context.Context, input string, p *plan, cell grid.Cell, writer scrapemate.ResultWriter) error {
	radius := p.RadiusKM * 1000

	depth := min(10, (p.Limit+19)/20)
	if p.CellSizeKM > 0 {
		// A square's full-width radius covers its farthest corner with overlap.
		radius = p.CellSizeKM * 1000
		depth = min(depth, 3)
	}

	zoom := 15
	if radius > 3000 {
		zoom -= int(math.Ceil(math.Log2(radius / 3000)))
	}

	cfg := &runner.Config{
		RunMode:                  runner.RunModeFile,
		InputFile:                input,
		FastMode:                 p.Mode == modeFast,
		ExtraReviews:             p.ExtraReviews,
		GeoCoordinates:           cell.GeoCoordinates(),
		Radius:                   radius,
		Zoom:                     zoom,
		LangCode:                 "en",
		MaxDepth:                 depth,
		Concurrency:              4,
		ExitOnInactivityDuration: 2 * time.Minute,
	}

	worker, err := filerunner.NewWithResultWriter(cfg, writer)
	if err != nil {
		return err
	}

	var (
		firstFailure   error
		captureFailure sync.Once
	)

	runErr := worker.Run(gmaps.WithSearchErrorReporter(ctx, func(err error) {
		if !errors.Is(err, context.Canceled) {
			captureFailure.Do(func() { firstFailure = err })
		}
	}))

	closeErr := worker.Close(ctx)
	if errors.Is(runErr, context.Canceled) && ctx.Err() == nil {
		runErr = nil
	}

	return errors.Join(runErr, closeErr, firstFailure)
}
