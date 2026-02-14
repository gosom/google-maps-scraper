//go:build integration

package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

const (
	berlinWeddingCafeKeyword = "Berlin wedding cafe"
	unlimitedMaxResults      = 0
	unlimitedReviewsMax      = 9999
)

var (
	flagRunE2E          = flag.Bool("braza-run-scrape-e2e", false, "run live Google Maps scrape E2E tests (requires a running API)")
	flagBaseURL         = flag.String("braza-api-base-url", "", "API base URL (default http://localhost:8080)")
	flagDevUserID       = flag.String("braza-dev-user-id", "", "dev auth bypass user_id (server must run with BRAZA_DEV_AUTH_BYPASS=1)")
	flagKeepJobs        = flag.Bool("braza-keep-jobs", false, "keep jobs after tests (no delete cleanup)")
	flagIncludeFastMode = flag.Bool("braza-include-fast-mode", false, "include fast mode scenarios in the matrix")
)

type scrapeScenario struct {
	name            string
	jobData         map[string]any
	expectedData    map[string]any
	minResults      int
	assertMaxResult bool
	expectImages    bool
	expectReviews   bool // user_reviews_extended
}

type e2eConfig struct {
	baseURL             string
	bearerToken         string
	sessionCookie       string
	clerkSecretKey      string
	clerkSessionID      string
	devUserID           string
	keepJobs            bool
	pollInterval        time.Duration
	jobTimeout          time.Duration
	pendingTimeout      time.Duration
	zeroResultsTimeout  time.Duration
	progressStall       time.Duration
	maxResultsGraceTime time.Duration
}

type apiE2EClient struct {
	t          *testing.T
	httpClient *http.Client
	cfg        e2eConfig

	tokenMu    sync.Mutex
	tokenCache cachedToken
}

type cachedToken struct {
	token  string
	expiry time.Time
}

