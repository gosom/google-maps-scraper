//go:build plugin
// +build plugin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
)

type GoogleSheetsClient struct {
	webhookURL string
	sheetName  string
	httpClient *http.Client
}

type WebhookPayload struct {
	Sheet  string      `json:"sheet"`
	Schema []string    `json:"schema"`
	Entry  interface{} `json:"entry"`
}

func NewGoogleSheetsClient() (*GoogleSheetsClient, error) {
	// Load .env file if it exists
	if err := loadEnvFile(); err != nil {
		// Not a fatal error, just log it
		fmt.Printf("Warning: could not load .env file: %v\n", err)
	}

	webhookURL := os.Getenv("URL_WEBHOOK")
	if webhookURL == "" {
		return nil, fmt.Errorf("URL_WEBHOOK environment variable is required")
	}

	sheetName := os.Getenv("SHEET_NAME")
	if sheetName == "" {
		sheetName = "scraping" // default value
	}

	return &GoogleSheetsClient{
		webhookURL: webhookURL,
		sheetName:  sheetName,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// loadEnvFile loads environment variables from .env file if it exists
func loadEnvFile() error {
	// Try to find .env file in current directory or parent directories
	envPath := findEnvFile()
	if envPath == "" {
		return fmt.Errorf(".env file not found")
	}

	file, err := os.Open(envPath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Only set if not already set (environment variables take precedence)
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}

// findEnvFile searches for .env file in current directory and parent directories
func findEnvFile() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}

	dir := wd
	for {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}

func (c *GoogleSheetsClient) SendEntry(ctx context.Context, entry *gmaps.Entry) error {
	// Transform entry to match Google Apps Script expected format
	transformedEntry := transformEntry(entry)

	payload := WebhookPayload{
		Sheet:  c.sheetName,
		Schema: getDefaultSchema(),
		Entry:  transformedEntry,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}