// Package tasks provides Redis task handling functionality
package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// TaskHandler handles processing of Redis tasks
type TaskHandler interface {
	ProcessTask(ctx context.Context, task *asynq.Task) error
}

// Handler implements TaskHandler interface
type Handler struct {
	maxRetries    int
	retryInterval time.Duration
	taskTimeout   time.Duration
	dataFolder    string
	concurrency   int
	proxies       []string
	disableReuse  bool
}

// HandlerOption is a function that configures a Handler
type HandlerOption func(*Handler)

// WithMaxRetries sets the maximum number of retries for a task
func WithMaxRetries(retries int) HandlerOption {
	return func(h *Handler) {
		h.maxRetries = retries
	}
}

// WithRetryInterval sets the retry interval for failed tasks
func WithRetryInterval(interval time.Duration) HandlerOption {
	return func(h *Handler) {
		h.retryInterval = interval
	}
}

// WithTaskTimeout sets the timeout for task processing
func WithTaskTimeout(timeout time.Duration) HandlerOption {
	return func(h *Handler) {
		h.taskTimeout = timeout
	}
}

// WithDataFolder sets the data folder for storing results
func WithDataFolder(folder string) HandlerOption {
	return func(h *Handler) {
		h.dataFolder = folder
	}
}

// WithConcurrency sets the concurrency for scraping
func WithConcurrency(n int) HandlerOption {
	return func(h *Handler) {
		h.concurrency = n
	}
}

// WithProxies sets the proxies for scraping
func WithProxies(proxies []string) HandlerOption {
	return func(h *Handler) {
		h.proxies = proxies
	}
}

// WithDisablePageReuse disables page reuse
func WithDisablePageReuse(disable bool) HandlerOption {
	return func(h *Handler) {
		h.disableReuse = disable
	}
}

// NewHandler creates a new task handler with the provided options
func NewHandler(opts ...HandlerOption) *Handler {
	h := &Handler{
		maxRetries:    3,
		retryInterval: 5 * time.Second,
		taskTimeout:   30 * time.Second,
		concurrency:   2,
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

// ProcessTask processes a task based on its type
func (h *Handler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	ctx, cancel := context.WithTimeout(ctx, h.taskTimeout)
	defer cancel()

	switch task.Type() {
	case TypeScrapeGMaps:
		return h.processScrapeTask(ctx, task)
	case TypeEmailExtract:
		return h.processEmailTask(ctx, task)
	case TypeHealthCheck:
		return nil // Health check task always succeeds
	case TypeConnectionTest:
		return nil // Connection test task always succeeds
	default:
		return fmt.Errorf("unknown task type: %s", task.Type())
	}
}
