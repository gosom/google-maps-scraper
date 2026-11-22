package gmaps

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"net/http"
	"uuid"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
)

const searchBatchSize = 20

type SearchJobOptions func(*SearchJob)

type MapLocation struct {
	Lat     float64
	Lon     float64
	ZoomLvl float64
	Radius  float64
}

type MapSearchParams struct {
	Location  MapLocation
	Query     string
	ViewportW int
	ViewportH int
	Hl        string
}

type SearchJob struct {
	scrapemate.Job

	params                  *MapSearchParams
	maxDepth                int
	batch                   int
	seen                    map[string]struct{}
	ExitMonitor             exiter.Exiter
	WriterManagedCompletion bool
}

func NewSearchJob(params *MapSearchParams, opts ...SearchJobOptions) *SearchJob {
	const (
		defaultPrio       = scrapemate.PriorityMedium
		defaultMaxRetries = 3
		baseURL           = "https://maps.google.com/search"
	)

	job := SearchJob{
		Job: scrapemate.Job{
			ID:         uuid.NewV4().String(),
			Method:     http.MethodGet,
			URL:        baseURL,
			URLParams:  buildGoogleMapsParams(params, 0),
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.params = params
	job.maxDepth = 1

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithSearchJobExitMonitor(exitMonitor exiter.Exiter) SearchJobOptions {
	return func(j *SearchJob) {
		j.ExitMonitor = exitMonitor
	}
}

func WithSearchJobWriterManagedCompletion() SearchJobOptions {
	return func(j *SearchJob) {
		j.WriterManagedCompletion = true
	}
}

func (j *SearchJob) ProcessOnFetchError() bool {
	return true
}

// WithSearchJobDepth limits the number of HTTP result batches. Values below one use one batch.
func WithSearchJobDepth(depth int) SearchJobOptions {
	return func(j *SearchJob) { j.maxDepth = max(1, depth) }
}

func (j *SearchJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	continuing := false

	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil

		if !continuing && j.ExitMonitor != nil {
			j.ExitMonitor.IncrSeedCompleted(1)
		}
	}()

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	if resp.Error != nil {
		return nil, nil, resp.Error
	}

	if resp.StatusCode != 0 && resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("search returned HTTP %d", resp.StatusCode)
	}

	body := removeFirstLine(resp.Body)
	if len(body) == 0 {
		return nil, nil, fmt.Errorf("empty response body")
	}

	entries, err := ParseSearchResults(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse search results: %w", err)
	}

	rawCount := len(entries)
	entries, seen := j.uniqueEntries(entries)
	newCount := len(entries)
	entries = filterAndSortEntriesWithinRadius(entries,
		j.params.Location.Lat, j.params.Location.Lon, j.params.Location.Radius)

	var next []scrapemate.IJob
	if rawCount >= searchBatchSize && newCount > 0 && j.batch+1 < j.maxDepth {
		next = []scrapemate.IJob{j.nextBatch(seen)}
		continuing = true
	}

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrPlacesFound(len(entries))

		if !j.WriterManagedCompletion {
			j.ExitMonitor.IncrPlacesCompleted(len(entries))
		}
	}

	return entries, next, nil
}

func (j *SearchJob) uniqueEntries(entries []*Entry) (unique []*Entry, seen map[string]struct{}) {
	// Each continuation owns a snapshot, so processing a response never mutates its parent.
	seen = maps.Clone(j.seen)
	if seen == nil {
		seen = make(map[string]struct{}, len(entries))
	}

	unique = make([]*Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Title == "" {
			continue
		}

		key := searchEntryIdentity(entry)
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}
		entry.ID = j.inputID()
		unique = append(unique, entry)
	}

	return unique, seen
}

func searchEntryIdentity(entry *Entry) string {
	switch {
	case entry.PlaceID != "":
		return "place:" + entry.PlaceID
	case entry.DataID != "":
		return "data:" + entry.DataID
	default:
		return fmt.Sprintf("business:%q:%q:%.7f:%.7f", entry.Title, entry.Address, entry.Latitude, entry.Longtitude)
	}
}

func (j *SearchJob) inputID() string {
	if j.ParentID != "" {
		return j.ParentID
	}

	return j.ID
}

func (j *SearchJob) nextBatch(seen map[string]struct{}) *SearchJob {
	next := *j
	next.ID = uuid.NewV4().String()
	next.ParentID = j.inputID()
	next.batch++
	next.seen = seen
	next.URLParams = buildGoogleMapsParams(j.params, next.batch*searchBatchSize)

	return &next
}

func removeFirstLine(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	index := bytes.IndexByte(data, '\n')
	if index == -1 {
		return []byte{}
	}

	return data[index+1:]
}

func buildGoogleMapsParams(params *MapSearchParams, offset int) map[string]string {
	const (
		viewportHeight = 800
		viewportWidth  = 600
	)

	ans := map[string]string{
		"tbm":              "map",
		"authuser":         "0",
		languageQueryParam: params.Hl,
		"q":                params.Query,
	}

	pb := fmt.Sprintf("!4m12!1m3!1d3826.902183192154!2d%.4f!3d%.4f!2m3!1f0!2f0!3f0!3m2!1i%d!2i%d!4f%.1f!7i20!8i%d"+
		"!10b1!12m22!1m3!18b1!30b1!34e1!2m3!5m1!6e2!20e3!4b0!10b1!12b1!13b1!16b1!17m1!3e1!20m3!5e2!6b1!14b1!46m1!1b0"+
		"!96b1!19m4!2m3!1i360!2i120!4i8",
		params.Location.Lon,
		params.Location.Lat,
		viewportWidth,
		viewportHeight,
		params.Location.ZoomLvl,
		offset,
	)

	ans["pb"] = pb

	return ans
}
