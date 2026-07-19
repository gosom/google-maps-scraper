package scrapemate

import "time"

type RetryPolicy int

const (
	// DefaultUserAgent is the default user agent scrape mate uses
	DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/104.0.5112.79 Safari/537.36"

	// RetryJob  retry a job
	RetryJob = 0
	// DiscardJob just discard it in case crawling fails
	DiscardJob = 1
	// RefreshIP refresh the api and then retry job
	RefreshIP = 2
	// StopScraping exit scraping completely when an error happens
	StopScraping = 3

	// DefaultMaxRetryDelay the default max delay between 2 consequive retries
	DefaultMaxRetryDelay = 2 * time.Second

	// PriorityHigh high priority
	PriorityHigh = 0
	// PriorityMedium medium priority
	PriorityMedium = 1
	// PriorityLow low priority
	PriorityLow = 2
)
