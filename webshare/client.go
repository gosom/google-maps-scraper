package webshare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	baseURL = "https://proxy.webshare.io/api/v2"
)

// Client represents a Webshare API client
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Webshare API client
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// doRequest performs an HTTP request with proper authentication
func (c *Client) doRequest(method, endpoint string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, baseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// GetMyIP retrieves the current public IP address
func (c *Client) GetMyIP() (string, error) {
	log.Println("🔍 Fetching current IP from Webshare API...")

	respBody, err := c.doRequest("GET", "/proxy/ipauthorization/whatsmyip/", nil)
	if err != nil {
		return "", fmt.Errorf("failed to get my IP: %w", err)
	}

	var response WhatsMyIPResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("failed to parse IP response: %w", err)
	}

	log.Printf("✅ Current IP detected: %s", response.IPAddress)
	return response.IPAddress, nil
}

// ListIPAuthorizations lists all authorized IPs
func (c *Client) ListIPAuthorizations() ([]IPAuthorization, error) {
	log.Println("📋 Fetching authorized IPs from Webshare...")

	respBody, err := c.doRequest("GET", "/proxy/ipauthorization/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list IP authorizations: %w", err)
	}

	var response IPAuthorizationListResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse IP authorization list: %w", err)
	}

	log.Printf("✅ Found %d authorized IPs", len(response.Results))
	return response.Results, nil
}

// IsIPAuthorized checks if a specific IP is already authorized
func (c *Client) IsIPAuthorized(ipAddress string) (bool, error) {
	authorizedIPs, err := c.ListIPAuthorizations()
	if err != nil {
		return false, err
	}

	for _, auth := range authorizedIPs {
		if auth.IPAddress == ipAddress {
			log.Printf("✅ IP %s is already authorized (ID: %d)", ipAddress, auth.ID)
			return true, nil
		}
	}

	log.Printf("⚠️ IP %s is NOT authorized", ipAddress)
	return false, nil
}

// AddIPAuthorization adds a new IP authorization
func (c *Client) AddIPAuthorization(ipAddress string) error {
	log.Printf("➕ Adding IP authorization for %s...", ipAddress)

	requestBody := map[string]string{
		"ip_address": ipAddress,
	}

	respBody, err := c.doRequest("POST", "/proxy/ipauthorization/", requestBody)
	if err != nil {
		return fmt.Errorf("failed to add IP authorization: %w", err)
	}

	var response IPAuthorization
	if err := json.Unmarshal(respBody, &response); err != nil {
		return fmt.Errorf("failed to parse IP authorization response: %w", err)
	}

	log.Printf("✅ Successfully authorized IP %s (ID: %d)", response.IPAddress, response.ID)
	return nil
}

// EnsureIPAuthorized ensures the current IP is authorized, adding it if necessary
func (c *Client) EnsureIPAuthorized() error {
	// Get current IP
	currentIP, err := c.GetMyIP()
	if err != nil {
		return fmt.Errorf("failed to get current IP: %w", err)
	}

	// Check if already authorized
	isAuthorized, err := c.IsIPAuthorized(currentIP)
	if err != nil {
		return fmt.Errorf("failed to check IP authorization: %w", err)
	}

	if isAuthorized {
		return nil // Already authorized
	}

	// Add authorization
	if err := c.AddIPAuthorization(currentIP); err != nil {
		return fmt.Errorf("failed to authorize IP: %w", err)
	}

	return nil
}

// GetProxyList retrieves all available proxies from Webshare
func (c *Client) GetProxyList(mode string) ([]Proxy, error) {
	if mode == "" {
		mode = "direct"
	}

	log.Printf("🔄 Fetching proxy list from Webshare (mode: %s)...", mode)

	allProxies := []Proxy{}
	page := 1
	pageSize := 100 // Fetch 100 proxies per page

	for {
		endpoint := fmt.Sprintf("/proxy/list/?mode=%s&page=%d&page_size=%d", mode, page, pageSize)
		respBody, err := c.doRequest("GET", endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get proxy list (page %d): %w", page, err)
		}

		var response ProxyListResponse
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to parse proxy list response: %w", err)
		}

		allProxies = append(allProxies, response.Results...)
		log.Printf("📦 Fetched page %d: %d proxies (total: %d/%d)", page, len(response.Results), len(allProxies), response.Count)

		// Check if there are more pages
		if response.Next == nil {
			break
		}

		page++
	}

	log.Printf("✅ Successfully fetched %d proxies from Webshare", len(allProxies))
	return allProxies, nil
}

// FormatProxiesForScraper converts Webshare proxies to URL strings
func FormatProxiesForScraper(proxies []Proxy) []string {
	proxyURLs := make([]string, 0, len(proxies))

	for _, proxy := range proxies {
		// Only include valid proxies
		if !proxy.Valid {
			log.Printf("⚠️ Skipping invalid proxy: %s:%d", proxy.ProxyAddress, proxy.Port)
			continue
		}

		// Format: http://username:password@host:port
		proxyURL := fmt.Sprintf("http://%s:%s@%s:%d",
			proxy.Username,
			proxy.Password,
			proxy.ProxyAddress,
			proxy.Port,
		)
		proxyURLs = append(proxyURLs, proxyURL)
	}

	log.Printf("✅ Formatted %d valid proxies for scraper", len(proxyURLs))
	return proxyURLs
}

// GetProxiesForScraper is a convenience function that fetches and formats proxies
func (c *Client) GetProxiesForScraper(mode string) ([]string, error) {
	proxies, err := c.GetProxyList(mode)
	if err != nil {
		return nil, err
	}

	return FormatProxiesForScraper(proxies), nil
}