type paginatedResultsResponse struct {
	Results    []map[string]any `json:"results"`
	TotalCount int              `json:"total_count"`
	Page       int              `json:"page"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
	TotalPages int              `json:"total_pages"`
	HasNext    bool             `json:"has_next"`
	HasPrev    bool             `json:"has_prev"`
}

type csvValidation struct {
	HeaderColumns          []string
	TotalDataRows          int
	RowsWithTitleAndLink   int
	RowsWithNonEmptyImages int
	// RowsWithNonEmptyReviewsExtended counts rows with user_reviews_extended != []/null/empty.
	RowsWithNonEmptyReviewsExtended int
	RequiredColumnsPresent          bool
}

func TestAPIJobs_ScrapeParameterMatrix(t *testing.T) {
	cfg, ok := loadE2EConfig(t)
	if !ok {
		return
	}

	client := &apiE2EClient{
		t: t,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		cfg: cfg,
	}

	scenarios := []scrapeScenario{
		{
			name: "happy_path_default_depth_minimal_payload",
			jobData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"lang":        "de",
				"depth":       5,
				"max_time":    420,
				"reviews_max": 0,
				"max_results": 10,
			},
			expectedData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"depth":       5,
				"images":      false,
				"reviews_max": 0,
				"max_results": 10,
			},
			minResults:      1,
			assertMaxResult: true,
			expectImages:    false,
			expectReviews:   false,
		},
		{
			name: "images_enabled_small_run",
			jobData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"lang":        "de",
				"depth":       5,
				"images":      true,
				"max_time":    480,
				"reviews_max": 5,
				"max_results": 5,
			},
			expectedData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"depth":       5,
				"images":      true,
				"reviews_max": 5,
				"max_results": 5,
			},
			minResults:      1,
			assertMaxResult: true,
			expectImages:    true,
			expectReviews:   true,
		},
		{
			name: "unlimited_results_images_no_reviews_depth6",
			jobData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"lang":        "de",
				"depth":       6,
				"images":      true,
				"max_time":    900,
				"reviews_max": 0,
				"max_results": unlimitedMaxResults,
			},
			expectedData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"depth":       6,
				"images":      true,
				"reviews_max": 0,
				"max_results": unlimitedMaxResults,
			},
			minResults:      1,
			assertMaxResult: false,
			expectImages:    true,
			expectReviews:   false,
		},
		{
			name: "unlimited_results_images_reviews_depth5",
			jobData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"lang":        "de",
				"depth":       5,
				"images":      true,
				"max_time":    900,
				"reviews_max": unlimitedReviewsMax,
				"max_results": unlimitedMaxResults,
			},
			expectedData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"depth":       5,
				"images":      true,
				"reviews_max": unlimitedReviewsMax,
				"max_results": unlimitedMaxResults,
			},
			minResults:      1,
			assertMaxResult: false,
			expectImages:    true,
			expectReviews:   true,
		},
		{
			name: "unlimited_results_images_reviews_depth6",
			jobData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"lang":        "de",
				"depth":       6,
				"images":      true,
				"max_time":    900,
				"reviews_max": unlimitedReviewsMax,
				"max_results": unlimitedMaxResults,
			},
			expectedData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"depth":       6,
				"images":      true,
				"reviews_max": unlimitedReviewsMax,
				"max_results": unlimitedMaxResults,
			},
			minResults:      1,
			assertMaxResult: false,
			expectImages:    true,
			expectReviews:   true,
		},
		{
			name: "unlimited_results_images_reviews_depth7",
			jobData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"lang":        "de",
				"depth":       7,
				"images":      true,
				"max_time":    900,
				"reviews_max": unlimitedReviewsMax,
				"max_results": unlimitedMaxResults,
			},
			expectedData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"depth":       7,
				"images":      true,
				"reviews_max": unlimitedReviewsMax,
				"max_results": unlimitedMaxResults,
			},
			minResults:      1,
			assertMaxResult: false,
			expectImages:    true,
			expectReviews:   true,
		},
	}

	// Keep fast-mode tests opt-in; they are much more brittle and are not part of
	// the default Playwright (non-fast-mode) coverage.
	if strings.TrimSpace(os.Getenv("BRAZA_INCLUDE_FAST_MODE")) == "1" || (*flagIncludeFastMode) {
		scenarios = append(scenarios, scrapeScenario{
			name: "fast_mode_with_coordinates",
			jobData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"lang":        "de",
				"depth":       1,
				"images":      false,
				"fast_mode":   true,
				"lat":         "52.5476",
				"lon":         "13.3656",
				"zoom":        14,
				"radius":      5000,
				"max_time":    420,
				"reviews_max": 0,
				"max_results": 8,
			},
			expectedData: map[string]any{
				"keywords":    []string{berlinWeddingCafeKeyword},
				"images":      false,
				"fast_mode":   true,
				"lat":         "52.5476",
				"lon":         "13.3656",
				"zoom":        14,
				"radius":      5000,
				"max_results": 8,
			},
			minResults:      1,
			assertMaxResult: true,
			expectImages:    false,
			expectReviews:   false,
		})
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			scenarioStart := time.Now()

			jobName := fmt.Sprintf("%s-%d", scenario.name, time.Now().UnixNano())
			jobID, err := client.createJob(jobName, scenario.jobData)
			if err != nil {
				t.Fatalf("failed to create job: %v", err)
			}

			if !cfg.keepJobs {
				t.Cleanup(func() {
					if delErr := client.deleteJob(jobID); delErr != nil {
						t.Logf("cleanup warning: failed to delete job %s: %v", jobID, delErr)
					}
				})
			} else {
				t.Logf("BRAZA_KEEP_JOBS=1, preserving job %s", jobID)
			}

			maxResults, err := scenarioMaxResults(scenario)
			if err != nil {
				t.Fatalf("invalid scenario max_results: %v", err)
			}

			t.Logf("created job %s (%s)", jobID, scenario.name)
			finalJob, finalObservedResults, err := client.waitForSuccessfulCompletion(jobID, maxResults)
			if err != nil {
				t.Fatalf("job %s failed lifecycle checks: %v", jobID, err)
			}

			finalStatus := extractJobStatus(finalJob)
			if finalStatus != models.StatusOK {
				t.Fatalf("expected final status ok, got %q", finalStatus)
			}

			if reason := extractFailureReason(finalJob); reason != "" {
				t.Fatalf("expected empty failure reason for successful job, got %q", reason)
			}

			jobData, err := getMap(finalJob, "Data", "data")
			if err != nil {
				t.Fatalf("job %s missing Data payload: %v", jobID, err)
			}
			assertMapContains(t, jobData, scenario.expectedData)

			maxTimeRaw, ok := jobData["max_time"]
			if !ok {
				t.Fatalf("job %s data missing max_time", jobID)
			}
			maxTimeNanos, err := int64FromAny(maxTimeRaw)
			if err != nil || maxTimeNanos <= 0 {
				t.Fatalf("job %s invalid max_time=%v err=%v", jobID, maxTimeRaw, err)
			}

			resultsPage, err := client.getResultsPage(jobID, 100, 1)
			if err != nil {
				t.Fatalf("failed to fetch results page for job %s: %v", jobID, err)
			}

			assertResultsQuality(t, resultsPage, scenario.minResults, scenario.expectImages, scenario.expectReviews)

			if scenario.assertMaxResult && maxResults > 0 && resultsPage.TotalCount > maxResults {
				t.Fatalf("expected total results <= max_results (%d), got %d", maxResults, resultsPage.TotalCount)
			}

			csvBytes, err := client.downloadJobCSV(jobID)
			if err != nil {
				t.Fatalf("failed to download CSV for job %s: %v", jobID, err)
			}

			csvCheck, err := validateCSVContent(csvBytes)
			if err != nil {
				t.Fatalf("job %s invalid CSV content: %v", jobID, err)
			}

			if !csvCheck.RequiredColumnsPresent {
				t.Fatalf("job %s CSV missing required columns", jobID)
			}
			if csvCheck.TotalDataRows < scenario.minResults {
				t.Fatalf("job %s CSV has too few rows: want at least %d, got %d", jobID, scenario.minResults, csvCheck.TotalDataRows)
			}
			if csvCheck.RowsWithTitleAndLink == 0 {
				t.Fatalf("job %s CSV has no rows with both title and link", jobID)
			}
			if scenario.expectImages && csvCheck.RowsWithNonEmptyImages == 0 {
				t.Fatalf("job %s expected images in CSV but none of the rows had a non-empty images payload", jobID)
			}
			if scenario.expectReviews && csvCheck.RowsWithNonEmptyReviewsExtended == 0 {
				t.Fatalf("job %s expected user_reviews_extended in CSV but none of the rows had a non-empty payload", jobID)
			}

			if csvCheck.TotalDataRows != resultsPage.TotalCount {
				t.Fatalf("job %s CSV/database mismatch: csv_rows=%d results_total=%d", jobID, csvCheck.TotalDataRows, resultsPage.TotalCount)
			}

			duration := time.Since(scenarioStart).Round(time.Second)
			t.Logf(
				"scenario_summary name=%s job_id=%s duration=%s depth=%v images=%v reviews_max=%v max_results=%v max_time=%v api_results=%d csv_rows=%d csv_rows_images=%d csv_rows_reviews_extended=%d",
				scenario.name,
				jobID,
				duration,
				scenario.jobData["depth"],
				scenario.jobData["images"],
				scenario.jobData["reviews_max"],
				scenario.jobData["max_results"],
				scenario.jobData["max_time"],
				resultsPage.TotalCount,
				csvCheck.TotalDataRows,
				csvCheck.RowsWithNonEmptyImages,
				csvCheck.RowsWithNonEmptyReviewsExtended,
			)

			t.Logf(
				"job %s completed successfully; duration=%s final_status=%s observed_results=%d api_results=%d csv_rows=%d",
				jobID,
				duration,
				finalStatus,
				finalObservedResults,
				resultsPage.TotalCount,
				csvCheck.TotalDataRows,
			)
		})
	}
}

func loadE2EConfig(t *testing.T) (e2eConfig, bool) {
	t.Helper()

	runRequested := strings.TrimSpace(os.Getenv("BRAZA_RUN_SCRAPE_E2E")) == "1" || (*flagRunE2E)
	if !runRequested {
		t.Skip("set BRAZA_RUN_SCRAPE_E2E=1 or pass -args -braza-run-scrape-e2e to run scrape integration tests")
		return e2eConfig{}, false
	}

	baseURL := strings.TrimSpace(envOrDefaultE2E("BRAZA_API_BASE_URL", "http://localhost:8080"))
	if strings.TrimSpace(*flagBaseURL) != "" {
		baseURL = strings.TrimSpace(*flagBaseURL)
	}

	cfg := e2eConfig{
		baseURL:             strings.TrimRight(baseURL, "/"),
		bearerToken:         strings.TrimSpace(os.Getenv("BRAZA_AUTH_TOKEN")),
		sessionCookie:       strings.TrimSpace(os.Getenv("BRAZA_SESSION_COOKIE")),
		clerkSecretKey:      strings.TrimSpace(envOrDefaultE2E("BRAZA_CLERK_SECRET_KEY", os.Getenv("CLERK_SECRET_KEY"))),
		clerkSessionID:      strings.TrimSpace(os.Getenv("BRAZA_CLERK_SESSION_ID")),
		devUserID:           strings.TrimSpace(os.Getenv("BRAZA_DEV_USER_ID")),
		keepJobs:            strings.TrimSpace(os.Getenv("BRAZA_KEEP_JOBS")) == "1" || (*flagKeepJobs),
		pollInterval:        parseDurationEnv(t, "BRAZA_POLL_INTERVAL", 10*time.Second),
		jobTimeout:          parseDurationEnv(t, "BRAZA_JOB_TIMEOUT", 35*time.Minute),
		pendingTimeout:      parseDurationEnv(t, "BRAZA_PENDING_TIMEOUT", 3*time.Minute),
		zeroResultsTimeout:  parseDurationEnv(t, "BRAZA_ZERO_RESULTS_TIMEOUT", 12*time.Minute),
		progressStall:       parseDurationEnv(t, "BRAZA_PROGRESS_STALL_TIMEOUT", 8*time.Minute),
		maxResultsGraceTime: parseDurationEnv(t, "BRAZA_MAX_RESULTS_GRACE_TIMEOUT", 4*time.Minute),
	}

	cfg.bearerToken = normalizeBearerToken(cfg.bearerToken)
	cfg.sessionCookie = normalizeSessionCookie(cfg.sessionCookie)

	if strings.TrimSpace(*flagDevUserID) != "" {
		cfg.devUserID = strings.TrimSpace(*flagDevUserID)
	}

	// Dev auth bypass mode does not require any token/cookie as long as the server
	// was started with BRAZA_DEV_AUTH_BYPASS=1.
	if cfg.devUserID != "" {
		return cfg, true
	}

	if cfg.clerkSessionID == "" && cfg.bearerToken != "" {
		if sid, err := extractSessionIDFromJWT(cfg.bearerToken); err == nil {
			cfg.clerkSessionID = sid
		}
	}
	if cfg.clerkSessionID == "" && cfg.sessionCookie != "" {
		// Clerk's __session cookie is also a JWT with a sid claim.
		if sid, err := extractSessionIDFromJWT(cfg.sessionCookie); err == nil {
			cfg.clerkSessionID = sid
		}
	}

	canRefreshFromClerk := cfg.clerkSecretKey != "" && cfg.clerkSessionID != ""
	if cfg.bearerToken == "" && cfg.sessionCookie == "" && !canRefreshFromClerk {
		t.Skip("set BRAZA_DEV_USER_ID (server must run with BRAZA_DEV_AUTH_BYPASS=1) or set BRAZA_AUTH_TOKEN or BRAZA_SESSION_COOKIE or BRAZA_CLERK_SECRET_KEY+BRAZA_CLERK_SESSION_ID for authenticated API calls")
		return e2eConfig{}, false
	}

	return cfg, true
}

func normalizeBearerToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}

	lower := strings.ToLower(token)
	if strings.HasPrefix(lower, "bearer ") {
		return strings.TrimSpace(token[len("bearer "):])
	}
	return token
}

func normalizeSessionCookie(cookie string) string {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return ""
	}

	// If someone pasted the full cookie string (e.g. "__session=...; other=..."),
	// extract the actual __session value.
	if strings.Contains(cookie, "__session=") {
		parts := strings.Split(cookie, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "__session=") {
				return strings.TrimSpace(strings.TrimPrefix(part, "__session="))
			}
		}
	}

	return cookie
}

func parseDurationEnv(t *testing.T, key string, fallback time.Duration) time.Duration {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		t.Logf("invalid %s=%q, using fallback %s", key, raw, fallback)
		return fallback
	}
	return d
}

func envOrDefaultE2E(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func scenarioMaxResults(s scrapeScenario) (int, error) {
	v, ok := s.jobData["max_results"]
	if !ok {
		return 0, fmt.Errorf("scenario %q missing max_results", s.name)
	}
	return intFromAny(v)
}

func (c *apiE2EClient) createJob(name string, jobData map[string]any) (string, error) {
	payload := make(map[string]any, len(jobData)+1)
	payload["name"] = name
	for k, v := range jobData {
		payload[k] = v
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(http.MethodPost, "/api/v1/jobs", payload, http.StatusCreated, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.ID) == "" {
		return "", fmt.Errorf("empty job id returned by API")
	}
	return resp.ID, nil
}

func (c *apiE2EClient) deleteJob(jobID string) error {
	path := fmt.Sprintf("/api/v1/jobs/%s", url.PathEscape(jobID))
	return c.doJSON(http.MethodDelete, path, nil, http.StatusOK, nil)
}

func (c *apiE2EClient) getJob(jobID string) (map[string]any, error) {
	path := fmt.Sprintf("/api/v1/jobs/%s", url.PathEscape(jobID))
	var resp map[string]any
	if err := c.doJSON(http.MethodGet, path, nil, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *apiE2EClient) getResultsPage(jobID string, limit, page int) (paginatedResultsResponse, error) {
	path := fmt.Sprintf("/api/v1/jobs/%s/results?limit=%d&page=%d", url.PathEscape(jobID), limit, page)
	var resp paginatedResultsResponse
	if err := c.doJSON(http.MethodGet, path, nil, http.StatusOK, &resp); err != nil {
		return paginatedResultsResponse{}, err
	}
	return resp, nil
}

func (c *apiE2EClient) downloadJobCSV(jobID string) ([]byte, error) {
	path := fmt.Sprintf("/api/v1/jobs/%s/download", url.PathEscape(jobID))
	body, contentType, err := c.doBytes(http.MethodGet, path, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(strings.ToLower(contentType), "text/csv") {
		return nil, fmt.Errorf("unexpected content-type %q", contentType)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("downloaded CSV is empty")
	}
	return body, nil
}

func (c *apiE2EClient) waitForSuccessfulCompletion(jobID string, maxResults int) (map[string]any, int, error) {
	start := time.Now()
	deadline := start.Add(c.cfg.jobTimeout)

	var pendingStartedAt time.Time
	var workingStartedAt time.Time
	var reachedMaxResultsAt time.Time

	lastStatus := ""
	lastFailureReason := ""
	lastResultsCount := -1
	lastResultsChangeAt := start

	for time.Now().Before(deadline) {
		now := time.Now()

		job, err := c.getJob(jobID)
		if err != nil {
			return nil, lastResultsCount, err
		}

		status := extractJobStatus(job)
		failureReason := extractFailureReason(job)

		if status == models.StatusFailed || status == models.StatusCancelled || status == models.StatusAborting {
			if failureReason == "" {
				failureReason = "no failure reason provided"
			}
			return nil, max(0, lastResultsCount), fmt.Errorf("terminal job status %q: %s", status, failureReason)
		}

		resultsPage, err := c.getResultsPage(jobID, 1, 1)
		if err != nil {
			return nil, lastResultsCount, fmt.Errorf("failed to retrieve intermediate results while status=%s: %w", status, err)
		}
		resultsCount := resultsPage.TotalCount

		if status != lastStatus || failureReason != lastFailureReason || resultsCount != lastResultsCount {
			c.t.Logf(
				"job %s status=%s failure_reason=%q results=%d elapsed=%s",
				jobID,
				status,
				failureReason,
				resultsCount,
				time.Since(start).Round(time.Second),
			)
			lastStatus = status
			lastFailureReason = failureReason
		}

		if resultsCount != lastResultsCount {
			lastResultsCount = resultsCount
			lastResultsChangeAt = now
			if maxResults > 0 && resultsCount >= maxResults && reachedMaxResultsAt.IsZero() {
				reachedMaxResultsAt = now
			}
		}

		switch status {
		case models.StatusOK:
			return job, resultsCount, nil
		case models.StatusPending:
			if pendingStartedAt.IsZero() {
				pendingStartedAt = now
			}
			if now.Sub(pendingStartedAt) > c.cfg.pendingTimeout {
				return nil, resultsCount, fmt.Errorf(
					"job stuck in pending for %s (pending timeout %s)",
					now.Sub(pendingStartedAt).Round(time.Second),
					c.cfg.pendingTimeout,
				)
			}
		case models.StatusWorking:
			if workingStartedAt.IsZero() {
				workingStartedAt = now
			}

			if resultsCount == 0 && now.Sub(workingStartedAt) > c.cfg.zeroResultsTimeout {
				return nil, resultsCount, fmt.Errorf(
					"job stuck in working with zero results for %s (zero-results timeout %s)",
					now.Sub(workingStartedAt).Round(time.Second),
					c.cfg.zeroResultsTimeout,
				)
			}

			if maxResults > 0 && resultsCount >= maxResults {
				if reachedMaxResultsAt.IsZero() {
					reachedMaxResultsAt = now
				}
				if now.Sub(reachedMaxResultsAt) > c.cfg.maxResultsGraceTime {
					return nil, resultsCount, fmt.Errorf(
						"job reached max_results=%d but stayed non-terminal for %s (grace timeout %s)",
						maxResults,
						now.Sub(reachedMaxResultsAt).Round(time.Second),
						c.cfg.maxResultsGraceTime,
					)
				}
			} else if resultsCount > 0 && now.Sub(lastResultsChangeAt) > c.cfg.progressStall {
				return nil, resultsCount, fmt.Errorf(
					"job progress stalled for %s with status=%s and results=%d (stall timeout %s)",
					now.Sub(lastResultsChangeAt).Round(time.Second),
					status,
					resultsCount,
					c.cfg.progressStall,
				)
			}
		default:
			return nil, resultsCount, fmt.Errorf("unexpected job status %q", status)
		}

		time.Sleep(c.cfg.pollInterval)
	}

	return nil, max(0, lastResultsCount), fmt.Errorf(
		"job timed out after %s (last_status=%s last_failure_reason=%q last_results=%d)",
		c.cfg.jobTimeout,
		lastStatus,
		lastFailureReason,
		max(0, lastResultsCount),
	)
}

func (c *apiE2EClient) doJSON(method, path string, body any, expectedStatus int, out any) error {
	respBody, _, err := c.doBytes(method, path, body, expectedStatus)
	if err != nil {
		return err
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("failed to decode response JSON: %w (body=%s)", err, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (c *apiE2EClient) doBytes(method, path string, body any, expectedStatus int) ([]byte, string, error) {
	endpoint := c.cfg.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, endpoint, bodyReader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.devUserID != "" {
		req.Header.Set("X-Braza-Dev-User", c.cfg.devUserID)
	}
	authToken, err := c.authTokenForRequest()
	if err != nil {
		return nil, "", err
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	if c.cfg.sessionCookie != "" {
		req.AddCookie(&http.Cookie{Name: "__session", Value: c.cfg.sessionCookie})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != expectedStatus {
		return nil, "", fmt.Errorf(
			"unexpected status %d for %s %s: %s",
			resp.StatusCode,
			method,
			endpoint,
			strings.TrimSpace(string(respBody)),
		)
	}

	return respBody, resp.Header.Get("Content-Type"), nil
}

func (c *apiE2EClient) authTokenForRequest() (string, error) {
	if strings.TrimSpace(c.cfg.devUserID) != "" {
		return "", nil
	}
	if c.cfg.clerkSecretKey != "" && c.cfg.clerkSessionID != "" {
		if tok := c.getCachedToken(); tok != "" {
			return tok, nil
		}

		token, err := c.mintClerkSessionToken()
		if err != nil {
			return "", fmt.Errorf("failed to mint Clerk session token: %w", err)
		}

		expiry, err := extractExpiryFromJWT(token)
		if err != nil {
			// If we can't parse expiry, cache briefly to avoid hammering Clerk.
			expiry = time.Now().Add(30 * time.Second)
		}

		c.setCachedToken(token, expiry)

		return token, nil
	}
	return c.cfg.bearerToken, nil
}

func (c *apiE2EClient) getCachedToken() string {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.tokenCache.token == "" {
		return ""
	}

	// Refresh 30s early to reduce flakiness near expiry.
	if !c.tokenCache.expiry.IsZero() && time.Now().After(c.tokenCache.expiry.Add(-30*time.Second)) {
		c.tokenCache = cachedToken{}
		return ""
	}

	return c.tokenCache.token
}

func (c *apiE2EClient) setCachedToken(token string, expiry time.Time) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	c.tokenCache = cachedToken{
		token:  token,
		expiry: expiry,
	}
}

func (c *apiE2EClient) mintClerkSessionToken() (string, error) {
	endpoint := fmt.Sprintf("https://api.clerk.com/v1/sessions/%s/tokens", url.PathEscape(c.cfg.clerkSessionID))

	const maxAttempts = 5
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodPost, endpoint, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create Clerk token request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.clerkSecretKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts && isRetryableNetErr(err) {
				time.Sleep(clerkRetryDelay(attempt))
				continue
			}
			return "", fmt.Errorf("Clerk token request failed: %w", err)
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < maxAttempts {
				time.Sleep(clerkRetryDelay(attempt))
				continue
			}
			return "", fmt.Errorf("failed to read Clerk token response: %w", readErr)
		}

		trimmed := strings.TrimSpace(string(body))
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 && attempt < maxAttempts {
			lastErr = fmt.Errorf("Clerk token endpoint returned status %d: %s", resp.StatusCode, trimmed)
			time.Sleep(clerkRetryDelay(attempt))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("Clerk token endpoint returned status %d: %s", resp.StatusCode, trimmed)
		}

		var out struct {
			JWT string `json:"jwt"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return "", fmt.Errorf("failed to parse Clerk token response: %w", err)
		}
		if strings.TrimSpace(out.JWT) == "" {
			return "", fmt.Errorf("Clerk token response missing jwt")
		}
		return out.JWT, nil
	}

	return "", fmt.Errorf("Clerk token request failed after %d attempts: %v", maxAttempts, lastErr)
}

