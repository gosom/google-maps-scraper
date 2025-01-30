// Package config provides Redis configuration management for the Google Maps scraper.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// RedisConfig holds Redis connection and configuration parameters
type RedisConfig struct {
	Host            string
	Port            int
	Password        string
	DB              int
	Workers         int
	RetryInterval   time.Duration
	MaxRetries      int
	RetentionPeriod time.Duration
	UseTLS          bool
	CertFile        string
	KeyFile         string
	CAFile          string
	QueuePriorities map[string]int
}

const (
	defaultHost          = "localhost"
	defaultPort          = 6379
	defaultDB            = 0
	defaultWorkers       = 10
	defaultRetryInterval = 5 * time.Second
	defaultMaxRetries    = 3
	defaultRetention     = 7 * 24 * time.Hour
	minPort              = 1
	maxPort              = 65535
	minDB                = 0
	maxDB                = 15
	minWorkers           = 1
	maxWorkers           = 100
	minRetryInterval     = time.Second
	maxRetryInterval     = time.Hour
	minMaxRetries        = 1
	maxMaxRetries        = 10
	minRetentionDays     = 1
	maxRetentionDays     = 365
)

// DefaultQueuePriorities defines the default priority settings for task queues
var DefaultQueuePriorities = map[string]int{
	"critical": 6,
	"default":  3,
	"low":      1,
}

// NewRedisConfig creates a new Redis configuration from environment variables
func NewRedisConfig() (*RedisConfig, error) {
	cfg := &RedisConfig{
		Host:            getEnvOrDefault("REDIS_HOST", defaultHost),
		Password:        os.Getenv("REDIS_PASSWORD"),
		UseTLS:          getEnvBool("REDIS_USE_TLS"),
		CertFile:        os.Getenv("REDIS_CERT_FILE"),
		KeyFile:         os.Getenv("REDIS_KEY_FILE"),
		CAFile:          os.Getenv("REDIS_CA_FILE"),
		QueuePriorities: make(map[string]int),
	}

	// Initialize queue priorities with defaults
	for queue, priority := range DefaultQueuePriorities {
		cfg.QueuePriorities[queue] = priority
	}

	// Check if Redis URL is provided
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		parsedURL, err := url.Parse(redisURL)
		if err != nil {
			return nil, fmt.Errorf("invalid Redis URL: %w", err)
		}

		// Parse host and port
		host := parsedURL.Hostname()
		if host != "" {
			cfg.Host = host
		}

		if port := parsedURL.Port(); port != "" {
			p, err := strconv.Atoi(port)
			if err != nil {
				return nil, fmt.Errorf("invalid port in Redis URL: %w", err)
			}
			cfg.Port = p
		} else {
			cfg.Port = defaultPort
		}

		// Parse password from URL
		if password, ok := parsedURL.User.Password(); ok {
			cfg.Password = password
		}

		// Parse database number from path
		if path := parsedURL.Path; path != "" {
			path = strings.TrimPrefix(path, "/")
			if path != "" {
				db, err := strconv.Atoi(path)
				if err != nil {
					return nil, fmt.Errorf("invalid database number in Redis URL: %w", err)
				}
				cfg.DB = db
			}
		}

		return cfg, nil
	}

	// If no Redis URL is provided, use individual configuration parameters
	// Validate and set Port
	if port, err := validatePort(getEnvOrDefault("REDIS_PORT", strconv.Itoa(defaultPort))); err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	} else {
		cfg.Port = port
	}

	// Validate and set DB
	if db, err := validateDB(getEnvOrDefault("REDIS_DB", strconv.Itoa(defaultDB))); err != nil {
		return nil, fmt.Errorf("invalid DB: %w", err)
	} else {
		cfg.DB = db
	}

	// Validate and set Workers
	if workers, err := validateWorkers(getEnvOrDefault("REDIS_WORKERS", strconv.Itoa(defaultWorkers))); err != nil {
		return nil, fmt.Errorf("invalid workers: %w", err)
	} else {
		cfg.Workers = workers
	}

	// Validate and set RetryInterval
	if interval, err := validateRetryInterval(getEnvOrDefault("REDIS_RETRY_INTERVAL", defaultRetryInterval.String())); err != nil {
		return nil, fmt.Errorf("invalid retry interval: %w", err)
	} else {
		cfg.RetryInterval = interval
	}

	// Validate and set MaxRetries
	if retries, err := validateMaxRetries(getEnvOrDefault("REDIS_MAX_RETRIES", strconv.Itoa(defaultMaxRetries))); err != nil {
		return nil, fmt.Errorf("invalid max retries: %w", err)
	} else {
		cfg.MaxRetries = retries
	}

	// Validate and set RetentionPeriod
	if days, err := validateRetentionDays(getEnvOrDefault("REDIS_RETENTION_DAYS", "7")); err != nil {
		return nil, fmt.Errorf("invalid retention days: %w", err)
	} else {
		cfg.RetentionPeriod = time.Duration(days) * 24 * time.Hour
	}

	// Validate TLS configuration if enabled
	if cfg.UseTLS {
		// Skip TLS file validation in test mode
		if !isTestMode() {
			if err := validateTLSConfig(cfg); err != nil {
				return nil, fmt.Errorf("invalid TLS configuration: %w", err)
			}
		}
	}

	return cfg, nil
}

