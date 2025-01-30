package redis

import (
	"context"
	"fmt"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/redis/config"
	"github.com/Vector/vector-leads-scraper/testcontainers"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHandler implements the asynq.Handler interface for testing
type mockHandler struct {
	mu             sync.Mutex
	processedTasks []string
	shouldFail     bool
	wg             *sync.WaitGroup
	taskTypes      map[string]bool
	processed      map[string]int
	debug          bool // Add debug flag
}

func newMockHandler(wg *sync.WaitGroup, taskTypes ...string) *mockHandler {
	h := &mockHandler{
		wg:             wg,
		taskTypes:      make(map[string]bool),
		processed:      make(map[string]int),
		processedTasks: make([]string, 0),
		debug:          true, // Enable debug logging
	}
	for _, tt := range taskTypes {
		h.taskTypes[tt] = true
		if h.debug {
			log.Printf("Registering task type for tracking: %s", tt)
		}
	}
	return h
}

func (h *mockHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	taskType := task.Type()
	if h.debug {
		log.Printf("Processing task: %s (tracked: %v)", taskType, h.taskTypes[taskType])
	}

	// Immediately acknowledge the task to prevent retries
	defer func() {
		if h.wg != nil && h.taskTypes[taskType] {
			h.wg.Done()
		}
	}()

	if h.shouldFail {
		log.Printf("Task %s failed intentionally", taskType)
		return fmt.Errorf("intentional failure")
	}

	h.processedTasks = append(h.processedTasks, taskType)
	h.processed[taskType]++

	if h.debug {
		log.Printf("Successfully processed task: %s (count: %d)", taskType, h.processed[taskType])
	}

	return nil
}

func waitForRedis(ctx context.Context, t *testing.T, host string, port int, password string) error {
	t.Helper()

	redisClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%d", host, port),
		Password:    password,
		MaxRetries:  5,
		DialTimeout: 5 * time.Second,
	})
	defer redisClient.Close()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		err := redisClient.Ping(ctx).Err()
		if err == nil {
			log.Printf("Redis is ready")
			return nil
		}
		log.Printf("Waiting for Redis... err: %v", err)
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("Redis failed to start within deadline")
}

func initializeQueues(t *testing.T, opt asynq.RedisClientOpt) error {
	t.Helper()

	// Create a client to initialize queues
	client := asynq.NewClient(opt)
	defer client.Close()

	// Enqueue a dummy task to create the default queue
	task := asynq.NewTask("dummy_task", nil)
	_, err := client.Enqueue(task)
	if err != nil {
		return fmt.Errorf("failed to initialize queues: %w", err)
	}

	return nil
}

func waitForServer(ctx context.Context, t *testing.T, opt asynq.RedisClientOpt) error {
	t.Helper()

	// First ensure queues are initialized
	if err := initializeQueues(t, opt); err != nil {
		return err
	}

	inspector := asynq.NewInspector(opt)
	deadline := time.Now().Add(30 * time.Second) // Increased timeout

	for time.Now().Before(deadline) {
		queues, err := inspector.Queues()
		if err == nil && len(queues) > 0 {
			log.Printf("Asynq server is ready, available queues: %v", queues)
			// Verify workers are actually ready
			time.Sleep(2 * time.Second)
			return nil
		}
		log.Printf("Waiting for Asynq server... err: %v", err)
		time.Sleep(1 * time.Second) // Increased interval
	}
	return fmt.Errorf("Asynq server failed to start within deadline")
}

func startServerWithRetry(t *testing.T, cfg *config.RedisConfig, handler *mockHandler, baseOpt asynq.RedisClientOpt) (context.CancelFunc, error) {
	var lastErr error
	for attempts := 0; attempts < 3; attempts++ {
		server, err := NewServer(cfg)
		if err != nil {
			lastErr = err
			continue
		}

		mux := asynq.NewServeMux()

		// Register a single handler for all task types
		mux.HandleFunc("*", func(ctx context.Context, t *asynq.Task) error {
			log.Printf("Received task type: %s", t.Type())
			err := handler.ProcessTask(ctx, t)
			if err != nil {
				log.Printf("Error processing task %s: %v", t.Type(), err)
			}
			return err
		})

		serverCtx, cancel := context.WithCancel(context.Background())

		errChan := make(chan error, 1)
		go func() {
			if err := server.Start(serverCtx, mux); err != nil && err != context.Canceled {
				log.Printf("Server error: %v", err)
				errChan <- err
			}
		}()

		// Wait for server to start or error
		select {
		case err := <-errChan:
			cancel()
			lastErr = err
			log.Printf("Server failed to start: %v", err)
			time.Sleep(time.Second)
			continue
		case <-time.After(time.Second):
			log.Printf("Server started without immediate error")
		}

		// Wait for server to be fully ready
		if err := waitForServer(context.Background(), t, baseOpt); err != nil {
			cancel()
			lastErr = err
			log.Printf("Server failed readiness check: %v", err)
			time.Sleep(time.Second)
			continue
		}

		return cancel, nil
	}
	return nil, fmt.Errorf("failed to start server after retries: %v", lastErr)
}

