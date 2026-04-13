package gmaps

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/fetchers/stealth"
)

type fetchReviewsParams struct {
	page        scrapemate.BrowserPage
	mapURL      string
	reviewCount int
}

type FetchReviewsResponse struct {
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

func (f *fetcher) fetch(ctx context.Context) (FetchReviewsResponse, error) {
	requestIDForSession, err := generateRandomID(21)
	if err != nil {
		return FetchReviewsResponse{}, fmt.Errorf("failed to generate session request ID: %v", err)
	}

	reviewURL, err := f.generateURL(f.params.mapURL, "", 20, requestIDForSession)
	if err != nil {
		return FetchReviewsResponse{}, fmt.Errorf("failed to generate initial URL: %v", err)
	}

	// First, try to fetch using the browser's session (has cookies/authentication)
	if f.params.page != nil {
		ans, err := f.fetchWithBrowser(ctx, reviewURL, requestIDForSession)
		if err == nil && len(ans.pages) > 0 {
			return ans, nil
		}

		log.Printf("Browser-based RPC fetch failed: %v, trying HTTP", err)
	}

	// Fallback to direct HTTP (may fail due to lack of authentication)
	currentPageBody, err := f.fetchReviewPage(ctx, reviewURL)
	if err != nil {
		log.Printf("RPC fetch failed, will try DOM extraction: %v", err)
		return FetchReviewsResponse{}, err
	}

	ans := FetchReviewsResponse{}
	ans.pages = append(ans.pages, currentPageBody)

	nextPageToken := extractNextPageToken(currentPageBody)

	for nextPageToken != "" {
		reviewURL, err = f.generateURL(f.params.mapURL, nextPageToken, 20, requestIDForSession)
		if err != nil {
			log.Printf("Error generating URL for token %s: %v", nextPageToken, err)
			break
		}

		currentPageBody, err = f.fetchReviewPage(ctx, reviewURL)
		if err != nil {
			log.Printf("Error fetching review page with token %s: %v", nextPageToken, err)
			break
		}

		ans.pages = append(ans.pages, currentPageBody)
		nextPageToken = extractNextPageToken(currentPageBody)
	}

	return ans, nil
}

// fetchWithBrowser uses Playwright to fetch the review API with browser cookies
func (f *fetcher) fetchWithBrowser(_ context.Context, initialURL, requestID string) (FetchReviewsResponse, error) {
	ans := FetchReviewsResponse{}
	page := f.params.page

	// Use JavaScript fetch to get the reviews with proper cookies
	jsCode := fmt.Sprintf(`async () => {
		try {
			const response = await fetch('%s', {
				method: 'GET',
				credentials: 'include',
				headers: {
					'Accept': '*/*',
					'Accept-Language': 'en-US,en;q=0.9'
				}
			});
			if (!response.ok) {
				return { error: 'HTTP ' + response.status };
			}
			const text = await response.text();
			return { data: text };
		} catch (e) {
			return { error: e.message };
		}
	}`, initialURL)

	result, err := page.Eval(jsCode)
	if err != nil {
		return ans, fmt.Errorf("browser fetch failed: %w", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return ans, fmt.Errorf("unexpected result type: %T", result)
	}

	if errMsg, hasError := resultMap["error"]; hasError {
		return ans, fmt.Errorf("fetch error: %v", errMsg)
	}

	data, ok := resultMap["data"].(string)
	if !ok || len(data) < 10 {
		return ans, fmt.Errorf("empty response from browser fetch")
	}

	ans.pages = append(ans.pages, []byte(data))

	// Get additional pages
	nextPageToken := extractNextPageToken([]byte(data))
	for nextPageToken != "" && len(ans.pages) < 50 { // Limit to 50 pages
		nextURL, err := f.generateURL(f.params.mapURL, nextPageToken, 20, requestID)
		if err != nil {
			break
		}

		jsCode = fmt.Sprintf(`async () => {
			try {
				const response = await fetch('%s', {
					method: 'GET',
					credentials: 'include'
				});
				if (!response.ok) {
					return { error: 'HTTP ' + response.status };
				}
				return { data: await response.text() };
			} catch (e) {
				return { error: e.message };
			}
		}`, nextURL)

		result, err = page.Eval(jsCode)
		if err != nil {
			break
		}

		resultMap, ok = result.(map[string]interface{})
		if !ok || resultMap["error"] != nil {
			break
		}

		data, ok = resultMap["data"].(string)
		if !ok || len(data) < 10 {
			break
		}

		ans.pages = append(ans.pages, []byte(data))
		nextPageToken = extractNextPageToken([]byte(data))
	}

	return ans, nil
}

var (
	patternsOnce sync.Once
	patterns     map[string]*regexp.Regexp
)

const hexMatchPattern = `0x[0-9a-fA-F]+:0x[0-9a-fA-F]+` // Hex format place ID

// extractPlaceID extracts the place ID from various Google Maps URL formats
func extractPlaceID(mapURL string) (string, error) {
	patternsOnce.Do(func() {
		patterns = make(map[string]*regexp.Regexp)
		// Try multiple patterns for extracting place ID
		avail := []string{
			`!1s([^!]+)`,                             // Standard format: !1s0x...
			`place_id=([^&]+)`,                       // Query parameter format
			`/place/[^/]+/@[^/]+/data=!.*!1s([^!]+)`, // Full place URL
			hexMatchPattern,                          // Hex format place ID
		}

		for _, p := range avail {
			patterns[p] = regexp.MustCompile(p)
		}
	})

	for pattern, re := range patterns {
		match := re.FindStringSubmatch(mapURL)
		if len(match) >= 2 {
			rawPlaceID, err := url.QueryUnescape(match[1])
			if err != nil {
				rawPlaceID = match[1]
			}

			return rawPlaceID, nil
		}
		// For hex format, match[0] is the full match
		if pattern == hexMatchPattern && len(match) >= 1 {
			return match[0], nil
		}
	}

	return "", fmt.Errorf("could not extract place ID from URL: %s", mapURL)
}

func (f *fetcher) generateURL(mapURL, pageToken string, pageSize int, requestID string) (string, error) {
	rawPlaceID, err := extractPlaceID(mapURL)
	if err != nil {
		return "", err
	}

	encodedPlaceID := url.QueryEscape(rawPlaceID)
	encodedPageToken := url.QueryEscape(pageToken)

	// Updated pb components based on current Google Maps API format (Dec 2025)
	pbComponents := []string{
		fmt.Sprintf("!1m6!1s%s", encodedPlaceID),
		"!6m4!4m1!1e1!4m1!1e3",
		fmt.Sprintf("!2m2!1i%d!2s%s", pageSize, encodedPageToken),
		fmt.Sprintf("!5m2!1s%s!7e81", requestID),
		"!8m9!2b1!3b1!5b1!7b1",
		"!12m4!1b1!2b1!4m1!1e1!11m0!13m1!1e1",
	}

	// Use English language for consistent parsing
	fullURL := fmt.Sprintf(
		"https://www.google.com/maps/rpc/listugcposts?authuser=0&hl=en&pb=%s",
		strings.Join(pbComponents, ""),
	)

	return fullURL, nil
}

func (f *fetcher) fetchReviewPage(ctx context.Context, u string) ([]byte, error) {
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

// DOMReview represents a review extracted from the DOM
type DOMReview struct {
	AuthorName              string
	AuthorURL               string
	ProfilePicture          string
	Rating                  int
	RelativeTimeDescription string
	Text                    string
	Images                  []string
}

// ConvertDOMReviewsToReviews converts DOMReview slice to Review slice
func ConvertDOMReviewsToReviews(domReviews []DOMReview) []Review {
	reviews := make([]Review, 0, len(domReviews))

	for _, dr := range domReviews {
		review := Review{
			Name:           dr.AuthorName,
			ProfilePicture: dr.ProfilePicture,
			Rating:         dr.Rating,
			Description:    dr.Text,
			When:           dr.RelativeTimeDescription,
			Images:         dr.Images,
		}
		if review.Name != "" {
			reviews = append(reviews, review)
		}
	}

	return reviews
}

// extractReviewsFromPage extracts reviews directly from the page DOM
// This is a fallback when the RPC API fails
func extractReviewsFromPage(ctx context.Context, page scrapemate.BrowserPage) ([]DOMReview, error) {
	log.Printf("Attempting DOM-based review extraction")

	// First, try to click the reviews section to open the reviews panel
	clickedReviews, _ := page.Eval(`() => {
		try {
			// Method 1: Click on the reviews count/link in the place info
			const reviewsButtons = document.querySelectorAll('button[jsaction*="reviewChart"], button[jsaction*="reviews"]');
			for (const btn of reviewsButtons) {
				if (btn.textContent.includes('review') || btn.getAttribute('aria-label')?.includes('review')) {
					btn.click();
					return 'reviews_button';
				}
			}

			// Method 2: Click on the reviews tab
			const tabs = document.querySelectorAll('button[role="tab"]');
			for (const tab of tabs) {
				const label = tab.getAttribute('aria-label') || tab.textContent || '';
				if (label.toLowerCase().includes('review')) {
					tab.click();
					return 'reviews_tab';
				}
			}

			// Method 3: Click the star rating area which often opens reviews
			const ratingArea = document.querySelector('.F7nice, .fontDisplayLarge');
			if (ratingArea) {
				ratingArea.click();
				return 'rating_area';
			}

			// Method 4: Look for "See all reviews" or similar links
			const allLinks = document.querySelectorAll('a, button');
			for (const link of allLinks) {
				const text = link.textContent?.toLowerCase() || '';
				if (text.includes('all review') || text.includes('see review') || text.includes('more review')) {
					link.click();
					return 'all_reviews_link';
				}
			}

			return false;
		} catch (e) {
			console.error('Error clicking reviews:', e);
			return false;
		}
	}`)

	if clickedReviews != nil && clickedReviews != false {
		log.Printf("Clicked reviews via: %v", clickedReviews)
	}

	// Wait for reviews panel to load
	time.Sleep(3 * time.Second)

	var reviews []DOMReview

	maxScrollAttempts := 30
	lastCount := 0
	stuckCount := 0

	for attempt := 0; attempt < maxScrollAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return reviews, ctx.Err()
		default:
		}

		// Extract reviews from the DOM - updated for Dec 2025 Google Maps structure
		reviewsJSON, err := page.Eval(`() => {
			try {
				const reviews = [];

				// Try multiple selectors for review container elements
				// Google Maps uses various class names that change over time
				const reviewSelectors = [
					'.jftiEf',                           // Common review container
					'div[data-review-id]',               // Review with ID attribute
					'.gws-localreviews__google-review',  // Alternative format
					'[data-hveid] .review-dialog-list > div', // Search results reviews
					'.WMbnJf',                           // Another review container
					'.bwb7ce',                           // New review format
				];

				let reviewElements = [];
				for (const selector of reviewSelectors) {
					const elements = document.querySelectorAll(selector);
					if (elements && elements.length > 0) {
						reviewElements = Array.from(elements);
						console.log('Found reviews with selector:', selector, 'count:', elements.length);
						break;
					}
				}

				// If no reviews found with specific selectors, try to find by structure
				if (reviewElements.length === 0) {
					// Look for elements that look like reviews (have rating + text)
					const allDivs = document.querySelectorAll('div[class]');
					for (const div of allDivs) {
						const hasRating = div.querySelector('[aria-label*="star"], [role="img"][aria-label*="star"]');
						const hasText = div.querySelector('span.wiI7pd, span[class*="review"]');
						if (hasRating && hasText && !reviewElements.includes(div)) {
							reviewElements.push(div);
						}
					}
				}

				console.log('Total review elements found:', reviewElements.length);

				for (const element of reviewElements) {
					try {
						// Author name - comprehensive selectors
						const userSelectors = [
							'.d4r55',           // Primary name class
							'.WNxzHc',          // Alternative name
							'.TSUbDb a',        // Link with name
							'.review-author',   // Generic
							'button.al6Kxe',    // Clickable name
							'.bHrnEe',          // Another name container
						];
						let userName = '';
						let userUrl = '';
						for (const sel of userSelectors) {
							const el = element.querySelector(sel);
							if (el) {
								userName = el.textContent?.trim() || '';
								if (el.tagName?.toLowerCase() === 'a') {
									userUrl = el.getAttribute('href') || '';
								}
								if (userName) break;
							}
						}

						// Profile picture - multiple patterns
						const profilePicSelectors = [
							'.NBa7we',
							'img[src*="googleusercontent"]',
							'img[src*="lh3.google"]',
							'.review-author-photo img',
						];
						let profilePic = '';
						for (const sel of profilePicSelectors) {
							const el = element.querySelector(sel);
							if (el) {
								profilePic = el.getAttribute('src') || '';
								if (profilePic) break;
							}
						}

						// Rating - try multiple approaches
						let rating = 0;
						const ratingSelectors = [
							'.kvMYJc',
							'.DU9Pgb span[aria-label]',
							'[role="img"][aria-label*="star"]',
							'.pjemBf span',
							'.review-score',
						];
						for (const sel of ratingSelectors) {
							const ratingEl = element.querySelector(sel);
							if (ratingEl) {
								const ariaLabel = ratingEl.getAttribute('aria-label') || '';
								// Match patterns like "5 stars", "Rated 4 out of 5", "4.5 étoiles"
								const match = ariaLabel.match(/(\d+(?:\.\d+)?)/);
								if (match) {
									rating = Math.round(parseFloat(match[1])) || 0;
									break;
								}
								// Also try counting filled stars
								const filledStars = element.querySelectorAll('.hCCjke.vzX5Ic, [aria-label*="star"][style*="color"]').length;
								if (filledStars > 0) {
									rating = filledStars;
									break;
								}
							}
						}

						// Time/date - multiple selectors
						const timeSelectors = ['.rsqaWe', '.DU9Pgb', '.tTVLSc', '.review-date', '.dehysf'];
						let relativeTime = '';
						for (const sel of timeSelectors) {
							const el = element.querySelector(sel);
							if (el) {
								const text = el.textContent?.trim() || '';
								// Look for time-related text (ago, month, year, etc)
								if (text && (text.includes('ago') || text.includes('week') || text.includes('month') ||
								    text.includes('year') || text.includes('day') || text.match(/\d{4}/))) {
									relativeTime = text;
									break;
								}
							}
						}

						// Review text - try to expand and get full text
						const textSelectors = [
							'.wiI7pd',
							'.MyEned span',
							'.review-full-text',
							'.Jtu6Td span',
							'[data-expandable-section] span',
						];
						let text = '';

						// First try to click "More" button to expand text
						const moreButtons = element.querySelectorAll('.w8nwRe, button[aria-label*="More"], button[aria-expanded="false"]');
						for (const btn of moreButtons) {
							try { btn.click(); } catch(e) {}
						}

						for (const sel of textSelectors) {
							const textEl = element.querySelector(sel);
							if (textEl) {
								text = textEl.textContent?.trim() || '';
								if (text && text.length > 5) break;
							}
						}

						// Images
						const imageElements = element.querySelectorAll('.KtCyie img, .Tya61d img, .review-photos img, img[src*="lh3"]');
						const images = [];
						for (const img of imageElements) {
							const src = img.getAttribute('src') || '';
							if (src && !src.includes('data:image') && !src.includes('profile')) {
								images.push(src);
							}
						}

						if (userName && (text || rating > 0)) {
							reviews.push({
								author_name: userName,
								author_url: userUrl,
								profile_picture: profilePic,
								rating: rating,
								relative_time_description: relativeTime,
								text: text,
								images: images
							});
						}
					} catch (e) {
						console.error("Error extracting review:", e);
					}
				}

				return reviews;
			} catch (e) {
				console.error("Error in review extraction:", e);
				return [];
			}
		}`)

		if err != nil {
			log.Printf("Error extracting reviews from DOM: %v", err)
		} else if reviewsJSON != nil {
			rawReviews, ok := reviewsJSON.([]any)
			if ok {
				for _, rawReview := range rawReviews {
					reviewMap, ok := rawReview.(map[string]interface{})
					if !ok {
						continue
					}

					review := DOMReview{}
					if v, ok := reviewMap["author_name"].(string); ok {
						review.AuthorName = v
					}

					if v, ok := reviewMap["author_url"].(string); ok {
						review.AuthorURL = v
					}

					if v, ok := reviewMap["profile_picture"].(string); ok {
						review.ProfilePicture = v
					}

					if v, ok := reviewMap["rating"].(float64); ok {
						review.Rating = int(v)
					}

					if v, ok := reviewMap["relative_time_description"].(string); ok {
						review.RelativeTimeDescription = v
					}

					if v, ok := reviewMap["text"].(string); ok {
						review.Text = v
					}

					if v, ok := reviewMap["images"].([]interface{}); ok {
						for _, img := range v {
							if imgStr, ok := img.(string); ok {
								review.Images = append(review.Images, imgStr)
							}
						}
					}

					// Add if unique (check by author name and text prefix)
					isDuplicate := false

					for _, existing := range reviews {
						if existing.AuthorName == review.AuthorName {
							if existing.Text == review.Text {
								isDuplicate = true
								break
							}

							if len(existing.Text) > 20 && len(review.Text) > 20 &&
								existing.Text[:20] == review.Text[:20] {
								isDuplicate = true
								break
							}
						}
					}

					if !isDuplicate && review.AuthorName != "" {
						reviews = append(reviews, review)
					}
				}
			}
		}

		currentCount := len(reviews)
		if currentCount == lastCount {
			stuckCount++
			if stuckCount > 5 {
				log.Printf("Review count stuck at %d, stopping scroll", currentCount)
				break
			}
		} else {
			stuckCount = 0
			lastCount = currentCount
		}

		// Scroll to load more reviews
		_, _ = page.Eval(`() => {
			try {
				// Try multiple scroll containers
				const selectors = [
					'.m6QErb.DxyBCb.kA9KIf.dS8AEf',
					'.m6QErb.DxyBCb.kA9KIf',
					'.DxyBCb.kA9KIf',
					'.m6QErb',
					'.section-scrollbox',
					'div[role="feed"]'
				];

				for (const selector of selectors) {
					const el = document.querySelector(selector);
					if (el) {
						el.scrollBy(0, 800);
						return true;
					}
				}

				window.scrollBy(0, 800);
				return true;
			} catch (e) {
				window.scrollBy(0, 800);
				return false;
			}
		}`)

		time.Sleep(500 * time.Millisecond)
	}

	log.Printf("DOM extraction completed: %d reviews found", len(reviews))

	return reviews, nil
}

// FetchReviewsWithFallback attempts RPC-based extraction first, then falls back to DOM
func FetchReviewsWithFallback(ctx context.Context, params fetchReviewsParams) (FetchReviewsResponse, []DOMReview, error) {
	fetcher := newReviewFetcher(params)

	// Try RPC-based extraction first
	rpcResponse, err := fetcher.fetch(ctx)
	if err == nil && len(rpcResponse.pages) > 0 {
		// Validate that we actually got reviews
		totalReviews := 0

		for _, page := range rpcResponse.pages {
			reviews := extractReviews(page)
			totalReviews += len(reviews)
		}

		if totalReviews > 0 {
			log.Printf("RPC extraction successful: %d review pages, ~%d reviews", len(rpcResponse.pages), totalReviews)
			return rpcResponse, nil, nil
		}

		log.Printf("RPC returned empty reviews, trying DOM extraction")
	}

	// Fallback to DOM-based extraction
	if params.page != nil {
		domReviews, domErr := extractReviewsFromPage(ctx, params.page)
		if domErr == nil && len(domReviews) > 0 {
			log.Printf("DOM extraction successful: %d reviews", len(domReviews))
			return FetchReviewsResponse{}, domReviews, nil
		}

		if domErr != nil {
			log.Printf("DOM extraction failed: %v", domErr)
		}
	}

	// Return whatever we have
	if err != nil {
		return FetchReviewsResponse{}, nil, fmt.Errorf("all review extraction methods failed: %v", err)
	}

	return rpcResponse, nil, nil
}
