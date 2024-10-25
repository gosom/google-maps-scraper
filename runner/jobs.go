package runner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"plugin"
	"strings"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

func CreateSeedJobs(
	langCode string,
	r io.Reader,
	maxDepth int,
	email bool,
	geoCoordinates string,
	zoom int,
	dedup deduper.Deduper,
	exitMonitor exiter.Exiter,
) (jobs []scrapemate.IJob, err error) {
	scanner := bufio.NewScanner(r)

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

		opts := []gmaps.GmapJobOptions{}

		if dedup != nil {
			opts = append(opts, gmaps.WithDeduper(dedup))
		}

		if exitMonitor != nil {
			opts = append(opts, gmaps.WithExitMonitor(exitMonitor))
		}

		job := gmaps.NewGmapJob(id, langCode, query, maxDepth, email, geoCoordinates, zoom, opts...)

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
