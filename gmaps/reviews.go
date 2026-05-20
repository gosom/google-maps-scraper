package gmaps

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/proxypool"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/fetchers/stealth"
	"github.com/playwright-community/playwright-go"
)

const maxReviewPages = 500

type fetchReviewsParams struct {
	page        playwright.Page
	mapURL      string
	reviewCount int
	maxReviews  int    // Maximum number of reviews to fetch
	langCode    string // Language code for the review API (e.g., "en", "de")

	// Logging context — populated by the caller in place.go. Optional in CLI
	// scrapes where these fields are unknown; emit helpers (see
	// userArgsFromParams) omit user_id/job_id entirely when empty to avoid
	// polluting per-user Grafana queries with empty-string buckets.
	placeJobID  string
	searchJobID string
	placeName   string
	// userID and userJobID are propagated from PlaceJob.UserID / PlaceJob.UserJobID.
	// They are emitted explicitly in all reviews.go log calls because
	// scrapemate replaces the ctx-bound logger per job (scrapemate.go:312),
	// stripping the .With(job_id, user_id) attributes set by the webrunner.
	userID    string
	userJobID string
	// proxyURL is the upstream HTTP proxy URL applied to the cookie-authenticated
	// review-RPC fetch (fetchWithCookies). Empty means direct egress, which is
	// what the prior implementation always did. Propagated from PlaceJob.ProxyURL,
	// which the webrunner sets to the same per-scrape rotated proxy already used
	// for browser navigation. Routing this request through the proxy avoids
	// Google's datacenter-IP rejection on /maps/rpc/listugcposts — verified May
	// 2026 with byte-level reproduction: cookies + direct (Netcup) = 33-byte
	// `[null,null,null,null,null,1]` stub; cookies + Decodo proxy = full reviews.
	proxyURL string
}

// userArgsFromParams returns the user_id/job_id args for a fetchReviewsParams,
// omitting any empty values. See userArgs in place.go for rationale.
func userArgsFromParams(p *fetchReviewsParams) []any {
	args := make([]any, 0, 4)
	if p.userJobID != "" {
		args = append(args, "job_id", p.userJobID)
	}
	if p.userID != "" {
		args = append(args, "user_id", p.userID)
	}
	return args
}

type fetchReviewsResponse struct {
	pages [][]byte
}

type fetcher struct {
	httpClient scrapemate.HTTPFetcher
	// cookieFetchClient is the *http.Client used by fetchWithCookies for every
	// paginated review-page request. Built once in newReviewFetcher and reused
	// across all f.fetch() pages so the connection pool (which lives on
	// http.Transport, not http.Client) actually pools — without this, every
	// page costs a fresh TCP + TLS + proxy CONNECT handshake.
	cookieFetchClient *http.Client
	params            fetchReviewsParams
}

func newReviewFetcher(params fetchReviewsParams) (*fetcher, error) {
	cookieClient, err := newCookieFetchClient(params.proxyURL)
	if err != nil {
		return nil, err
	}
	return &fetcher{
		params:            params,
		httpClient:        stealth.New("firefox", nil),
		cookieFetchClient: cookieClient,
	}, nil
}

func (f *fetcher) langForURL() string {
	if f.params.langCode != "" {
		return f.params.langCode
	}
	return "en"
}