func clerkRetryDelay(attempt int) time.Duration {
	// 0.5s, 1s, 2s, 4s, 4s...
	delay := 500 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 4*time.Second {
		return 4 * time.Second
	}
	return delay
}

func isRetryableNetErr(err error) bool {
	// Handle common wrappers.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isRetryableNetErr(urlErr.Err)
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	type netErr interface {
		Timeout() bool
		Temporary() bool
	}
	var ne netErr
	if errors.As(err, &ne) {
		return ne.Timeout() || ne.Temporary()
	}

	return false
}

func extractSessionIDFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid JWT format")
	}

	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode JWT claims: %w", err)
	}

	var claims struct {
		SessionID string `json:"sid"`
	}
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return "", fmt.Errorf("failed to unmarshal JWT claims: %w", err)
	}
	if strings.TrimSpace(claims.SessionID) == "" {
		return "", fmt.Errorf("sid claim not found in JWT")
	}
	return claims.SessionID, nil
}

func extractExpiryFromJWT(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid JWT format")
	}

	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to decode JWT claims: %w", err)
	}

	var claims struct {
		Expiry int64 `json:"exp"`
	}
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return time.Time{}, fmt.Errorf("failed to unmarshal JWT claims: %w", err)
	}

	if claims.Expiry == 0 {
		return time.Time{}, fmt.Errorf("exp claim not found in JWT")
	}

	return time.Unix(claims.Expiry, 0), nil
}

