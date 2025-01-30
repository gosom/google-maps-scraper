package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
)

// mockBlockingHandler is used to test context cancellation
type mockBlockingHandler struct {
	*Handler
	blockCh chan struct{}
}

func newMockBlockingHandler(h *Handler) *mockBlockingHandler {
	return &mockBlockingHandler{
		Handler: h,
		blockCh: make(chan struct{}),
	}
}

func (h *mockBlockingHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	// Block until context is cancelled or blockCh is closed
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.blockCh:
		return nil
	}
}

func TestNewHandler(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		h := NewHandler()
		assert.Equal(t, 3, h.maxRetries)
		assert.Equal(t, 5*time.Second, h.retryInterval)
		assert.Equal(t, 30*time.Second, h.taskTimeout)
		assert.Equal(t, 2, h.concurrency)
		assert.Empty(t, h.dataFolder)
		assert.Empty(t, h.proxies)
		assert.False(t, h.disableReuse)
	})

	t.Run("custom configuration", func(t *testing.T) {
		proxies := []string{"proxy1", "proxy2"}
		h := NewHandler(
			WithMaxRetries(5),
			WithRetryInterval(10*time.Second),
			WithTaskTimeout(1*time.Minute),
			WithDataFolder("/data"),
			WithConcurrency(4),
			WithProxies(proxies),
			WithDisablePageReuse(true),
		)

		assert.Equal(t, 5, h.maxRetries)
		assert.Equal(t, 10*time.Second, h.retryInterval)
		assert.Equal(t, 1*time.Minute, h.taskTimeout)
		assert.Equal(t, "/data", h.dataFolder)
		assert.Equal(t, 4, h.concurrency)
		assert.Equal(t, proxies, h.proxies)
		assert.True(t, h.disableReuse)
	})
}

func TestProcessTask(t *testing.T) {
	t.Run("unknown task type", func(t *testing.T) {
		h := NewHandler()
		task := asynq.NewTask("unknown_type", nil)
		err := h.ProcessTask(context.Background(), task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown task type")
	})

	t.Run("scrape task", func(t *testing.T) {
		h := NewHandler(
			WithDataFolder(t.TempDir()),
			WithConcurrency(1),
		)
		task := asynq.NewTask(TypeScrapeGMaps, []byte(`{}`))
		err := h.ProcessTask(context.Background(), task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no keywords provided")
	})

	t.Run("context timeout", func(t *testing.T) {
		baseHandler := NewHandler(
			WithDataFolder(t.TempDir()),
			WithTaskTimeout(1*time.Hour), // Long timeout to ensure context timeout triggers first
		)
		h := newMockBlockingHandler(baseHandler)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		task := asynq.NewTask(TypeScrapeGMaps, []byte(`{"keywords": ["test"]}`))

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.ProcessTask(ctx, task)
		}()

		select {
		case err := <-errCh:
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "context deadline exceeded")
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Test timed out waiting for context cancellation")
		}
	})
}

func TestTaskValidation(t *testing.T) {
	h := NewHandler(
		WithDataFolder(t.TempDir()),
	)

	t.Run("invalid scrape task payload", func(t *testing.T) {
		task := asynq.NewTask(TypeScrapeGMaps, []byte(`{invalid json}`))
		err := h.ProcessTask(context.Background(), task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal scrape payload")
	})

	t.Run("invalid email task payload", func(t *testing.T) {
		task := asynq.NewTask(TypeEmailExtract, []byte(`{invalid json}`))
		err := h.ProcessTask(context.Background(), task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal email payload")
	})

	t.Run("empty scrape task payload", func(t *testing.T) {
		task := asynq.NewTask(TypeScrapeGMaps, nil)
		err := h.ProcessTask(context.Background(), task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal scrape payload")
	})

	t.Run("empty email task payload", func(t *testing.T) {
		task := asynq.NewTask(TypeEmailExtract, nil)
		err := h.ProcessTask(context.Background(), task)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal email payload")
	})
}
