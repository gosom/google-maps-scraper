package gmaps

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"uuid"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
	"github.com/mcnijman/go-emailaddress"

	"github.com/gosom/google-maps-scraper/exiter"
)

type EmailExtractJobOptions func(*EmailExtractJob)

type EmailExtractJob struct {
	scrapemate.Job

	Entry                   *Entry
	ExitMonitor             exiter.Exiter
	WriterManagedCompletion bool
}

func NewEmailJob(parentID string, entry *Entry, opts ...EmailExtractJobOptions) *EmailExtractJob {
	const (
		defaultPrio       = scrapemate.PriorityHigh
		defaultMaxRetries = 0
	)

	job := EmailExtractJob{
		Job: scrapemate.Job{
			ID:         uuid.NewV4().String(),
			ParentID:   parentID,
			Method:     requestMethodGet,
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

	// if html fetch failed just return
	if resp.Error != nil {
		return j.Entry, nil, nil
	}

	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		return j.Entry, nil, nil
	}

	emails := docEmailExtractor(doc)
	if len(emails) == 0 {
		emails = regexEmailExtractor(resp.Body)
	}

	j.Entry.Emails = emails

	return j.Entry, nil, nil
}

func (j *EmailExtractJob) ProcessOnFetchError() bool {
	return true
}

func docEmailExtractor(doc *goquery.Document) []string {
	seen := map[string]bool{}

	var emails []string

	doc.Find("a[href^='mailto:']").Each(func(_ int, s *goquery.Selection) {
		mailto, exists := s.Attr("href")
		if exists {
			value := strings.TrimPrefix(mailto, "mailto:")
			value = strings.Split(value, "?")[0] // strip mailto query parameters
			if email, ok := cleanEmail(value); ok {
				if !seen[email] {
					emails = append(emails, email)
					seen[email] = true
				}
			}
		}
	})

	return emails
}

func regexEmailExtractor(body []byte) []string {
	seen := map[string]bool{}

	var emails []string

	addresses := emailaddress.Find(body, false)
	for i := range addresses {
		email, ok := cleanEmail(addresses[i].String())
		if !ok {
			continue
		}
		if !seen[email] {
			emails = append(emails, email)
			seen[email] = true
		}
	}

	return emails
}

var errInvalidEmail = errors.New("invalid email")

func getValidEmail(s string) (string, error) {
	email, ok := cleanEmail(s)
	if !ok {
		return "", errInvalidEmail
	}

	return email, nil
}

// cleanEmail normalizes raw email candidates and reports whether the address is valid and usable.
func cleanEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	email = strings.Trim(email, `"'<>`)
	email = strings.TrimSpace(email)

	// Control characters indicate the match was extracted from binary or corrupted markup.
	if strings.ContainsFunc(email, unicode.IsControl) {
		return "", false
	}

	if len(email) < 5 || len(email) > 254 || !strings.Contains(email, "@") {
		return "", false
	}

	parsed, err := emailaddress.Parse(email)
	if err != nil {
		return "", false
	}
	email = parsed.String()

	if !isUsableEmail(email) {
		return "", false
	}

	return email, true
}

// isUsableEmail rejects asset/media filenames and telemetry/tracker domains.
func isUsableEmail(email string) bool {
	for _, ext := range invalidEmailExtensions {
		if strings.HasSuffix(email, ext) {
			return false
		}
	}

	emailLower := strings.ToLower(email)
	for _, suffix := range junkEmailDomainSuffixes {
		if strings.HasSuffix(emailLower, suffix) {
			return false
		}
	}

	if at := strings.LastIndex(emailLower, "@"); at >= 0 {
		domain := emailLower[at+1:]
		for _, label := range junkEmailDomainLabels {
			if strings.HasPrefix(domain, label) {
				return false
			}
		}
	}

	return true
}

// invalidEmailExtensions covers retina srcset filenames ("name@2x.ext") that
// resemble email addresses and pass syntax validation.
var invalidEmailExtensions = []string{
	".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".avif",
	".jfif", ".bmp", ".ico", ".tif", ".tiff", ".heic", ".heif",
	".css", ".js", ".mjs", ".json", ".xml", ".html", ".htm",
	".pdf", ".doc", ".docx", ".zip", ".rar",
	".mp4", ".webm", ".mp3", ".woff", ".woff2", ".ttf", ".eot",
}

var junkEmailDomainSuffixes = []string{
	"@sentry.io", ".ingest.sentry.io", ".ingest.us.sentry.io", ".ingest.de.sentry.io",
	"@exceptions.doctolib.fr", ".vk-portal.net",
	".elementor.cloud", ".wixsite.com", "wixpress.com",
}

var junkEmailDomainLabels = []string{
	"sentry.", "sentry-next.", "exceptions.", "errors.", "telemetry.",
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
