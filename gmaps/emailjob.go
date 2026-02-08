package gmaps

import (
	"context"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
	"github.com/mcnijman/go-emailaddress"
)

type EmailExtractJobOptions func(*EmailExtractJob)

type EmailFilter func(string) bool

func DefaultEmailFilter(email string) bool {
	lower := strings.ToLower(email)

	// Filter images
	imageExtensions := []string{".png", ".webp", ".jpg", ".jpeg", ".gif"}
	for _, ext := range imageExtensions {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}
	if strings.Contains(lower, "@2x.webp") || strings.Contains(lower, "@3x.webp") {
		return false
	}

	// Filter sandbox/test emails
	if strings.Contains(lower, "sentry-next.wixpress.com") {
		return false
	}

	// Filter local/test domains
	parts := strings.Split(lower, "@")
	if len(parts) == 2 {
		domain := parts[1]
		invalidSuffixes := []string{".local", ".test", ".example"}
		for _, suffix := range invalidSuffixes {
			if strings.HasSuffix(domain, suffix) {
				return false
			}
		}
	}

	return true
}


type EmailExtractJob struct {
	scrapemate.Job

	Entry       *Entry
	ExitMonitor exiter.Exiter
	EmailFilter EmailFilter
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
			URL:        entry.WebSite,
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

func WithEmailFilter(filter EmailFilter) EmailExtractJobOptions {
	return func(j *EmailExtractJob) {
		j.EmailFilter = filter
	}
}

func WithEmailJobExitMonitor(exitMonitor exiter.Exiter) EmailExtractJobOptions {
	return func(j *EmailExtractJob) {
		j.ExitMonitor = exitMonitor
	}
}

func (j *EmailExtractJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	defer func() {
		if j.ExitMonitor != nil {
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

	emails = docEmailExtractor(doc)
	if len(emails) == 0 {
		emails = regexEmailExtractor(resp.Body)
	}

	if j.EmailFilter != nil {
		var filtered []string
		for _, email := range emails {
			if j.EmailFilter(email) {
				filtered = append(filtered, email)
			}
		}
		emails = filtered
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
			if email, err := getValidEmail(value); err == nil {
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
		if !seen[addresses[i].String()] {
			emails = append(emails, addresses[i].String())
			seen[addresses[i].String()] = true
		}
	}

	return emails
}

func getValidEmail(s string) (string, error) {
	email, err := emailaddress.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", err
	}

	return email.String(), nil
}
