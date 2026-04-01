package runner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"plugin"
	"strconv"
	"strings"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

// SeedJobConfig groups all parameters needed to create seed jobs.
type SeedJobConfig struct {
	FastMode       bool
	LangCode       string
	Input          io.Reader
	MaxDepth       int
	Email          bool
	Images         bool
	Debug          bool
	ReviewsMax     int
	GeoCoordinates string
	Zoom           int
	Radius         float64
	Dedup          deduper.Deduper
	ExitMonitor    exiter.Exiter
	ExtraReviews   bool
	MaxResults     int
}

func CreateSeedJobs(cfg SeedJobConfig) (jobs []scrapemate.IJob, err error) {
	var lat, lon float64

	if cfg.FastMode {
		if cfg.GeoCoordinates == "" {
			return nil, fmt.Errorf("geo coordinates are required in fast mode")
		}

		parts := strings.Split(cfg.GeoCoordinates, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid geo coordinates: %s", cfg.GeoCoordinates)
		}

		lat, err = strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid latitude: %w", err)
		}

		lon, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid longitude: %w", err)
		}

		if lat < -90 || lat > 90 {
			return nil, fmt.Errorf("invalid latitude: %f", lat)
		}

		if lon < -180 || lon > 180 {
			return nil, fmt.Errorf("invalid longitude: %f", lon)
		}

		if cfg.Zoom < 1 || cfg.Zoom > 21 {
			return nil, fmt.Errorf("invalid zoom level: %d", cfg.Zoom)
		}

		if cfg.Radius < 0 {
			return nil, fmt.Errorf("invalid radius: %f", cfg.Radius)
		}
	}

	// Set max results limit on the exit monitor if provided
	if cfg.ExitMonitor != nil && cfg.MaxResults > 0 {
		cfg.ExitMonitor.SetMaxResults(cfg.MaxResults)
	}

	scanner := bufio.NewScanner(cfg.Input)

	for scanner.Scan() {
		query := strings.TrimSpace(scanner.Text())
		if query == "" {
			continue
		}

		var id string

		if before, after, ok := strings.Cut(query, "#!#"); ok {
			query = strings.TrimSpace(before)
			id = strings.TrimSpace(after)
		}

		var job scrapemate.IJob

		if !cfg.FastMode {
			opts := []gmaps.GmapJobOptions{}

			if cfg.Dedup != nil {
				opts = append(opts, gmaps.WithDeduper(cfg.Dedup))
			}

			if cfg.ExitMonitor != nil {
				opts = append(opts, gmaps.WithExitMonitor(cfg.ExitMonitor))
			}

			if cfg.ExtraReviews {
				opts = append(opts, gmaps.WithExtraReviews())
			}

			if cfg.Debug {
				opts = append(opts, gmaps.WithDebug())
			}

			job = gmaps.NewGmapJob(id, cfg.LangCode, query, cfg.MaxDepth, cfg.Email, cfg.Images, cfg.ReviewsMax, cfg.GeoCoordinates, cfg.Zoom, opts...)
		} else {
			jparams := gmaps.MapSearchParams{
				Location: gmaps.MapLocation{
					Lat:     lat,
					Lon:     lon,
					ZoomLvl: float64(cfg.Zoom),
					Radius:  cfg.Radius,
				},
				Query:     query,
				ViewportW: 1920,
				ViewportH: 450,
				Hl:        cfg.LangCode,
			}

			opts := []gmaps.SearchJobOptions{}

			if cfg.ExitMonitor != nil {
				opts = append(opts, gmaps.WithSearchJobExitMonitor(cfg.ExitMonitor))
			}

			job = gmaps.NewSearchJob(&jparams, opts...)
		}

		jobs = append(jobs, job)
	}

	return jobs, scanner.Err()
}

func LoadCustomWriter(pluginDir, pluginName string) (scrapemate.ResultWriter, error) {
	files, err := os.ReadDir(pluginDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read plugin directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".so" && filepath.Ext(file.Name()) != ".dll" {
			continue
		}

		pluginPath := filepath.Join(pluginDir, file.Name())

		p, err := plugin.Open(pluginPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open plugin %s: %w", file.Name(), err)
		}

		symWriter, err := p.Lookup(pluginName)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup symbol %s: %w", pluginName, err)
		}

		writer, ok := symWriter.(*scrapemate.ResultWriter)
		if !ok {
			return nil, fmt.Errorf("unexpected type %T from writer symbol in plugin %s", symWriter, file.Name())
		}

		return *writer, nil
	}

	return nil, fmt.Errorf("no plugin found in %s", pluginDir)
}
