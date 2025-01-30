package testcontainers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// defaultRedisPort is the default port exposed by the Redis container
	defaultRedisPort = "6379"
)

// RedisConfig holds Redis connection configuration for tests.
// It provides all necessary parameters to establish a connection to the Redis instance.
//
// Example:
//
//	config := &RedisConfig{
//	    Host:     "localhost",
//	    Port:     6379,
//	    Password: "optional-password",
//	}
type RedisConfig struct {
	// Host is the Redis server hostname or IP address
	Host string

	// Port is the Redis server port number
	Port int

	// Password is the optional Redis authentication password
	Password string
}

// RedisContainer represents a Redis container for testing.
// It wraps the testcontainers.Container interface and provides
// additional Redis-specific functionality.
//
// Example:
//
//	container, err := NewRedisContainer(ctx)
//	if err != nil {
//	    t.Fatal(err)
//	}
//	defer container.Terminate(ctx)
//
//	address := container.GetAddress()
type RedisContainer struct {
	testcontainers.Container
	// Host is the container's hostname or IP address
	Host string

	// Port is the exposed Redis port
	Port int

	// Password is the Redis authentication password (empty for test containers)
	Password string
}

// NewRedisContainer creates a new Redis container for testing.
// It starts a container with the latest Redis image and waits for it
// to be ready to accept connections.
//
// The container is configured with:
//   - Latest Redis image
//   - Default Redis port (6379)
//   - No authentication
//   - Health check waiting for "Ready to accept connections" message
//
// Example:
//
//	container, err := NewRedisContainer(ctx)
//	if err != nil {
//	    t.Fatal(err)
//	}
//	defer container.Terminate(ctx)
func NewRedisContainer(ctx context.Context) (*RedisContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "redis:latest",
		ExposedPorts: []string{defaultRedisPort + "/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, defaultRedisPort)
	if err != nil {
		return nil, fmt.Errorf("failed to get container port: %w", err)
	}

	port, err := strconv.Atoi(mappedPort.Port())
	if err != nil {
		return nil, fmt.Errorf("failed to parse port: %w", err)
	}

	return &RedisContainer{
		Container: container,
		Host:      host,
		Port:      port,
		Password:  "", // No password for test container
	}, nil
}

// GetAddress returns the Redis address in host:port format.
// This format is suitable for use with Redis clients.
//
// Example:
//
//	address := container.GetAddress() // Returns something like "localhost:49153"
func (c *RedisContainer) GetAddress() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
