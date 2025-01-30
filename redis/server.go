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

// Server wraps asynq server functionality
type Server struct {
	server *asynq.Server
	cfg    *config.RedisConfig
	mu     sync.RWMutex
}

// NewServer creates a new Redis server with the provided configuration
func NewServer(cfg *config.RedisConfig) (*Server, error) {
	redisOpt := asynq.RedisClientOpt{
		Addr:         cfg.GetRedisAddr(),
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		PoolSize:     10,
	}

	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: cfg.Workers,
			RetryDelayFunc: func(n int, err error, task *asynq.Task) time.Duration {
				if n >= cfg.MaxRetries {
					log.Printf("Task %s exhausted retries: %v", task.Type(), err)
					return -1 * time.Second
				}
				// Use exponential backoff with a minimum of 1 second
				delay := time.Duration(1<<uint(n)) * time.Second
				if delay > cfg.RetryInterval {
					delay = cfg.RetryInterval
				}
				log.Printf("Task %s failed, retry %d scheduled in %v: %v", task.Type(), n, delay, err)
				return delay
			},
			Queues:         cfg.QueuePriorities,
			StrictPriority: true,
		},
	)

	return &Server{
		server: srv,
		cfg:    cfg,
	}, nil
}

// Start starts the server with the provided handler
func (s *Server) Start(ctx context.Context, mux *asynq.ServeMux) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.server.Start(mux); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Monitor server health
	go s.monitorHealth(ctx)

	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.server.Shutdown()
	return nil
}

// IsHealthy checks if the server is healthy
func (s *Server) IsHealthy(ctx context.Context) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// For asynq server, we can consider it healthy if it's running
	// You might want to add more sophisticated health checks based on your needs
	return true
}

// monitorHealth periodically checks server health
func (s *Server) monitorHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.IsHealthy(ctx) {
				log.Println("Warning: Redis server is not healthy")
			}
		}
	}
}
