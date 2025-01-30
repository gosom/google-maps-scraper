// Package testcontainers provides a comprehensive testing infrastructure for integration tests
// that require Redis and PostgreSQL databases. It manages the lifecycle of Docker containers,
// connection pooling, and automatic cleanup of resources.
//
// The package is designed to make integration testing as simple as possible while ensuring
// proper isolation between tests and reliable cleanup of resources. It uses testcontainers-go
// to manage Docker containers and provides high-level abstractions for common testing scenarios.
//
// Basic usage:
//
//	func TestMyFeature(t *testing.T) {
//	    ctx := testcontainers.NewTestContext(t)
//	    defer ctx.Cleanup()
//
//	    // Use Redis
//	    err := ctx.Redis.Set(ctx.ctx, "key", "value", time.Hour).Err()
//	    require.NoError(t, err)
//
//	    // Use PostgreSQL
//	    var count int
//	    err = ctx.DB.QueryRow(ctx.ctx, "SELECT COUNT(*) FROM users").Scan(&count)
//	    require.NoError(t, err)
//	}
//
// The package also provides a helper function WithTestContext for more concise test writing:
//
//	func TestMyFeature(t *testing.T) {
//	    testcontainers.WithTestContext(t, func(ctx *TestContext) {
//	        // Test code here
//	    })
//	}
//
// Prerequisites:
//   - Docker must be installed and running
//   - Go 1.16 or later
//   - Network access to pull Docker images
//
// Environment Variables:
//   - TESTCONTAINERS_RYUK_DISABLED: Set to "true" to disable Ryuk (container cleanup)
//   - DOCKER_HOST: Custom Docker host (optional)
package testcontainers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	// defaultTimeout is the maximum time to wait for container startup and initialization
	defaultTimeout = 30 * time.Second
)

// TestContext holds all the test infrastructure components and provides methods
// for managing test containers and their lifecycle. It ensures proper cleanup
// of resources even in case of test failures.
//
// TestContext manages:
//   - Container lifecycle (creation, health checks, termination)
//   - Connection pooling for databases
//   - Automatic cleanup of resources
//   - Configuration for all services
//
// Example usage:
//
//	ctx := testcontainers.NewTestContext(t)
//	defer ctx.Cleanup()
//
//	// Use Redis
//	err := ctx.Redis.Set(ctx.ctx, "key", "value", time.Hour).Err()
//	require.NoError(t, err)
//
//	// Use PostgreSQL
//	result, err := ctx.DB.Exec(ctx.ctx, "INSERT INTO users (name) VALUES ($1)", "test")
//	require.NoError(t, err)
type TestContext struct {
	t *testing.T

	// Context and cleanup
	ctx        context.Context
	cancelFunc context.CancelFunc
	cleanup    []func()

	// Infrastructure
	redisContainer    *RedisContainer
	postgresContainer *PostgresContainer

	// Clients
	Redis *redis.Client // Redis client for test operations
	DB    *pgxpool.Pool // PostgreSQL connection pool

	// Configuration
	RedisConfig    *RedisConfig    // Redis connection configuration
	PostgresConfig *PostgresConfig // PostgreSQL connection configuration
}

// NewTestContext creates a new test context with all required infrastructure.
// It initializes Redis and PostgreSQL containers, sets up connection pools,
// and configures automatic cleanup.
//
// The function will fail the test if any container fails to start or if
// connections cannot be established.
//
// Example:
//
//	func TestMyFeature(t *testing.T) {
//	    ctx := testcontainers.NewTestContext(t)
//	    defer ctx.Cleanup()
//
//	    // Use the test context
//	    err := ctx.Redis.Set(ctx.ctx, "key", "value", time.Hour).Err()
//	    require.NoError(t, err)
//	}
func NewTestContext(t *testing.T) *TestContext {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	tc := &TestContext{
		t:          t,
		ctx:        ctx,
		cancelFunc: cancel,
		cleanup:    make([]func(), 0),
	}

	// Initialize containers
	if err := tc.initRedis(); err != nil {
		t.Fatalf("Failed to initialize Redis: %v", err)
	}
	if err := tc.initPostgres(); err != nil {
		t.Fatalf("Failed to initialize Postgres: %v", err)
	}

	return tc
}

