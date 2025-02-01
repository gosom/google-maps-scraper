package redis

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Vector/vector-leads-scraper/redis/config"
	"github.com/hibiken/asynq"
)

// Client wraps asynq client functionality
type Client struct {
	client *asynq.Client
	cfg    *config.RedisConfig
	mu     sync.RWMutex
}

// Config holds Redis connection configuration parameters
type Config struct {
	Host            string
	Port            int
	Password        string
	DB              int
	Workers         int
	RetryInterval   time.Duration
	MaxRetries      int
	RetentionPeriod time.Duration
}

// NewClient creates a new Redis client with the provided configuration
func NewClient(cfg *config.RedisConfig) (*Client, error) {
	redisOpt := asynq.RedisClientOpt{
		Addr:     cfg.GetRedisAddr(),
		Password: cfg.Password,
		DB:       cfg.DB,
	}

	client := asynq.NewClient(redisOpt)
	// Test connection
	if err := testConnection(client); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &Client{
		client: client,
		cfg:    cfg,
	}, nil
}

// EnqueueTask enqueues a task with the given type and payload.
// Available options include:
//   - asynq.MaxRetry(n): Set maximum number of retries
//   - asynq.Queue(name): Specify queue name
//   - asynq.Timeout(d): Set task timeout duration
//   - asynq.Deadline(t): Set task deadline time
//   - asynq.Unique(ttl): Ensure task uniqueness with TTL
//   - asynq.ProcessAt(t): Schedule task for specific time
//   - asynq.ProcessIn(d): Schedule task after duration
//   - asynq.Retention(d): Set task retention duration
//   - asynq.Group(name): Specify task group
func (c *Client) EnqueueTask(ctx context.Context, taskType string, payload []byte, opts ...asynq.Option) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	task := asynq.NewTask(taskType, payload)
	_, err := c.client.EnqueueContext(ctx, task, opts...)
	if err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	return nil
}

// Close closes the Redis client connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.client.Close(); err != nil {
		return fmt.Errorf("failed to close Redis client: %w", err)
	}
	return nil
}

// IsHealthy checks if the Redis connection is healthy
func (c *Client) IsHealthy(ctx context.Context) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Ping Redis to check connection
	_, err := c.client.EnqueueContext(ctx, asynq.NewTask("health:check", nil))
	return err == nil
}

// RetryWithBackoff implements exponential backoff for connection retries
func RetryWithBackoff(operation func() error, maxRetries int, initialInterval time.Duration) error {
	var err error
	interval := initialInterval

	for i := 0; i < maxRetries; i++ {
		if err = operation(); err == nil {
			return nil
		}

		if i == maxRetries-1 {
			break
		}

		log.Printf("Retry attempt %d failed: %v. Retrying in %v...", i+1, err, interval)
		time.Sleep(interval)
		interval *= 2 // Exponential backoff
	}

	return fmt.Errorf("operation failed after %d retries: %w", maxRetries, err)
}

// testConnection tests the Redis connection
func testConnection(client *asynq.Client) error {
	ctx := context.Background()
	task := asynq.NewTask("connection:test", nil)
	_, err := client.EnqueueContext(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}
	return nil
}
