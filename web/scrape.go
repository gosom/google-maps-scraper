package web

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds application configuration
type Config struct {
	// Database
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// Full DSN string (overrides individual DB settings if provided)
	DatabaseURL string

	// Server
	ServerPort string
	DataFolder string

	// Authentication
	ClerkAPIKey string

	// AWS Lambda
	AWSRegion             string
	AWSLambdaFunctionName string
	AWSAccessKeyID        string
	AWSSecretAccessKey    string

	// S3
	S3BucketName string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() Config {
	// Load .env file if it exists
	_ = godotenv.Load()

	return Config{
		// Database
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "scraper"),
		DBPassword: getEnv("DB_PASSWORD", "strongpassword"),
		DBName:     getEnv("DB_NAME", "google_maps_scraper"),
		DBSSLMode:  getEnv("DB_SSL_MODE", "disable"),

		// Full DSN
		DatabaseURL: getEnv("DATABASE_URL", ""),

		// Server
		ServerPort: getEnv("SERVER_PORT", "8080"),
		DataFolder: getEnv("DATA_FOLDER", "./webdata"),

		// Authentication
		ClerkAPIKey: getEnv("CLERK_API_KEY", ""),

		// AWS Lambda
		AWSRegion:             getEnv("AWS_REGION", ""),
		AWSLambdaFunctionName: getEnv("AWS_LAMBDA_FUNCTION_NAME", ""),
		AWSAccessKeyID:        getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey:    getEnv("AWS_SECRET_ACCESS_KEY", ""),

		// S3
		S3BucketName: getEnv("S3_BUCKET_NAME", ""),
	}
}

// GetDBConnectionString returns PostgreSQL connection string
func (c *Config) GetDBConnectionString() string {
	// If a full DSN is provided, use it
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}

	// Otherwise build from individual components
	return "host=" + c.DBHost +
		" port=" + c.DBPort +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + c.DBSSLMode
}

// getEnv reads an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}
