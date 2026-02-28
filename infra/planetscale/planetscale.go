// Package planetscale provides database provisioning via the PlanetScale API.
// Note: PlanetScale provides MySQL-compatible databases.
package planetscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gosom/google-maps-scraper/infra"
)

const apiBase = "https://api.planetscale.com/v1"

// Client interacts with the PlanetScale API.
type Client struct {
	token      string
	org        string
	httpClient *http.Client
}

// New creates a new PlanetScale client.
func New(token, org string) *Client {
	return &Client{
		token:      token,
		org:        org,
		httpClient: http.DefaultClient,
	}
}

// CheckConnectivity validates the API token by listing databases.
func (c *Client) CheckConnectivity(ctx context.Context) error {
	url := fmt.Sprintf("%s/organizations/%s/databases", apiBase, c.org)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("PlanetScale API error: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PlanetScale API returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// CreateDatabase creates a new PlanetScale database and returns a connection URL.
func (c *Client) CreateDatabase(ctx context.Context, name string) (*infra.DatabaseInfo, error) {
	// Step 1: Create the database
	dbURL := fmt.Sprintf("%s/organizations/%s/databases", apiBase, c.org)
	body, _ := json.Marshal(map[string]string{"name": name})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dbURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create database (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// Step 2: Create a password for the main branch
	pwURL := fmt.Sprintf("%s/organizations/%s/databases/%s/branches/main/passwords", apiBase, c.org, name)
	pwBody, _ := json.Marshal(map[string]string{"role": "readwriter"})

	pwReq, err := http.NewRequestWithContext(ctx, http.MethodPost, pwURL, bytes.NewReader(pwBody))
	if err != nil {
		return nil, err
	}

	pwResp, err := c.do(pwReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create database password: %w", err)
	}

	defer func() { _ = pwResp.Body.Close() }()

	if pwResp.StatusCode != http.StatusCreated && pwResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(pwResp.Body)
		return nil, fmt.Errorf("failed to create password (HTTP %d): %s", pwResp.StatusCode, string(respBody))
	}

	var pwResult struct {
		PlainText string `json:"plain_text"`
		Username  string `json:"username"`
		Hostname  string `json:"hostname"`
	}

	if err := json.NewDecoder(pwResp.Body).Decode(&pwResult); err != nil {
		return nil, fmt.Errorf("failed to parse password response: %w", err)
	}

	// Construct MySQL connection URL
	connURL := fmt.Sprintf("mysql://%s:%s@%s/%s?ssl=true",
		pwResult.Username, pwResult.PlainText, pwResult.Hostname, name)

	return &infra.DatabaseInfo{
		ConnectionURL: connURL,
	}, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return c.httpClient.Do(req)
}