// GetRedisAddr returns the formatted Redis address
func (c *RedisConfig) GetRedisAddr() string {
	host := c.Host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, c.Port)
}

func validatePort(port string) (int, error) {
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("port must be a number: %w", err)
	}
	if p < minPort || p > maxPort {
		return 0, fmt.Errorf("port must be between %d and %d", minPort, maxPort)
	}
	return p, nil
}

func validateDB(db string) (int, error) {
	d, err := strconv.Atoi(db)
	if err != nil {
		return 0, fmt.Errorf("DB must be a number: %w", err)
	}
	if d < minDB || d > maxDB {
		return 0, fmt.Errorf("DB must be between %d and %d", minDB, maxDB)
	}
	return d, nil
}

func validateWorkers(workers string) (int, error) {
	w, err := strconv.Atoi(workers)
	if err != nil {
		return 0, fmt.Errorf("workers must be a number: %w", err)
	}
	if w < minWorkers || w > maxWorkers {
		return 0, fmt.Errorf("workers must be between %d and %d", minWorkers, maxWorkers)
	}
	return w, nil
}

func validateRetryInterval(interval string) (time.Duration, error) {
	d, err := time.ParseDuration(interval)
	if err != nil {
		return 0, fmt.Errorf("invalid duration format: %w", err)
	}
	if d < minRetryInterval || d > maxRetryInterval {
		return 0, fmt.Errorf("retry interval must be between %v and %v", minRetryInterval, maxRetryInterval)
	}
	return d, nil
}

func validateMaxRetries(retries string) (int, error) {
	r, err := strconv.Atoi(retries)
	if err != nil {
		return 0, fmt.Errorf("max retries must be a number: %w", err)
	}
	if r < minMaxRetries || r > maxMaxRetries {
		return 0, fmt.Errorf("max retries must be between %d and %d", minMaxRetries, maxMaxRetries)
	}
	return r, nil
}

func validateRetentionDays(days string) (int, error) {
	d, err := strconv.Atoi(days)
	if err != nil {
		return 0, fmt.Errorf("retention days must be a number: %w", err)
	}
	if d < minRetentionDays || d > maxRetentionDays {
		return 0, fmt.Errorf("retention days must be between %d and %d", minRetentionDays, maxRetentionDays)
	}
	return d, nil
}

func validateTLSConfig(cfg *RedisConfig) error {
	if cfg.CertFile == "" {
		return fmt.Errorf("TLS certificate file is required when TLS is enabled")
	}
	if cfg.KeyFile == "" {
		return fmt.Errorf("TLS key file is required when TLS is enabled")
	}

	// Check if files exist and are readable
	if err := checkFileReadable(cfg.CertFile); err != nil {
		return fmt.Errorf("certificate file error: %w", err)
	}
	if err := checkFileReadable(cfg.KeyFile); err != nil {
		return fmt.Errorf("key file error: %w", err)
	}
	if cfg.CAFile != "" {
		if err := checkFileReadable(cfg.CAFile); err != nil {
			return fmt.Errorf("CA file error: %w", err)
		}
	}

	return nil
}

func checkFileReadable(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %s", path)
		}
		return fmt.Errorf("cannot access file: %s: %w", path, err)
	}
	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string) bool {
	value := strings.ToLower(os.Getenv(key))
	return value == "true" || value == "1" || value == "yes"
}

// isTestMode returns true if the code is running in test mode
func isTestMode() bool {
	return strings.HasSuffix(os.Args[0], ".test") || os.Getenv("GO_TEST") == "1"
}
