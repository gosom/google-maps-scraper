package gmaps

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/gosom/scrapemate"

	"github.com/AxioIntel/Axio-CRED/exiter"
)

type EmailExtractJobOptions func(*EmailExtractJob)

type EmailExtractJob struct {
	scrapemate.Job

	Entry                   *Entry
	ExitMonitor             exiter.Exiter
	WriterManagedCompletion bool

	// fetchPage reads the business's contact-like pages; nil is fetchSitePage. Set by tests.
	fetchPage pageFetcher
}

func NewEmailJob(parentID string, entry *Entry, opts ...EmailExtractJobOptions) *EmailExtractJob {
	const (
		defaultPrio       = scrapemate.PriorityHigh
		defaultMaxRetries = 0
	)

	job := EmailExtractJob{
		Job: scrapemate.Job{
			ID:         uuid.New().String(),
			ParentID:   parentID,
			Method:     "GET",
			URL:        normalizeGoogleURL(entry.WebSite),
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
	}

	job.Entry = entry

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithEmailJobExitMonitor(exitMonitor exiter.Exiter) EmailExtractJobOptions {
	return func(j *EmailExtractJob) {
		j.ExitMonitor = exitMonitor
	}
}

func WithEmailJobWriterManagedCompletion() EmailExtractJobOptions {
	return func(j *EmailExtractJob) {
		j.WriterManagedCompletion = true
	}
}

func (j *EmailExtractJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	defer func() {
		if j.ExitMonitor != nil && !j.WriterManagedCompletion {
			j.ExitMonitor.IncrPlacesCompleted(1)
		}
	}()

	log := scrapemate.GetLoggerFromContext(ctx)

	log.Info("Processing email job", "url", j.URL)

	// if html fetch failed just return, saying why
	if resp.Error != nil {
		j.Entry.EmailStatus = EmailFetchFailed + fetchFailReason(resp.Error)

		return j.Entry, nil, nil
	}

	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		j.Entry.EmailStatus = EmailFetchFailed + "not_html"

		return j.Entry, nil, nil
	}

	fetch := j.fetchPage
	if fetch == nil {
		fetch = fetchSitePage
	}

	emails := findBusinessEmails(ctx, j.URL, doc, resp.Body, fetch)

	j.Entry.Emails = emails
	j.Entry.EmailStatus = emailOutcome(emails)

	return j.Entry, nil, nil
}

func (j *EmailExtractJob) ProcessOnFetchError() bool {
	return true
}

// BrowserActions fetches the business's front page with a plain, direct request instead of
// through the browser. The browser carries the run's proxies, and they are only needed for
// Google: a business's own website does not block us, and proxies billed by traffic would pay
// for every page of it. This also saves starting a page render for each site.
//
// A site that writes its address only with JavaScript is not seen this way; the contact pages
// were already read the same direct way, and mailto links and Cloudflare-protected addresses
// are in the served HTML.
func (j *EmailExtractJob) BrowserActions(ctx context.Context, _ scrapemate.BrowserPage) scrapemate.Response {
	fetch := j.fetchPage
	if fetch == nil {
		fetch = fetchSitePage
	}

	started := time.Now()

	body, err := fetch(ctx, j.URL)
	if err != nil {
		return scrapemate.Response{URL: j.URL, Error: err}
	}

	return scrapemate.Response{URL: j.URL, StatusCode: http.StatusOK, Body: body, Duration: time.Since(started)}
}

// normalizeGoogleURL extracts the actual target URL from Google redirect URLs.
// Google Maps sometimes returns URLs like "/url?q=http://example.com/&opi=..."
// for external website links.
func normalizeGoogleURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	if strings.HasPrefix(rawURL, "/url?q=") {
		fullURL := "https://www.google.com" + rawURL

		parsed, err := url.Parse(fullURL)
		if err != nil {
			return rawURL
		}

		if target := parsed.Query().Get("q"); target != "" {
			return target
		}
	}

	if strings.HasPrefix(rawURL, "/") {
		return "https://www.google.com" + rawURL
	}

	return rawURL
}