func validateCSVContent(csvBytes []byte) (csvValidation, error) {
	raw := bytes.TrimSpace(csvBytes)
	if len(raw) == 0 {
		return csvValidation{}, fmt.Errorf("CSV payload is empty")
	}

	utf8BOM := []byte{0xEF, 0xBB, 0xBF}
	if bytes.HasPrefix(raw, utf8BOM) {
		raw = bytes.TrimPrefix(raw, utf8BOM)
	}

	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = -1

	headerRow, err := reader.Read()
	if err != nil {
		return csvValidation{}, fmt.Errorf("failed to parse CSV header: %w", err)
	}
	header := make([]string, len(headerRow))
	copy(header, headerRow)
	if len(header) == 0 {
		return csvValidation{}, fmt.Errorf("CSV header is empty")
	}
	header[0] = strings.TrimPrefix(header[0], "\ufeff")

	headerIndex := make(map[string]int, len(header))
	for i, h := range header {
		headerIndex[strings.ToLower(strings.TrimSpace(h))] = i
	}

	requiredColumns := []string{"input_id", "link", "title", "images", "user_reviews_extended"}
	requiredPresent := true
	for _, column := range requiredColumns {
		if _, ok := headerIndex[column]; !ok {
			requiredPresent = false
			break
		}
	}
	if !requiredPresent {
		return csvValidation{
			HeaderColumns:          header,
			RequiredColumnsPresent: false,
		}, fmt.Errorf("CSV missing one of required columns: %v", requiredColumns)
	}

	titleIdx := headerIndex["title"]
	linkIdx := headerIndex["link"]
	imagesIdx := headerIndex["images"]
	reviewsExtendedIdx := headerIndex["user_reviews_extended"]

	dataRows := 0
	rowsWithTitleAndLink := 0
	rowsWithNonEmptyImages := 0
	rowsWithNonEmptyReviewsExtended := 0

	lineNumber := 1 // header
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return csvValidation{}, fmt.Errorf("failed to read CSV row %d: %w", lineNumber+1, err)
		}
		lineNumber++

		if len(row) == 1 && strings.TrimSpace(row[0]) == "" {
			continue
		}
		if len(row) != len(header) {
			return csvValidation{}, fmt.Errorf("row %d has %d columns, expected %d", lineNumber, len(row), len(header))
		}
		dataRows++

		title := strings.TrimSpace(row[titleIdx])
		link := strings.TrimSpace(row[linkIdx])
		if title != "" && link != "" {
			rowsWithTitleAndLink++
		}

		imagesRaw := strings.TrimSpace(row[imagesIdx])
		if imagesRaw != "" && imagesRaw != "[]" && imagesRaw != "null" {
			rowsWithNonEmptyImages++
		}
		reviewsRaw := strings.TrimSpace(row[reviewsExtendedIdx])
		if reviewsRaw != "" && reviewsRaw != "[]" && reviewsRaw != "null" {
			rowsWithNonEmptyReviewsExtended++
		}
	}

	if dataRows == 0 {
		return csvValidation{}, fmt.Errorf("CSV parsed but no non-empty data rows were found")
	}

	return csvValidation{
		HeaderColumns:                   header,
		TotalDataRows:                   dataRows,
		RowsWithTitleAndLink:            rowsWithTitleAndLink,
		RowsWithNonEmptyImages:          rowsWithNonEmptyImages,
		RowsWithNonEmptyReviewsExtended: rowsWithNonEmptyReviewsExtended,
		RequiredColumnsPresent:          true,
	}, nil
}

