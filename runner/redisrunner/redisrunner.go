// Package redisrunner provides Redis-backed task processing functionality.
package redisrunner

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Vector/vector-leads-scraper/redis"
	"github.com/Vector/vector-leads-scraper/redis/config"
	"github.com/Vector/vector-leads-scraper/redis/tasks"
	"github.com/Vector/vector-leads-scraper/runner"
	"github.com/hibiken/asynq"
)

// RedisRunner implements the runner.Runner interface for Redis-backed task processing.
type RedisRunner struct {
	cfg      *config.RedisConfig
	server   *redis.Server
	client   *redis.Client
	mux      *asynq.ServeMux
	wg       sync.WaitGroup
	done     chan struct{}
	handlers map[string]tasks.TaskHandler
}

// New creates a new RedisRunner from the provided configuration.
func New(cfg *runner.Config) (*RedisRunner, error) {
	var redisCfg *config.RedisConfig
	var err error

	// If Redis URL is provided, use it
	if cfg.RedisURL != "" {
		// Set the Redis URL in environment for the config package to pick up
		if err := os.Setenv("REDIS_URL", cfg.RedisURL); err != nil {
			return nil, fmt.Errorf("failed to set Redis URL: %w", err)
		}
		redisCfg, err = config.NewRedisConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create Redis config from URL: %w", err)
		}
	} else {
		// Create Redis configuration from individual parameters
		redisCfg = &config.RedisConfig{
			Host:            cfg.RedisHost,
			Port:            cfg.RedisPort,
			Password:        cfg.RedisPassword,
			DB:              cfg.RedisDB,
			UseTLS:          cfg.RedisUseTLS,
			CertFile:        cfg.RedisCertFile,
			KeyFile:         cfg.RedisKeyFile,
			CAFile:          cfg.RedisCAFile,
			Workers:         cfg.RedisWorkers,
			RetryInterval:   cfg.RedisRetryInterval,
			MaxRetries:      cfg.RedisMaxRetries,
			RetentionPeriod: time.Duration(cfg.RedisRetentionDays) * 24 * time.Hour,
		}
	}

	// Set the workers count
	redisCfg.Workers = cfg.RedisWorkers

	// Initialize task handlers
	handlers := map[string]tasks.TaskHandler{
		tasks.TypeScrapeGMaps: tasks.NewHandler(
			tasks.WithMaxRetries(cfg.RedisMaxRetries),
			tasks.WithRetryInterval(cfg.RedisRetryInterval),
		),
		tasks.TypeEmailExtract: tasks.NewHandler(
			tasks.WithMaxRetries(cfg.RedisMaxRetries),
			tasks.WithRetryInterval(cfg.RedisRetryInterval),
		),
	}

	// Initialize Redis client
	client, err := redis.NewClient(redisCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}

	// Initialize Redis server
	server, err := redis.NewServer(redisCfg)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create Redis server: %w", err)
	}

	// Initialize task handlers
	mux := asynq.NewServeMux()
	for taskType, handler := range handlers {
		h := handler // Create a new variable to avoid closure issues
		mux.HandleFunc(taskType, func(ctx context.Context, task *asynq.Task) error {
			return h.ProcessTask(ctx, task)
		})
	}

	return &RedisRunner{
		cfg:      redisCfg,
		server:   server,
		client:   client,
		mux:      mux,
		done:     make(chan struct{}),
		handlers: handlers,
	}, nil
}

// Run starts the Redis runner and begins processing tasks.
func (r *RedisRunner) Run(ctx context.Context) error {
	log.Printf("Starting Redis runner with %d workers", r.cfg.Workers)

	// Start health check goroutine
	r.wg.Add(1)
	go r.monitorHealth(ctx)

	// Start the server in a goroutine
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := r.server.Start(ctx, r.mux); err != nil {
			log.Printf("Error running Redis server: %v", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	return nil
}

// Close gracefully shuts down the Redis runner.
func (r *RedisRunner) Close(ctx context.Context) error {
	log.Println("Shutting down Redis runner...")

	// Signal all goroutines to stop
	close(r.done)

	// Wait for all goroutines to finish
	r.wg.Wait()

	// Shutdown server
	if err := r.server.Shutdown(ctx); err != nil {
		log.Printf("Error shutting down Redis server: %v", err)
	}

	// Close client
	if err := r.client.Close(); err != nil {
		log.Printf("Error closing Redis client: %v", err)
	}

	log.Println("Redis runner shutdown complete")
	return nil
}

// monitorHealth periodically checks the health of Redis connections.
func (r *RedisRunner) monitorHealth(ctx context.Context) {
	defer r.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		default:
			if !r.client.IsHealthy(ctx) {
				log.Println("Warning: Redis client connection is not healthy")
			}
			if !r.server.IsHealthy(ctx) {
				log.Println("Warning: Redis server is not healthy")
			}
		}
	}
}

// EnqueueTask enqueues a new task for processing.
func (r *RedisRunner) EnqueueTask(ctx context.Context, taskType string, payload []byte, opts ...asynq.Option) error {
	return r.client.EnqueueTask(ctx, taskType, payload, opts...)
}
