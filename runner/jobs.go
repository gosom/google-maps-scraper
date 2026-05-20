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
	FastMode      bool
	LangCode      string
	Input         io.Reader
	MaxDepth      int
	IncludeEmails bool
	// Images enables image extraction at all. When false (or when
	// ImagesPerPlace is 0), no images are scraped and the JSON-payload
	// images are dropped in PlaceJob.Process so the user is not billed
	// for images they didn't ask for.
	Images bool
	// ImagesPerPlace is the hard per-place image cap. 0 = skip images;
	// positive = take at most N images per place (cheap JSON first,
	// browser extractor only when JSON has fewer than N). Replaces the
	// legacy per-job-total ImageBudget *atomic.Int64 — see
	// gmaps.PlaceJob.applyPerPlaceImageCap for the contract (May 2026,
	// Cafe Schöneberg fix).
	ImagesPerPlace int
	Debug          bool
	ReviewsMax     int
	GeoCoordinates string
	Zoom           int
	Radius         float64
	Dedup          deduper.Deduper
	ExitMonitor    exiter.Exiter
	ExtraReviews   bool
	MaxResults     int
	// UserID and UserJobID are propagated to every seed job so that log lines
	// emitted deep in gmaps code paths carry the user-facing identifiers even
	// though scrapemate replaces the ctx-bound logger per job.
	UserID    string
	UserJobID string
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

			// Per-place image cap propagated to every PlaceJob spawned by
			// this seed — see gmaps.PlaceJob.applyPerPlaceImageCap.
			if cfg.ImagesPerPlace > 0 {
				opts = append(opts, gmaps.WithImagesPerPlace(cfg.ImagesPerPlace))
			}

			// Propagate user context so gmaps log lines carry user_id and the
			// user-facing job_id even though scrapemate replaces the ctx logger.
			if cfg.UserID != "" || cfg.UserJobID != "" {
				opts = append(opts, gmaps.WithUserContext(cfg.UserID, cfg.UserJobID))
			}

			job = gmaps.NewGmapJob(id, cfg.LangCode, query, cfg.MaxDepth, cfg.IncludeEmails, cfg.Images, cfg.ReviewsMax, cfg.GeoCoordinates, cfg.Zoom, opts...)
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

			if cfg.UserID != "" || cfg.UserJobID != "" {
				opts = append(opts, gmaps.WithSearchJobUserContext(cfg.UserID, cfg.UserJobID))
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
