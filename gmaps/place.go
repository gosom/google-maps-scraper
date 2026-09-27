package gmaps

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/scrapemate"

	"github.com/AxioIntel/Axio-CRED/exiter"
)

type PlaceJobOptions func(*PlaceJob)

type PlaceJob struct {
	scrapemate.Job

	UsageInResults          bool
	ExtractEmail            bool
	ExitMonitor             exiter.Exiter
	ExtractExtraReviews     bool
	WriterManagedCompletion bool
	// ReviewConfig is how the extended review collection runs; the zero value is the defaults.
	ReviewConfig ReviewConfig
}

func NewPlaceJob(parentID, langCode, u string, extractEmail, extraExtraReviews bool, opts ...PlaceJobOptions) *PlaceJob {
	const (
		defaultPrio       = scrapemate.PriorityMedium
		defaultMaxRetries = 3
	)

	job := PlaceJob{
		Job: scrapemate.Job{
			ID:         uuid.New().String(),
			ParentID:   parentID,
			Method:     "GET",
			URL:        u,
			URLParams:  map[string]string{"hl": langCode},
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.UsageInResults = true
	job.ExtractEmail = extractEmail
	job.ExtractExtraReviews = extraExtraReviews

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithPlaceJobExitMonitor(exitMonitor exiter.Exiter) PlaceJobOptions {
	return func(j *PlaceJob) {
		j.ExitMonitor = exitMonitor
	}
}

func WithPlaceJobReviewConfig(cfg ReviewConfig) PlaceJobOptions {
	return func(j *PlaceJob) {
		j.ReviewConfig = cfg
	}
}

func WithPlaceJobWriterManagedCompletion() PlaceJobOptions {
	return func(j *PlaceJob) {
		j.WriterManagedCompletion = true
	}
}

func (j *PlaceJob) ProcessOnFetchError() bool {
	return true
}

func (j *PlaceJob) Process(_ context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil
	}()

	if resp.Error != nil {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}

		return nil, nil, resp.Error
	}

	raw, ok := resp.Meta["json"].([]byte)
	if !ok {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}

		return nil, nil, fmt.Errorf("could not convert to []byte")
	}

	entry, err := EntryFromJSON(raw)
	if err != nil {
		if j.ExitMonitor != nil {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}

		return nil, nil, err
	}

	entry.ID = j.ParentID

	if entry.Link == "" {
		entry.Link = j.GetURL()
	}

	// The extended review collection, merged against the inline reviews once (`reviewSet`: a
	// review both carry is enriched in place, never stored twice), and its report settled against
	// that union.
	if result, ok := resp.Meta["review_result"].(*reviewResult); ok {
		if j.ReviewConfig.NewestOnly {
			// The page's inline reviews are its "most relevant" few, not the newest: a newest-only
			// collection is exactly the Newest-sorted list, so its receiver can tell from it
			// whether anything newer than what it already holds was missed.
			entry.UserReviews = make([]Review, 0)
		}

		set := newReviewSet(entry.UserReviews, j.ReviewConfig.withDefaults().MaxReviews)
		for i := range result.Rows {
			set.add(&result.Rows[i])
		}

		entry.SetExtendedReviews(set.extended())

		report := result.Report
		report.WithoutID = set.withoutID()
		report.finalize(set.distinct())
		entry.ReviewCollection = &report

		log.Printf("reviews: %d of %d collected (%s%s), stages %v, %d rpc pages, %d blocks, %.0fs",
			report.Collected, report.Reported, report.StopReason, stageSuffix(report.StopStage),
			report.Stages, report.RPCPages, report.Blocks, report.ElapsedSeconds)
	}

	if j.ExtractEmail && entry.IsWebsiteValidForEmail() {
		opts := []EmailExtractJobOptions{}
		if j.ExitMonitor != nil {
			opts = append(opts, WithEmailJobExitMonitor(j.ExitMonitor))
		}

		if j.WriterManagedCompletion {
			opts = append(opts, WithEmailJobWriterManagedCompletion())
		}

		emailJob := NewEmailJob(j.ID, &entry, opts...)

		j.UsageInResults = false

		return nil, []scrapemate.IJob{emailJob}, nil
	}

	if j.ExtractEmail {
		entry.EmailStatus = entry.noEmailSearchReason()
	}

	if j.ExitMonitor != nil && !j.WriterManagedCompletion {
		j.ExitMonitor.IncrPlacesCompleted(1)
	}

	return &entry, nil, err
}