func (f *fetcher) fetch(ctx context.Context) (fetchReviewsResponse, error) {
	requestIDForSession, err := generateRandomID(21)
	if err != nil {
		return fetchReviewsResponse{}, fmt.Errorf("failed to generate session request ID: %v", err)
	}

	// Calculate page size - don't fetch more than we need
	pageSize := 20
	if f.params.maxReviews > 0 && f.params.maxReviews < pageSize {
		pageSize = f.params.maxReviews
	}

	reviewURL, err := f.generateURL(f.params.mapURL, "", pageSize, requestIDForSession)
	if err != nil {
		args := userArgsFromParams(&f.params)
		args = append(args,
			"place_job_id", f.params.placeJobID,
			"search_job_id", f.params.searchJobID,
			"place_url", f.params.mapURL,
			"place_name", f.params.placeName,
			"next_page_token", "",
			"error", err,
		)
		scrapemate.GetLoggerFromContext(ctx).Error("reviews_generate_url_failed", args...)
		return fetchReviewsResponse{}, fmt.Errorf("failed to generate initial URL: %v", err)
	}

	currentPageBody, err := f.fetchReviewPage(ctx, reviewURL)
	if err != nil {
		return fetchReviewsResponse{}, fmt.Errorf("failed to fetch initial review page: %v", err)
	}

	ans := fetchReviewsResponse{}
	ans.pages = append(ans.pages, currentPageBody)

	// Count reviews collected so far (approximate)
	reviewsCollected := pageSize

	nextPageToken := extractNextPageToken(currentPageBody)
	pageCount := 1

	for nextPageToken != "" {
		select {
		case <-ctx.Done():
			return ans, ctx.Err()
		default:
		}

		// Hard upper limit on pages to prevent unbounded fetching
		if pageCount >= maxReviewPages {
			break
		}

		// Stop if we've reached the limit
		if f.params.maxReviews > 0 && reviewsCollected >= f.params.maxReviews {
			break
		}

		// Adjust page size for remaining reviews when a limit is set
		currentPageSize := pageSize
		if f.params.maxReviews > 0 {
			remainingNeeded := f.params.maxReviews - reviewsCollected
			if remainingNeeded <= 0 {
				break
			}
			if remainingNeeded < pageSize {
				currentPageSize = remainingNeeded
			}
		}

		reviewURL, err = f.generateURL(f.params.mapURL, nextPageToken, currentPageSize, requestIDForSession)
		if err != nil {
			args := userArgsFromParams(&f.params)
			args = append(args,
				"place_job_id", f.params.placeJobID,
				"search_job_id", f.params.searchJobID,
				"place_url", f.params.mapURL,
				"place_name", f.params.placeName,
				"next_page_token", nextPageToken,
				"error", err,
			)
			scrapemate.GetLoggerFromContext(ctx).Error("reviews_generate_url_failed", args...)
			break
		}

		currentPageBody, err = f.fetchReviewPage(ctx, reviewURL)
		if err != nil {
			args := userArgsFromParams(&f.params)
			args = append(args,
				"place_job_id", f.params.placeJobID,
				"search_job_id", f.params.searchJobID,
				"place_url", f.params.mapURL,
				"place_name", f.params.placeName,
				"next_page_token", nextPageToken,
				"review_url", reviewURL,
				"error", err,
			)
			scrapemate.GetLoggerFromContext(ctx).Error("reviews_fetch_page_failed", args...)
			break
		}

		ans.pages = append(ans.pages, currentPageBody)
		reviewsCollected += currentPageSize
		pageCount++
		nextPageToken = extractNextPageToken(currentPageBody)
	}

	return ans, nil
}

// Note the added 'requestID' parameter
func (f *fetcher) generateURL(mapURL, pageToken string, pageSize int, requestID string) (string, error) {
	placeIDRegex := regexp.MustCompile(`!1s([^!]+)`)

	placeIDMatch := placeIDRegex.FindStringSubmatch(mapURL)
	if len(placeIDMatch) < 2 {
		return "", fmt.Errorf("could not extract place ID from URL: %s", mapURL)
	}

	rawPlaceID, err := url.QueryUnescape(placeIDMatch[1])
	if err != nil {
		rawPlaceID = placeIDMatch[1]
	}

	encodedPlaceID := url.QueryEscape(rawPlaceID)

	encodedPageToken := url.QueryEscape(pageToken)

	pbComponents := []string{
		fmt.Sprintf("!1m6!1s%s", encodedPlaceID),
		"!6m4!4m1!1e1!4m1!1e3",
		fmt.Sprintf("!2m2!1i%d!2s%s", pageSize, encodedPageToken),
		fmt.Sprintf("!5m2!1s%s!7e81", requestID),
		"!8m9!2b1!3b1!5b1!7b1",
		"!12m4!1b1!2b1!4m1!1e1!11m0!13m1!1e1",
	}

	fullURL := fmt.Sprintf(
		"https://www.google.com/maps/rpc/listugcposts?authuser=0&hl=%s&pb=%s",
		f.langForURL(), strings.Join(pbComponents, ""),
	)

	return fullURL, nil
}

