package web

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds application configuration
type Config struct {
	// Server
	ServerPort string
	DataFolder string

	// Authentication
	ClerkSecretKey string

	// AWS Lambda
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string

	// S3
	S3BucketName string
}

// LoadConfig loads configuration from environment variables and an injected dataFolder.
//
// dataFolder is sourced from the typed pkg/config.Config (appCfg.DataFolder)
// by the caller, so this function does not read DATA_FOLDER directly. Other
// env reads here remain pending the 2026-04-27 env-config consolidation plan.
//
// Note: this function currently has zero callers in the repo. The struct
// and constructor are vestigial and should be deleted in a follow-up PR.
func LoadConfig(dataFolder string) Config {
	// Load .env file if it exists
	_ = godotenv.Load()

	return Config{
		// Server
		ServerPort: getEnv("SERVER_PORT", "8080"),
		DataFolder: dataFolder,

		// Authentication
		ClerkSecretKey: getEnv("CLERK_SECRET_KEY", ""),

		// AWS Lambda
		AWSRegion:          getEnv("AWS_REGION", ""),
		AWSAccessKeyID:     getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: getEnv("AWS_SECRET_ACCESS_KEY", ""),

		// S3
		S3BucketName: getEnv("S3_BUCKET_NAME", ""),
	}
}

// getEnv reads an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}