func assertResultsQuality(t *testing.T, page paginatedResultsResponse, minResults int, expectImages bool, expectReviews bool) {
	t.Helper()

	if page.TotalCount < minResults {
		t.Fatalf("expected at least %d total results, got %d", minResults, page.TotalCount)
	}
	if len(page.Results) == 0 {
		t.Fatalf("results page has total_count=%d but results array is empty", page.TotalCount)
	}

	nonEmptyTitleAndLink := 0
	resultsWithImages := 0
	resultsWithReviews := 0
	for _, result := range page.Results {
		title := strings.TrimSpace(stringFromAny(result["title"]))
		link := strings.TrimSpace(stringFromAny(result["link"]))
		if title != "" && link != "" {
			nonEmptyTitleAndLink++
		}

		if expectImages {
			rawImages, ok := result["images"]
			if !ok || rawImages == nil {
				continue
			}
			imgs, ok := rawImages.([]any)
			if !ok || len(imgs) == 0 {
				continue
			}
			// Sanity check: at least one image entry has a non-empty "image" URL.
			for _, img := range imgs {
				imgMap, ok := img.(map[string]any)
				if !ok {
					continue
				}
				u := strings.TrimSpace(stringFromAny(imgMap["image"]))
				if u != "" {
					resultsWithImages++
					break
				}
			}
		}

		if expectReviews {
			rawReviews, ok := result["user_reviews_extended"]
			if !ok || rawReviews == nil {
				continue
			}
			reviews, ok := rawReviews.([]any)
			if ok && len(reviews) > 0 {
				resultsWithReviews++
			}
		}
	}

	if nonEmptyTitleAndLink == 0 {
		t.Fatalf("results page does not contain any record with both non-empty title and link")
	}
	if expectImages && resultsWithImages == 0 {
		t.Fatalf("expected at least one result record with non-empty images, but none were found")
	}
	if expectReviews && resultsWithReviews == 0 {
		t.Fatalf("expected at least one result record with non-empty user_reviews_extended, but none were found")
	}
}

