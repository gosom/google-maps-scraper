package config

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type redisContainer struct {
	testcontainers.Container
	URI string
}

func setupRedis(ctx context.Context) (*redisContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "redis:latest",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:         true,
	})
	if err != nil {
		return nil, err
	}

	mappedPort, err := container.MappedPort(ctx, "6379")
	if err != nil {
		return nil, err
	}

	hostIP, err := container.Host(ctx)
	if err != nil {
		return nil, err
	}

	uri := fmt.Sprintf("%s:%s", hostIP, mappedPort.Port())

	return &redisContainer{
		Container: container,
		URI:       uri,
	}, nil
}

func TestRedisConfigIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	redisC, err := setupRedis(ctx)
	if err != nil {
		t.Fatalf("Failed to setup Redis container: %v", err)
	}
	defer func() {
		if err := redisC.Terminate(ctx); err != nil {
			t.Fatalf("Failed to terminate container: %v", err)
		}
	}()

	tests := []struct {
		name      string
		config    *RedisConfig
		wantError bool
	}{
		{
			name: "valid connection",
			config: &RedisConfig{
				Host:            "localhost",
				Port:            6379,
				RetryInterval:   time.Second,
				MaxRetries:      3,
				RetentionPeriod: 24 * time.Hour,
				QueuePriorities: DefaultQueuePriorities,
			},
			wantError: false,
		},
		{
			name: "invalid port",
			config: &RedisConfig{
				Host:            "localhost",
				Port:            1234,
				RetryInterval:   time.Second,
				MaxRetries:      1,
				RetentionPeriod: 24 * time.Hour,
				QueuePriorities: DefaultQueuePriorities,
			},
			wantError: true,
		},
		{
			name: "with password",
			config: &RedisConfig{
				Host:            "localhost",
				Port:            6379,
				Password:        "testpass",
				RetryInterval:   time.Second,
				MaxRetries:      1,
				RetentionPeriod: 24 * time.Hour,
				QueuePriorities: DefaultQueuePriorities,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Update the config with the container's host and port
			host, err := redisC.Host(ctx)
			if err != nil {
				t.Fatalf("Failed to get container host: %v", err)
			}
			
			port, err := redisC.MappedPort(ctx, "6379")
			if err != nil {
				t.Fatalf("Failed to get container port: %v", err)
			}

			if !tt.wantError {
				tt.config.Host = host
				tt.config.Port = port.Int()
			}

			// Test Redis connection
			client := redis.NewClient(&redis.Options{
				Addr:     tt.config.GetRedisAddr(),
				Password: tt.config.Password,
				DB:       tt.config.DB,
			})
			defer client.Close()

			_, err = client.Ping(ctx).Result()
			if (err != nil) != tt.wantError {
				t.Errorf("Redis connection test failed: got error = %v, wantError %v", err, tt.wantError)
			}

			// Test queue priorities
			if !tt.wantError {
				priorities := tt.config.QueuePriorities
				if len(priorities) != 3 {
					t.Errorf("Expected 3 queue priorities, got %d", len(priorities))
				}

				expectedPriorities := DefaultQueuePriorities
				for queue, priority := range priorities {
					if expectedPriority, ok := expectedPriorities[queue]; !ok || priority != expectedPriority {
						t.Errorf("Queue %s: got priority %d, want %d", queue, priority, expectedPriority)
					}
				}
			}
		})
	}
}