func (f *fetcher) fetchReviewPage(ctx context.Context, u string) ([]byte, error) {
	// Try authenticated fetch with Google cookies (bypasses restricted view).
	// Reuses f.cookieFetchClient which was built once per fetcher in
	// newReviewFetcher — so paginated review-page requests share a single
	// connection pool. The client's transport is pinned to f.params.proxyURL
	// when non-empty (see fetchReviewsParams.proxyURL).
	if cookieHeader := GetCookieHeader(); cookieHeader != "" {
		body, err := fetchWithCookies(ctx, u, cookieHeader, f.cookieFetchClient)
		if err == nil {
			return body, nil
		}
		args := userArgsFromParams(&f.params)
		args = append(args,
			"place_job_id", f.params.placeJobID,
			"search_job_id", f.params.searchJobID,
			"place_url", f.params.mapURL,
			"place_name", f.params.placeName,
			"proxy_used", cmp.Or(proxypool.HostOf(f.params.proxyURL), "direct"),
			"error", err,
		)
		scrapemate.GetLoggerFromContext(ctx).Debug("authenticated_review_fetch_failed_falling_back", args...)
	}

	// Fallback to unauthenticated stealth fetch
	job := scrapemate.Job{
		Method: "GET",
		URL:    u,
	}

	resp := f.httpClient.Fetch(ctx, &job)
	if resp.Error != nil {
		return nil, fmt.Errorf("fetch error for %s: %w", u, resp.Error)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: unexpected status code: %d", u, resp.StatusCode)
	}

	return resp.Body, nil
}

// fetchWithCookies performs an HTTP GET with Google auth cookies using the
// supplied *http.Client. The client carries the per-scrape proxy configuration
// (pinned via newCookieFetchClient) and a shared connection pool that survives
// across paginated review-page fetches — see fetcher.cookieFetchClient.
//
// Routing through a proxy matters because Google's /maps/rpc/listugcposts
// soft-rejects requests from datacenter IPs even with valid cookies; the fix
// is to share the upstream identity that browser navigation already uses.
// See fetchReviewsParams.proxyURL for the byte-level reproduction.
func fetchWithCookies(ctx context.Context, u string, cookieHeader string, client *http.Client) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("authenticated fetch returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// newCookieFetchClient builds the *http.Client used by fetchWithCookies.
//
// When proxyURL is empty, returns a client with NIL Transport, which causes
// net/http to fall back to the package-global http.DefaultTransport. That
// transport is (a) shared/pooled across the whole process and (b) configured
// with Proxy: http.ProxyFromEnvironment, so HTTPS_PROXY / HTTP_PROXY /
// NO_PROXY env vars are honored. This preserves the pre-fix behavior verbatim
// for CLI/standalone use and ensures no accidental loss of connection reuse.
//
// When proxyURL is non-empty, the client owns an explicit *http.Transport with
// Proxy pinned to that URL. Env vars are NOT consulted — we never want the
// per-scrape rotated proxy that webrunner already selected to be silently
// overridden by an OS-level setting.
//
// The returned client is intended to be held on a fetcher for its lifetime
// and reused across every paginated review-page request — do NOT rebuild
// per call, or the connection pool (which lives on the Transport) is thrown
// away each time.
func newCookieFetchClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		// nil Transport → http.DefaultTransport (shared pool, env-aware).
		return &http.Client{Timeout: 30 * time.Second}, nil
	}
	pu, err := url.Parse(proxyURL)
	if err != nil {
		// CRITICAL: do NOT wrap err with %w — url.Parse returns *url.Error
		// whose Error() formats as "parse <full-URL>: <inner>". The full
		// URL includes any user:password@ userinfo. Wrapping leaks
		// credentials into structured logs (emitReviewExtractionFailed
		// writes this error verbatim into the "error" log field).
		// Surface the host:port (via proxypool.HostOf) and the inner
		// error class only.
		return nil, fmt.Errorf("parse proxy URL %s: invalid proxy URL syntax", proxypool.HostOf(proxyURL))
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
	}, nil
}

func extractNextPageToken(data []byte) string {
	text := string(data)
	prefix := ")]}'\n"
	text = strings.TrimPrefix(text, prefix)

	var result []interface{}

	err := json.Unmarshal([]byte(text), &result)
	if err != nil {
		return ""
	}

	if len(result) < 2 || result[1] == nil {
		return ""
	}

	token, ok := result[1].(string)
	if !ok {
		return ""
	}

	return token
}

func generateRandomID(length int) (string, error) {
	numBytes := (length*6 + 7) / 8
	if numBytes < 16 {
		numBytes = 16
	}

	b := make([]byte, numBytes)

	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}

	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
	if len(encoded) >= length {
		return encoded[:length], nil
	}

	return "", errors.New("generated ID is shorter than expected")
}