// WithTestContext runs a test function with a test context and handles cleanup automatically.
// This is the recommended way to write integration tests as it ensures proper cleanup
// even if the test panics.
//
// Example:
//
//	func TestMyFeature(t *testing.T) {
//	    testcontainers.WithTestContext(t, func(ctx *TestContext) {
//	        // Test code here
//	        err := ctx.Redis.Set(ctx.ctx, "key", "value", time.Hour).Err()
//	        require.NoError(t, err)
//	    })
//	}
func WithTestContext(t *testing.T, fn func(*TestContext)) {
	t.Helper()
	ctx := NewTestContext(t)
	defer ctx.Cleanup()
	fn(ctx)
}

// Cleanup performs cleanup of all resources in reverse order of creation.
// This ensures proper shutdown of services and prevents resource leaks.
// It should be called using defer immediately after creating a TestContext.
//
// Example:
//
//	ctx := testcontainers.NewTestContext(t)
//	defer ctx.Cleanup() // Always clean up resources
func (tc *TestContext) Cleanup() {
	// Execute cleanup functions in reverse order
	for i := len(tc.cleanup) - 1; i >= 0; i-- {
		tc.cleanup[i]()
	}
	tc.cancelFunc()
}

// addCleanup adds a cleanup function to be executed during teardown.
// Cleanup functions are executed in reverse order (last added, first executed).
func (tc *TestContext) addCleanup(fn func()) {
	tc.cleanup = append(tc.cleanup, fn)
}

// initRedis initializes the Redis container and client.
// It sets up the container, waits for it to be ready, and creates a client
// with the correct configuration.
func (tc *TestContext) initRedis() error {
	// Initialize Redis container
	container, err := NewRedisContainer(tc.ctx)
	if err != nil {
		return fmt.Errorf("failed to create Redis container: %w", err)
	}
	tc.redisContainer = container
	tc.addCleanup(func() {
		if err := container.Terminate(tc.ctx); err != nil {
			tc.t.Errorf("Failed to terminate Redis container: %v", err)
		}
	})

	// Initialize Redis client
	tc.Redis = redis.NewClient(&redis.Options{
		Addr:     container.GetAddress(),
		Password: container.Password,
		DB:       0,
	})
	tc.addCleanup(func() {
		if err := tc.Redis.Close(); err != nil {
			tc.t.Errorf("Failed to close Redis client: %v", err)
		}
	})

	// Store configuration
	tc.RedisConfig = &RedisConfig{
		Host:     container.Host,
		Port:     container.Port,
		Password: container.Password,
	}

	return nil
}

// initPostgres initializes the PostgreSQL container and connection pool.
// It sets up the container, waits for it to be ready, and creates a connection
// pool with the correct configuration.
func (tc *TestContext) initPostgres() error {
	// Initialize Postgres container
	container, err := NewPostgresContainer(tc.ctx)
	if err != nil {
		return fmt.Errorf("failed to create Postgres container: %w", err)
	}
	tc.postgresContainer = container
	tc.addCleanup(func() {
		if err := container.Terminate(tc.ctx); err != nil {
			tc.t.Errorf("Failed to terminate Postgres container: %v", err)
		}
	})

	// Initialize database connection
	pool, err := pgxpool.New(tc.ctx, container.GetDSN())
	if err != nil {
		return fmt.Errorf("failed to create database connection: %w", err)
	}
	tc.DB = pool
	tc.addCleanup(func() {
		tc.DB.Close()
	})

	// Store configuration
	tc.PostgresConfig = &PostgresConfig{
		Host:     container.Host,
		Port:     container.Port,
		User:     container.User,
		Password: container.Password,
		Database: container.Database,
	}

	return nil
}
