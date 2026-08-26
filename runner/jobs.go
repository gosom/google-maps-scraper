package runner

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"plugin"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/grid"
	"github.com/gosom/scrapemate"
)

type seedJobConfig struct {
	completedInputSkipper func(string) bool
	completionTracker     gmaps.CompletionTracker
	deterministicIDs      bool
}

// SeedJobOption configures seed job creation.
type SeedJobOption func(*seedJobConfig)

// WithCompletedInputSkipper skips seed jobs whose input IDs are complete.
func WithCompletedInputSkipper(skipper func(string) bool) SeedJobOption {
	return func(cfg *seedJobConfig) {
		cfg.completedInputSkipper = skipper
	}
}

// WithCompletionTracker attaches a per-input completion tracker to created jobs.
func WithCompletionTracker(tracker gmaps.CompletionTracker) SeedJobOption {
	return func(cfg *seedJobConfig) {
		cfg.completionTracker = tracker
	}
}

// WithDeterministicSeedIDs derives stable IDs for input lines without custom IDs.
func WithDeterministicSeedIDs() SeedJobOption {
	return func(cfg *seedJobConfig) {
		cfg.deterministicIDs = true
	}
}

func CreateSeedJobs(
	fastmode bool,
	langCode string,
	r io.Reader,
	maxDepth int,
	email bool,
	geoCoordinates string,
	zoom int,
	radius float64,
	dedup deduper.Deduper,
	exitMonitor exiter.Exiter,
	extraReviews bool,
	opts ...SeedJobOption,
) (jobs []scrapemate.IJob, err error) {
	createCfg := newSeedJobConfig(opts...)

	var lat, lon float64

	if fastmode {
		if geoCoordinates == "" {
			return nil, fmt.Errorf("geo coordinates are required in fast mode")
		}

		parts := strings.Split(geoCoordinates, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid geo coordinates: %s", geoCoordinates)
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

		if zoom < 1 || zoom > 21 {
			return nil, fmt.Errorf("invalid zoom level: %d", zoom)
		}

		if radius < 0 {
			return nil, fmt.Errorf("invalid radius: %f", radius)
		}
	}

	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		q, ok, parseErr := parseQueryLine(scanner.Text())
		if parseErr != nil {
			return nil, parseErr
		}

		if !ok {
			continue
		}

		query := q.text
		id := q.id

		if createCfg.deterministicIDs && id == "" {
			id = deterministicSeedID(query)
		}

		if createCfg.completedInputSkipper != nil && createCfg.completedInputSkipper(id) {
			continue
		}

		var job scrapemate.IJob

		if !fastmode {
			opts := []gmaps.GmapJobOptions{}

			if dedup != nil {
				opts = append(opts, gmaps.WithDeduper(dedup))
			}

			if exitMonitor != nil {
				opts = append(opts, gmaps.WithExitMonitor(exitMonitor))
			}

			if extraReviews {
				opts = append(opts, gmaps.WithExtraReviews())
			}

			if createCfg.completionTracker != nil {
				opts = append(opts, gmaps.WithGmapCompletionTracker(createCfg.completionTracker))
			}

			job = gmaps.NewGmapJob(id, langCode, query, maxDepth, email, geoCoordinates, zoom, opts...)
		} else {
			jparams := gmaps.MapSearchParams{
				Location: gmaps.MapLocation{
					Lat:     lat,
					Lon:     lon,
					ZoomLvl: float64(zoom),
					Radius:  radius,
				},
				Query:     query,
				ViewportW: 1920,
				ViewportH: 450,
				Hl:        langCode,
			}

			opts := []gmaps.SearchJobOptions{}

			if exitMonitor != nil {
				opts = append(opts, gmaps.WithSearchJobExitMonitor(exitMonitor))
			}

			searchJob := gmaps.NewSearchJob(&jparams, opts...)

			if id != "" {
				searchJob.ID = id
			}

			job = searchJob
		}

		jobs = append(jobs, job)
	}

	return jobs, scanner.Err()
}