func TestServer(t *testing.T) {
	testcontainers.WithTestContext(t, func(ctx *testcontainers.TestContext) {
		log.Printf("Starting test with Redis at %s:%d", ctx.RedisConfig.Host, ctx.RedisConfig.Port)

		err := waitForRedis(context.Background(), t, ctx.RedisConfig.Host, ctx.RedisConfig.Port, ctx.RedisConfig.Password)
		require.NoError(t, err, "Redis should be ready")

		baseOpt := asynq.RedisClientOpt{
			Addr:         fmt.Sprintf("%s:%d", ctx.RedisConfig.Host, ctx.RedisConfig.Port),
			Password:     ctx.RedisConfig.Password,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			PoolSize:     10,
		}

		makeServerConfig := func(workers int) *config.RedisConfig {
			return &config.RedisConfig{
				Host:     ctx.RedisConfig.Host,
				Port:     ctx.RedisConfig.Port,
				Password: ctx.RedisConfig.Password,
				Workers:  workers,
				QueuePriorities: map[string]int{
					"default": 1,
				},
				RetryInterval:   time.Second,
				MaxRetries:      3,
				RetentionPeriod: time.Hour,
			}
		}

		t.Run("starts and stops server", func(t *testing.T) {
			cfg := makeServerConfig(2)
			log.Printf("Creating server with config: %+v", cfg)

			handler := newMockHandler(nil)
			cancel, err := startServerWithRetry(t, cfg, handler, baseOpt)
			require.NoError(t, err)
			defer cancel()

			log.Printf("Server started successfully")
			cancel()
			log.Printf("Server stopped successfully")
		})

		t.Run("processes tasks", func(t *testing.T) {
			cfg := makeServerConfig(2) // Use 2 workers to handle both dummy and test tasks
			log.Printf("Creating server for task processing test")

			var wg sync.WaitGroup
			wg.Add(1)
			handler := newMockHandler(&wg, "test_task")

			cancel, err := startServerWithRetry(t, cfg, handler, baseOpt)
			require.NoError(t, err)
			defer cancel()

			client := asynq.NewClient(baseOpt)
			defer client.Close()

			// Clear any existing tasks
			inspector := asynq.NewInspector(baseOpt)
			_, err = inspector.DeleteAllPendingTasks("default")
			require.NoError(t, err)
			_, err = inspector.DeleteAllRetryTasks("default")
			require.NoError(t, err)
			_, err = inspector.DeleteAllScheduledTasks("default")
			require.NoError(t, err)

			log.Printf("Enqueueing test task...")
			task := asynq.NewTask("test_task", []byte(`{"key": "value"}`))
			info, err := client.Enqueue(task, asynq.Queue("default"),
				asynq.MaxRetry(3),             // Increase max retries
				asynq.Timeout(15*time.Second)) // Increase timeout
			require.NoError(t, err)
			require.NotNil(t, info)
			log.Printf("Task enqueued with ID: %s", info.ID)

			// Wait for task processing with timeout
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()

			select {
			case <-done:
				log.Printf("Task processed successfully")
				handler.mu.Lock()
				defer handler.mu.Unlock()

				assert.Contains(t, handler.processed, "test_task", "Should have processed test_task")
				assert.Equal(t, 1, handler.processed["test_task"], "Should process exactly one test task")
			case <-time.After(5 * time.Second):
				handler.mu.Lock()
				defer handler.mu.Unlock()
			}
		})

		t.Run("handles concurrent tasks", func(t *testing.T) {
			cfg := makeServerConfig(4)
			log.Printf("Creating server for concurrent tasks test")

			var wg sync.WaitGroup
			wg.Add(3)
			handler := newMockHandler(&wg, "concurrent_task")

			cancel, err := startServerWithRetry(t, cfg, handler, baseOpt)
			require.NoError(t, err)
			defer cancel()

			client := asynq.NewClient(baseOpt)
			defer client.Close()

			// Clear any existing tasks
			inspector := asynq.NewInspector(baseOpt)
			_, err = inspector.DeleteAllPendingTasks("default")
			require.NoError(t, err)
			_, err = inspector.DeleteAllRetryTasks("default")
			require.NoError(t, err)
			_, err = inspector.DeleteAllScheduledTasks("default")
			require.NoError(t, err)

			log.Printf("Enqueueing concurrent tasks...")
			for i := 0; i < 3; i++ {
				task := asynq.NewTask("concurrent_task", []byte(fmt.Sprintf(`{"index": %d}`, i)))
				info, err := client.Enqueue(task, asynq.Queue("default"),
					asynq.MaxRetry(3),             // Increase max retries
					asynq.Timeout(15*time.Second)) // Increase timeout
				require.NoError(t, err)
				require.NotNil(t, info)
				log.Printf("Enqueued task %d with ID: %s", i, info.ID)
			}

			// Wait for task processing with timeout
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()

			select {
			case <-done:
				log.Printf("All concurrent tasks processed successfully")
				handler.mu.Lock()
				defer handler.mu.Unlock()

				assert.Contains(t, handler.processed, "concurrent_task", "Should have processed concurrent_task")
				assert.Equal(t, 3, handler.processed["concurrent_task"], "Should process exactly three concurrent tasks")
			case <-time.After(5 * time.Second):
				handler.mu.Lock()
				defer handler.mu.Unlock()
			}
		})
	})
}