func extractJobStatus(job map[string]any) string {
	status := strings.TrimSpace(stringFromAny(job["Status"]))
	if status != "" {
		return status
	}
	return strings.TrimSpace(stringFromAny(job["status"]))
}

func extractFailureReason(job map[string]any) string {
	failureReason := strings.TrimSpace(stringFromAny(job["FailureReason"]))
	if failureReason != "" {
		return failureReason
	}
	return strings.TrimSpace(stringFromAny(job["failure_reason"]))
}

func assertMapContains(t *testing.T, got map[string]any, expected map[string]any) {
	t.Helper()

	for key, want := range expected {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("expected key %q in job data", key)
		}

		switch wantTyped := want.(type) {
		case bool:
			gotBool, ok := gotValue.(bool)
			if !ok {
				t.Fatalf("expected bool for key %q, got %T", key, gotValue)
			}
			if gotBool != wantTyped {
				t.Fatalf("expected %q=%v, got %v", key, wantTyped, gotBool)
			}
		case int:
			gotInt, err := intFromAny(gotValue)
			if err != nil {
				t.Fatalf("expected int for key %q: %v", key, err)
			}
			if gotInt != wantTyped {
				t.Fatalf("expected %q=%d, got %d", key, wantTyped, gotInt)
			}
		case string:
			gotString := stringFromAny(gotValue)
			if gotString != wantTyped {
				t.Fatalf("expected %q=%q, got %q", key, wantTyped, gotString)
			}
		case []string:
			gotStrings, err := stringSliceFromAny(gotValue)
			if err != nil {
				t.Fatalf("expected []string for key %q: %v", key, err)
			}
			if !reflect.DeepEqual(gotStrings, wantTyped) {
				t.Fatalf("expected %q=%v, got %v", key, wantTyped, gotStrings)
			}
		default:
			t.Fatalf("unsupported expected type for key %q: %T", key, want)
		}
	}
}

func getMap(payload map[string]any, keys ...string) (map[string]any, error) {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			m, ok := value.(map[string]any)
			if ok {
				return m, nil
			}
			return nil, fmt.Errorf("key %q has unexpected type %T", key, value)
		}
	}
	return nil, fmt.Errorf("none of keys %v found", keys)
}

func intFromAny(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case json.Number:
		asInt, err := n.Int64()
		if err != nil {
			return 0, err
		}
		return int(asInt), nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", v)
	}
}

func int64FromAny(v any) (int64, error) {
	switch n := v.(type) {
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case float64:
		return int64(n), nil
	case json.Number:
		return n.Int64()
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", v)
	}
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch value := v.(type) {
	case string:
		return value
	default:
		return fmt.Sprintf("%v", v)
	}
}

func stringSliceFromAny(v any) ([]string, error) {
	switch values := v.(type) {
	case []string:
		return values, nil
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("non-string slice element type %T", item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported slice type %T", v)
	}
}
