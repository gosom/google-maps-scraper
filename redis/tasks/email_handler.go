package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/scrapemateapp"
	"github.com/hibiken/asynq"
	"github.com/mcnijman/go-emailaddress"
)

// EmailJob represents a scrapemate job for email extraction
type EmailJob struct {
	scrapemate.Job
	maxDepth  int
	userAgent string
}

// NewEmailJob creates a new email extraction job
func NewEmailJob(url string, maxDepth int, userAgent string) scrapemate.IJob {
	return &EmailJob{
		Job: scrapemate.Job{
			Method: "GET",
			URL:    url,
		},
		maxDepth:  maxDepth,
		userAgent: userAgent,
	}
}

// Process implements the scrapemate.IJob interface
func (j *EmailJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	// Get logger from context
	log := scrapemate.GetLoggerFromContext(ctx)
	log.Info("Processing email job", "url", j.URL)

	// if html fetch failed just return
	if resp.Error != nil {
		return nil, nil, nil
	}

	// Extract emails using both methods
	var emails []string
	
	// Try extracting from mailto links first
	if doc, ok := resp.Document.(*goquery.Document); ok {
		emails = docEmailExtractor(doc)
	}

	// If no emails found, try regex extraction
	if len(emails) == 0 {
		emails = regexEmailExtractor(resp.Body)
	}

	// Prepare result
	result := map[string]interface{}{
		"url":    j.URL,
		"emails": emails,
	}

	// If we haven't reached max depth, extract links and create new jobs
	if j.maxDepth > 0 && resp.Document != nil {
		var newJobs []scrapemate.IJob
		if doc, ok := resp.Document.(*goquery.Document); ok {
			baseURL, err := url.Parse(j.URL)
			if err != nil {
				return result, nil, fmt.Errorf("failed to parse base URL: %w", err)
			}

			doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
				if href, exists := s.Attr("href"); exists {
					parsedURL, err := url.Parse(href)
					if err != nil {
						return
					}
					absURL := baseURL.ResolveReference(parsedURL).String()
					newJobs = append(newJobs, NewEmailJob(absURL, j.maxDepth-1, j.userAgent))
				}
			})
		}
		return result, newJobs, nil
	}

	return result, nil, nil
}

// Helper functions from emailjob.go
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

// CreateEmailTask creates a new email extraction task
func CreateEmailTask(url string, maxDepth int, userAgent string) (*asynq.Task, error) {
	payload := map[string]interface{}{
		"url":        url,
		"max_depth":  maxDepth,
		"user_agent": userAgent,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal email payload: %w", err)
	}
	return asynq.NewTask(TypeEmailExtract, data), nil
}

func (h *Handler) processEmailTask(ctx context.Context, task *asynq.Task) error {
	var payload struct {
		URL       string `json:"url"`
		MaxDepth  int    `json:"max_depth"`
		UserAgent string `json:"user_agent"`
	}
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal email payload: %w", err)
	}

	// Set default values if not provided
	if payload.MaxDepth == 0 {
		payload.MaxDepth = 2 // Default to crawling 2 levels deep
	}
	if payload.UserAgent == "" {
		payload.UserAgent = "Mozilla/5.0"
	}

	// Normalize URL
	if !strings.HasPrefix(payload.URL, "http") {
		payload.URL = "https://" + payload.URL
	}

	// Setup scrapemate
	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(h.concurrency),
		scrapemateapp.WithExitOnInactivity(h.taskTimeout),
	}

	if len(h.proxies) > 0 {
		opts = append(opts, scrapemateapp.WithProxies(h.proxies))
	}

	matecfg, err := scrapemateapp.NewConfig(nil, opts...)
	if err != nil {
		return fmt.Errorf("failed to create scrapemate config: %w", err)
	}

	mate, err := scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		return fmt.Errorf("failed to create scrapemate app: %w", err)
	}
	defer mate.Close()

	// Create and run the email extraction job
	job := NewEmailJob(payload.URL, payload.MaxDepth, payload.UserAgent)
	if err := mate.Start(ctx, job); err != nil {
		if err != context.DeadlineExceeded && err != context.Canceled {
			return fmt.Errorf("failed to run email extraction: %w", err)
		}
	}

	return nil
} 