// CreateGridSeedJobs reads search queries from r and produces one GmapJob per
// (query, grid-cell) pair. Each cell covers approximately cellSizeKm × cellSizeKm
// on the ground. The zoom level controls how much of the map Google Maps renders
// per cell (use 14-16 for most cases).
//
// Deduplication across cells is handled automatically by the shared deduper.
func CreateGridSeedJobs(
	langCode string,
	r io.Reader,
	maxDepth int,
	email bool,
	bbox grid.BoundingBox,
	cellSizeKm float64,
	zoom int,
	dedup deduper.Deduper,
	exitMonitor exiter.Exiter,
	extraReviews bool,
	opts ...SeedJobOption,
) ([]scrapemate.IJob, error) {
	createCfg := newSeedJobConfig(opts...)

	if zoom < 1 || zoom > 21 {
		return nil, fmt.Errorf("invalid zoom level: %d", zoom)
	}

	cells := grid.GenerateCells(bbox, cellSizeKm)
	if len(cells) == 0 {
		return nil, fmt.Errorf("grid produced 0 cells — check bounding box and cell size")
	}

	queries, err := readQueries(r)
	if err != nil {
		return nil, err
	}

	if len(queries) == 0 {
		return nil, fmt.Errorf("no queries found in input")
	}

	var jobs []scrapemate.IJob

	for _, q := range queries {
		queryText := q.text
		queryID := q.id

		for _, cell := range cells {
			// Each cell gets a unique ID derived from the query ID (or a new UUID).
			cellID := uuid.New().String()

			switch {
			case createCfg.deterministicIDs && queryID != "":
				cellID = deterministicSeedID(queryID, cell.GeoCoordinates())
			case createCfg.deterministicIDs:
				cellID = deterministicSeedID(queryText, cell.GeoCoordinates())
			case queryID != "":
				cellID = fmt.Sprintf("%s-%s", queryID, cellID)
			}

			if createCfg.completedInputSkipper != nil && createCfg.completedInputSkipper(cellID) {
				continue
			}

			opts := []gmaps.GmapJobOptions{}

			if dedup != nil {
				opts = append(opts, gmaps.WithDeduper(dedup))
			}

			if exitMonitor != nil {
				opts = append(opts, gmaps.WithExitMonitor(exitMonitor))
			}

			if extraReviews {
				opts = append(opts, gmaps.WithExtraReviews())
			}

			if createCfg.completionTracker != nil {
				opts = append(opts, gmaps.WithGmapCompletionTracker(createCfg.completionTracker))
			}

			job := gmaps.NewGmapJob(
				cellID,
				langCode,
				queryText,
				maxDepth,
				email,
				cell.GeoCoordinates(),
				zoom,
				opts...,
			)

			jobs = append(jobs, job)
		}
	}

	return jobs, nil
}

func newSeedJobConfig(opts ...SeedJobOption) seedJobConfig {
	var cfg seedJobConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

func deterministicSeedID(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}

	return fmt.Sprintf("resume:%x", hash.Sum(nil))
}

// query holds a parsed input line.
type query struct {
	text string
	id   string
}

// readQueries reads all non-empty lines from r and parses optional custom IDs
// using the "#!#" delimiter (same format as CreateSeedJobs).
func readQueries(r io.Reader) ([]query, error) {
	var queries []query

	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		q, ok, parseErr := parseQueryLine(scanner.Text())
		if parseErr != nil {
			return nil, parseErr
		}

		if !ok {
			continue
		}

		queries = append(queries, q)
	}

	return queries, scanner.Err()
}

func parseQueryLine(line string) (query, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return query{}, false, nil
	}

	var q query

	if before, after, ok := strings.Cut(line, "#!#"); ok {
		q.text = strings.TrimSpace(before)
		q.id = strings.TrimSpace(after)
	} else {
		q.text = line
	}

	if q.text == "" {
		return query{}, false, fmt.Errorf("invalid query line %q: empty query text", line)
	}

	return q, true, nil
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