func TestNewRedisConfig(t *testing.T) {
	// Set test mode environment variable
	os.Setenv("GO_TEST", "1")
	defer os.Unsetenv("GO_TEST")

	tests := []struct {
		name      string
		envVars   map[string]string
		want      *RedisConfig
		wantError bool
	}{
		{
			name: "default configuration",
			want: &RedisConfig{
				Host:            "localhost",
				Port:            6379,
				DB:              0,
				Workers:         10,
				RetryInterval:   5 * time.Second,
				MaxRetries:      3,
				RetentionPeriod: 7 * 24 * time.Hour,
				QueuePriorities: DefaultQueuePriorities,
			},
			wantError: false,
		},
		{
			name: "custom configuration",
			envVars: map[string]string{
				"REDIS_HOST":              "redis.example.com",
				"REDIS_PORT":              "6380",
				"REDIS_PASSWORD":          "secret",
				"REDIS_DB":                "1",
				"REDIS_WORKERS":           "20",
				"REDIS_RETRY_INTERVAL":    "10s",
				"REDIS_MAX_RETRIES":       "5",
				"REDIS_RETENTION_DAYS":    "14",
				"REDIS_USE_TLS":           "true",
				"REDIS_CERT_FILE":         "/path/to/cert",
				"REDIS_KEY_FILE":          "/path/to/key",
				"REDIS_CA_FILE":           "/path/to/ca",
			},
			want: &RedisConfig{
				Host:            "redis.example.com",
				Port:            6380,
				Password:        "secret",
				DB:              1,
				Workers:         20,
				RetryInterval:   10 * time.Second,
				MaxRetries:      5,
				RetentionPeriod: 14 * 24 * time.Hour,
				UseTLS:          true,
				CertFile:        "/path/to/cert",
				KeyFile:         "/path/to/key",
				CAFile:          "/path/to/ca",
				QueuePriorities: DefaultQueuePriorities,
			},
			wantError: false,
		},
		{
			name: "invalid port",
			envVars: map[string]string{
				"REDIS_PORT": "invalid",
			},
			wantError: true,
		},
		{
			name: "invalid DB",
			envVars: map[string]string{
				"REDIS_DB": "invalid",
			},
			wantError: true,
		},
		{
			name: "invalid workers",
			envVars: map[string]string{
				"REDIS_WORKERS": "invalid",
			},
			wantError: true,
		},
		{
			name: "invalid retry interval",
			envVars: map[string]string{
				"REDIS_RETRY_INTERVAL": "invalid",
			},
			wantError: true,
		},
		{
			name: "invalid max retries",
			envVars: map[string]string{
				"REDIS_MAX_RETRIES": "invalid",
			},
			wantError: true,
		},
		{
			name: "invalid retention days",
			envVars: map[string]string{
				"REDIS_RETENTION_DAYS": "invalid",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables for the test
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			got, err := NewRedisConfig()
			if (err != nil) != tt.wantError {
				t.Errorf("NewRedisConfig() error = %v, wantError %v", err, tt.wantError)
				return
			}

			if !tt.wantError {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestRedisConfig_GetRedisAddr(t *testing.T) {
	tests := []struct {
		name string
		cfg  *RedisConfig
		want string
	}{
		{
			name: "default address",
			cfg: &RedisConfig{
				Host: "localhost",
				Port: 6379,
			},
			want: "localhost:6379",
		},
		{
			name: "custom address",
			cfg: &RedisConfig{
				Host: "redis.example.com",
				Port: 6380,
			},
			want: "redis.example.com:6380",
		},
		{
			name: "ipv4 address",
			cfg: &RedisConfig{
				Host: "127.0.0.1",
				Port: 6379,
			},
			want: "127.0.0.1:6379",
		},
		{
			name: "ipv6 address",
			cfg: &RedisConfig{
				Host: "::1",
				Port: 6379,
			},
			want: "[::1]:6379",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.GetRedisAddr(); got != tt.want {
				t.Errorf("RedisConfig.GetRedisAddr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedisConfig_QueuePriorities(t *testing.T) {
	cfg := &RedisConfig{
		QueuePriorities: DefaultQueuePriorities,
	}

	expectedPriorities := map[string]int{
		"critical": 6,
		"default":  3,
		"low":      1,
	}

	if len(cfg.QueuePriorities) != len(expectedPriorities) {
		t.Errorf("Expected %d queue priorities, got %d", len(expectedPriorities), len(cfg.QueuePriorities))
	}

	for queue, priority := range expectedPriorities {
		if got := cfg.QueuePriorities[queue]; got != priority {
			t.Errorf("Queue %s: got priority %d, want %d", queue, got, priority)
		}
	}
} 