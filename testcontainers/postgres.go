package testcontainers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultPostgresPort = "5432"
	defaultUser         = "test"
	defaultPassword     = "test"
	defaultDatabase     = "testdb"
)

// PostgresConfig holds PostgreSQL connection configuration for tests
type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// PostgresContainer represents a PostgreSQL container for testing
type PostgresContainer struct {
	testcontainers.Container
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// NewPostgresContainer creates a new PostgreSQL container
func NewPostgresContainer(ctx context.Context) (*PostgresContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:latest",
		ExposedPorts: []string{defaultPostgresPort + "/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     defaultUser,
			"POSTGRES_PASSWORD": defaultPassword,
			"POSTGRES_DB":       defaultDatabase,
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections"),
			wait.ForExposedPort(),
		),
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

	mappedPort, err := container.MappedPort(ctx, defaultPostgresPort)
	if err != nil {
		return nil, fmt.Errorf("failed to get container port: %w", err)
	}

	port, err := strconv.Atoi(mappedPort.Port())
	if err != nil {
		return nil, fmt.Errorf("failed to parse port: %w", err)
	}

	return &PostgresContainer{
		Container: container,
		Host:      host,
		Port:      port,
		User:      defaultUser,
		Password:  defaultPassword,
		Database:  defaultDatabase,
	}, nil
}

// GetDSN returns the PostgreSQL connection string
func (c *PostgresContainer) GetDSN() string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		c.User, c.Password, c.Host, c.Port, c.Database)
}