func (j *PlaceJob) BrowserActions(ctx context.Context, page scrapemate.BrowserPage) scrapemate.Response {
	var resp scrapemate.Response

	if err := Blocks.Wait(ctx); err != nil {
		resp.Error = err

		return resp
	}

	pageResponse, err := page.Goto(j.GetURL(), scrapemate.WaitUntilDOMContentLoaded)
	if err != nil {
		resp.Error = err

		return resp
	}

	if why := placeBlocked(pageResponse.StatusCode, page.URL()); why != "" {
		Blocks.Refused()

		resp.Error = fmt.Errorf("%w: %w: %s", ErrGoogleBlocked, errPlaceBlocked, why)

		return resp
	}

	clickRejectCookiesIfRequired(page)

	const defaultTimeout = 5 * time.Second

	// Ignore WaitForURL errors — Google Maps may redirect slowly especially via proxy
	_ = page.WaitForURL(page.URL(), defaultTimeout)

	resp.URL = pageResponse.URL
	resp.StatusCode = pageResponse.StatusCode
	resp.Headers = pageResponse.Headers

	raw, err := j.extractJSON(page)
	if err != nil {
		// No place data: a refusal page served with a 200 looks exactly like this.
		if html, cerr := page.Content(); cerr == nil && refusedByContent(html) {
			Blocks.Refused()

			err = ErrGoogleBlocked
		}

		resp.Error = err

		return resp
	}

	Blocks.Loaded()

	if resp.Meta == nil {
		resp.Meta = make(map[string]any)
	}

	resp.Meta["json"] = raw

	if j.ExtractExtraReviews {
		reported := j.getReviewCount(raw)
		if reported > 0 || placeShowsReviews(raw) {
			resp.Meta["review_result"] = newReviewCollector(j.ReviewConfig, page, page.URL()).run(ctx, reported)
		} else {
			resp.Meta["review_result"] = &reviewResult{
				Report: ReviewCollection{StopReason: stopNoReviews, Stages: []string{}},
			}
		}
	}

	return resp
}

func (j *PlaceJob) getRaw(ctx context.Context, page scrapemate.BrowserPage) (any, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout while getting raw data: %w", ctx.Err())
		default:
			raw, err := page.Eval(js)
			if err != nil {
				// Continue retrying on error
				<-time.After(time.Millisecond * 200)
				continue
			}

			// Check for valid non-null result.
			// JS null may arrive as nil, and empty strings are not useful here.
			if raw == nil {
				<-time.After(time.Millisecond * 200)
				continue
			}

			// If it's a string, make sure it's not empty
			if str, ok := raw.(string); ok {
				if str == "" {
					<-time.After(time.Millisecond * 200)
					continue
				}
			}

			return raw, nil
		}
	}
}

func (j *PlaceJob) extractJSON(page scrapemate.BrowserPage) ([]byte, error) {
	const maxRetries = 2

	for attempt := range maxRetries {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rawI, err := j.getRaw(ctx, page)

		cancel()

		if err != nil {
			// On timeout, try reloading the page
			if attempt < maxRetries-1 {
				if reloadErr := page.Reload(scrapemate.WaitUntilDOMContentLoaded); reloadErr == nil {
					continue
				}
			}

			return nil, err
		}

		if rawI == nil {
			if attempt < maxRetries-1 {
				if reloadErr := page.Reload(scrapemate.WaitUntilDOMContentLoaded); reloadErr == nil {
					continue
				}
			}

			return nil, fmt.Errorf("APP_INITIALIZATION_STATE data not found")
		}

		raw, ok := rawI.(string)
		if !ok {
			return nil, fmt.Errorf("could not convert to string, got type %T", rawI)
		}

		const prefix = `)]}'`

		raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))

		return []byte(raw), nil
	}

	return nil, fmt.Errorf("APP_INITIALIZATION_STATE data not found after retries")
}

func (j *PlaceJob) getReviewCount(data []byte) int {
	tmpEntry, err := EntryFromJSON(data, true)
	if err != nil {
		return 0
	}

	return tmpEntry.ReviewCount
}

// errPlaceBlocked: Google refused the place page itself -- no review stage can start.
var errPlaceBlocked = errors.New("place page blocked")

// placeBlocked says why a loaded place page is a refusal, or "" when it is not.
func placeBlocked(status int, finalURL string) string {
	switch {
	case strings.Contains(finalURL, "/sorry/"):
		return "redirected to Google's /sorry/ page"
	case status == 403 || status == 429:
		return fmt.Sprintf("HTTP %d", status)
	}

	return ""
}

// placeShowsReviews is whether a listing whose review count could not be read still shows
// reviews -- inline ones, or a per-star breakdown. Such a place is collected and reported
// `count_unknown`, never skipped as having none.
func placeShowsReviews(raw []byte) bool {
	entry, err := EntryFromJSON(raw)
	if err != nil {
		return false
	}

	if len(entry.UserReviews) > 0 {
		return true
	}

	for _, n := range entry.ReviewsPerRating {
		if n > 0 {
			return true
		}
	}

	return false
}

func stageSuffix(stage string) string {
	if stage == "" {
		return ""
	}

	return " in " + stage
}

func (j *PlaceJob) UseInResults() bool {
	return j.UsageInResults
}

const js = `
(function() {
	if (!window.APP_INITIALIZATION_STATE || !window.APP_INITIALIZATION_STATE[3]) {
		return null;
	}
	const appState = window.APP_INITIALIZATION_STATE[3];
	
	// Search all properties of appState for arrays containing JSON strings
	for (const key of Object.keys(appState)) {
		const arr = appState[key];
		if (Array.isArray(arr)) {
			// Check indices 6 and 5 (where place data typically is)
			for (const idx of [6, 5]) {
				const item = arr[idx];
				if (typeof item === 'string' && item.startsWith(")]}'")) {
					return item;
				}
			}
		}
	}
	return null;
})()
`
