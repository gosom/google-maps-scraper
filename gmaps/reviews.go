package gmaps

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/fetchers/stealth"
	"github.com/playwright-community/playwright-go"
)

const maxReviewPages = 500

type fetchReviewsParams struct {
	page        playwright.Page
	mapURL      string
	reviewCount int
	maxReviews  int // Maximum number of reviews to fetch
}

type fetchReviewsResponse struct {
	pages [][]byte
}

type fetcher struct {
	httpClient scrapemate.HTTPFetcher
	params     fetchReviewsParams
}

func newReviewFetcher(params fetchReviewsParams) *fetcher {
	netClient := stealth.New("firefox", nil)
	ans := fetcher{
		params:     params,
		httpClient: netClient,
	}

	return &ans
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
			slog.Error("reviews_generate_url_failed",
				slog.String("next_page_token", nextPageToken),
				slog.Any("error", err),
			)
			break
		}

		currentPageBody, err = f.fetchReviewPage(ctx, reviewURL)
		if err != nil {
			slog.Error("reviews_fetch_page_failed",
				slog.String("next_page_token", nextPageToken),
				slog.String("review_url", reviewURL),
				slog.Any("error", err),
			)
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
		"https://www.google.com/maps/rpc/listugcposts?authuser=0&hl=el&pb=%s",
		strings.Join(pbComponents, ""),
	)

	return fullURL, nil
}

func (f *fetcher) fetchReviewPage(ctx context.Context, u string) ([]byte, error) {
	// Try authenticated fetch with Google cookies (bypasses restricted view)
	if cookieHeader := GetCookieHeader(); cookieHeader != "" {
		body, err := fetchWithCookies(ctx, u, cookieHeader)
		if err == nil {
			return body, nil
		}
		slog.Debug("authenticated_review_fetch_failed_falling_back", slog.Any("error", err))
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

// fetchWithCookies performs an HTTP GET with Google auth cookies using net/http.
func fetchWithCookies(ctx context.Context, u string, cookieHeader string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	client := &http.Client{Timeout: 30 * time.Second}
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
