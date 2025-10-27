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
	"time"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/fetchers/stealth"
	"github.com/playwright-community/playwright-go"
)

type fetchReviewsParams struct {
	page        playwright.Page
	mapURL      string
	reviewCount int
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

	reviewURL, err := f.generateURL(f.params.mapURL, "", 20, requestIDForSession)
	if err != nil {
		return fetchReviewsResponse{}, fmt.Errorf("failed to generate initial URL: %v", err)
	}

	currentPageBody, err := f.fetchReviewPage(ctx, reviewURL)
	if err != nil {
		return fetchReviewsResponse{}, fmt.Errorf("failed to fetch initial review page: %v", err)
	}

	ans := fetchReviewsResponse{}
	ans.pages = append(ans.pages, currentPageBody)

	nextPageToken := extractNextPageToken(currentPageBody)

	for nextPageToken != "" {
		reviewURL, err = f.generateURL(f.params.mapURL, nextPageToken, 20, requestIDForSession)
		if err != nil {
			fmt.Printf("Error generating URL for token %s: %v\n", nextPageToken, err)
			break
		}

		currentPageBody, err = f.fetchReviewPage(ctx, reviewURL)
		if err != nil {
			fmt.Printf("Error fetching review page with token %s: %v (%s)\n", nextPageToken, err, reviewURL)
			break
		}

		ans.pages = append(ans.pages, currentPageBody)
		nextPageToken = extractNextPageToken(currentPageBody)
	}

	return ans, nil
}

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

type DOMReview struct {
	AuthorName              string
	AuthorURL               string
	Rating                  float64
	RelativeTimeDescription string
	Text                    string
}

func extractReviewsFromDOM(ctx context.Context, iframe playwright.Frame) ([]DOMReview, error) {
	reviewsJSON, err := iframe.Evaluate(`() => {
		try {
			const reviews = [];
			const reviewElements = document.querySelectorAll('.jftiEf');
			
			for (const element of reviewElements) {
				try {
					const userElement = element.querySelector('.d4r55');
					const userName = userElement ? userElement.textContent.trim() : "";
					const userUrl = userElement && userElement.tagName.toLowerCase() === 'a' ? 
						userElement.getAttribute('href') : "";
					
					const ratingElement = element.querySelector('.kvMYJc');
					let rating = 0;
					if (ratingElement) {
						const ariaLabel = ratingElement.getAttribute('aria-label');
						if (ariaLabel) {
							const match = ariaLabel.match(/(\d+)[\s\S]*?(\d+)/);
							if (match && match.length >= 3) {
								rating = parseFloat(match[2]) || 0;
							}
						}
					}
					
					const timeElement = element.querySelector('.rsqaWe');
					const relativeTime = timeElement ? timeElement.textContent.trim() : "";
					
					const textElement = element.querySelector('.wiI7pd');
					let text = textElement ? textElement.textContent.trim() : "";
					
					const moreButton = element.querySelector('.w8nwRe');
					if (moreButton && text.includes('...')) {
						try {
							const originalLength = text.length;
							
							moreButton.click();
							
							for (let i = 0; i < 1000000; i++) {
								if (i % 100000 === 0) {
									const updatedText = textElement.textContent.trim();
									if (updatedText.length > originalLength) {
										text = updatedText;
										break;
									}
								}
							}
						} catch (e) {
							console.error("Error expanding review text:", e);
						}
					}
					
					if (userName) {
						reviews.push({
							author_name: userName,
							author_url: userUrl,
							rating: rating,
							relative_time_description: relativeTime,
							text: text
						});
					}
				} catch (e) {
					console.error("Error extracting review data:", e);
				}
			}
			
			return reviews;
		} catch (e) {
			console.error("Error extracting reviews:", e);
			return [];
		}
	}`)
	
	if err != nil {
		return nil, fmt.Errorf("error evaluating JavaScript to extract reviews: %w", err)
	}
	
	rawReviews, ok := reviewsJSON.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format")
	}
	
	reviews := make([]DOMReview, 0, len(rawReviews))
	
	for _, rawReview := range rawReviews {
		reviewMap, ok := rawReview.(map[string]interface{})
		if !ok {
			continue
		}
		
		review := DOMReview{}
		
		if authorName, ok := reviewMap["author_name"].(string); ok {
			review.AuthorName = authorName
		}
		
		if authorURL, ok := reviewMap["author_url"].(string); ok {
			review.AuthorURL = authorURL
		}
		
		if rating, ok := reviewMap["rating"].(float64); ok {
			review.Rating = rating
		}
		
		if relativeTime, ok := reviewMap["relative_time_description"].(string); ok {
			review.RelativeTimeDescription = relativeTime
		}
		
		if text, ok := reviewMap["text"].(string); ok {
			review.Text = text
		}
		
		if review.AuthorName != "" {
			reviews = append(reviews, review)
		}
	}
	
	return reviews, nil
}

func scrollReviews(ctx context.Context, page playwright.Page, limit int) (int, []DOMReview, error) {
	startTime := time.Now()
	reviewCount := 0
	var reviews []DOMReview
	
	time.Sleep(3 * time.Second)
	
	iframe, err := findReviewsIframe(ctx, page)
	if err != nil {
		log.Printf("Failed to find reviews iframe, trying direct page approach: %v", err)
		return extractReviewsDirectly(ctx, page, limit)
	}
	
	if iframe == nil {
		log.Printf("No reviews iframe found, trying direct page approach")
		return extractReviewsDirectly(ctx, page, limit)
	}
	
	saveInterval := 20 
	scrollAttempts := 0
	maxScrollAttempts := 50
	lastReviewCount := 0
	stuckCounter := 0
	
	log.Printf("Starting to scroll reviews (limit: %d)", limit)
	
	for {
		select {
		case <-ctx.Done():
			finalReviews, _ := extractReviewsFromDOM(ctx, iframe)
			return mergeUniqueReviews(reviews, finalReviews), reviews, ctx.Err()
		default:
			currentCount, err := iframe.Evaluate(`() => {
				try {
					const reviews = document.querySelectorAll('.jftiEf');
					return reviews ? reviews.length : 0;
				} catch (e) {
					console.error("Error counting reviews:", e);
					return 0;
				}
			}`)
			
			if err != nil {
				log.Printf("Warning: error counting reviews: %v", err)
				currentCount = float64(0)
			}
			
			if currentCount == nil {
				log.Printf("Warning: got nil when counting reviews")
				currentCount = float64(0)
			}
			
			reviewCount = int(currentCount.(float64))
			
			if reviewCount == lastReviewCount {
				stuckCounter++
				if stuckCounter > 10 {
					log.Printf("Stuck at %d reviews after multiple scroll attempts, trying to break through...", reviewCount)
					
					_, _ = iframe.Evaluate(`() => {
						try {
							const moreBtn = document.querySelector('.w8nwRe');
							if (moreBtn) {
								moreBtn.click();
								return true;
							}
							
							// Try alternative buttons
							const altButtons = document.querySelectorAll('button');
							for (const btn of altButtons) {
								if (btn.textContent.includes('More') || btn.textContent.includes('Show')) {
									btn.click();
									return true;
								}
							}
							return false;
						} catch (e) {
							console.error("Error clicking more button:", e);
							return false;
						}
					}`)
					
					stuckCounter = 0
					scrollAttempts += 5
				}
			} else {
				stuckCounter = 0
				lastReviewCount = reviewCount
			}
			
			if reviewCount > 0 && (reviewCount % saveInterval == 0 || scrollAttempts % 10 == 0) && len(reviews) < reviewCount {
				newReviews, err := extractReviewsFromDOM(ctx, iframe)
				if err != nil {
					log.Printf("Warning: error extracting reviews: %v", err)
				} else {
					beforeCount := len(reviews)
					reviews = mergeUniqueReviews(reviews, newReviews)
					log.Printf("Extracted %d reviews, added %d new unique reviews, total: %d", 
						len(newReviews), len(reviews) - beforeCount, len(reviews))
				}
			}
			
			reachedEnd, err := iframe.Evaluate(`() => {
				try {
					const selectors = [
						'.m6QErb.DxyBCb.kA9KIf.dS8AEf',
						'.m6QErb.DxyBCb.kA9KIf',
						'.DxyBCb.kA9KIf',
						'.m6QErb',
						'.section-scrollbox'
					];
					
					let scrollElement = null;
					for (const selector of selectors) {
						const el = document.querySelector(selector);
						if (el) {
							scrollElement = el;
							break;
						}
					}
					
					if (!scrollElement) {
						return false;
					}
					
					// Check if we've reached the bottom
					const scrollHeight = scrollElement.scrollHeight || 0;
					const scrollTop = scrollElement.scrollTop || 0;
					const clientHeight = scrollElement.clientHeight || 0;
					
					// Consider end reached if we're within 5 pixels of the bottom
					return scrollHeight > 0 && (scrollTop + clientHeight + 5 >= scrollHeight);
				} catch (e) {
					console.error("Error checking scroll position:", e);
					return false;
				}
			}`)
			
			if err != nil {
				log.Printf("Warning: error checking if reached end: %v", err)
			} else if reachedEnd != nil && reachedEnd.(bool) {
				log.Println("Reached end of reviews, no more to load")
				finalReviews, err := extractReviewsFromDOM(ctx, iframe)
				if err == nil && len(finalReviews) > 0 {
					beforeCount := len(reviews)
					reviews = mergeUniqueReviews(reviews, finalReviews)
					log.Printf("Added %d final unique reviews, total: %d",
						len(reviews) - beforeCount, len(reviews))
				}
				break
			}
			
			if limit > 0 && len(reviews) >= limit {
				log.Printf("Reached limit of %d unique reviews", limit)
				break
			}
			
			scrollAttempts++
			if scrollAttempts >= maxScrollAttempts {
				log.Printf("Made %d scroll attempts, stopping to avoid infinite loop", scrollAttempts)
				break
			}
			
			_, err = iframe.Evaluate(`() => {
				try {
					const selectors = [
						'.m6QErb.DxyBCb.kA9KIf.dS8AEf',
						'.m6QErb.DxyBCb.kA9KIf',
						'.DxyBCb.kA9KIf',
						'.m6QErb',
						'.section-scrollbox',
						'div[role="feed"]'
					];
					
					let scrollElement = null;
					for (const selector of selectors) {
						const el = document.querySelector(selector);
						if (el) {
							scrollElement = el;
							break;
						}
					}
					
					if (!scrollElement) {
						console.log("No scroll container found, scrolling document");
						window.scrollBy(0, 600);
						return true;
					}
					
					if (typeof scrollElement.scrollBy === 'function') {
						scrollElement.scrollBy(0, 600);
					} else {
						const currentScrollTop = scrollElement.scrollTop || 0;
						scrollElement.scrollTop = currentScrollTop + 600;
					}
					return true;
				} catch (e) {
					console.error("Error scrolling:", e);
					try {
						window.scrollBy(0, 600);
						return true;
					} catch (e2) {
						console.error("Error scrolling document:", e2);
						return false;
					}
				}
			}`)
			
			if err != nil {
				log.Printf("Warning: error scrolling: %v", err)
			}
			
			time.Sleep(500 * time.Millisecond)
		}
	}
	
	log.Printf("Finished scrolling after %s, found %d reviews", time.Since(startTime), len(reviews))
	return reviewCount, reviews, nil
}

func findReviewsIframe(ctx context.Context, page playwright.Page) (playwright.Frame, error) {
	iframeSelector := "iframe[src*=\"preview=place\"]"
	
	iframeHandle, err := page.QuerySelector(iframeSelector)
	if err != nil || iframeHandle == nil {
		alternativeSelectors := []string{
			"iframe.xmEWXe",
			"iframe[src*=\"maps\"]",
			"iframe[title=\"Google Maps\"]",
			"iframe[aria-label*=\"Map\"]"
		}
		
		for _, selector := range alternativeSelectors {
			iframeHandle, err = page.QuerySelector(selector)
			if err == nil && iframeHandle != nil {
				break
			}
		}
	}
	
	if err != nil || iframeHandle == nil {
		return nil, fmt.Errorf("no iframe found on page")
	}
	
	defer iframeHandle.Dispose()
	
	frameId, err := iframeHandle.GetAttribute("id")
	if err != nil || frameId == "" {
		return nil, fmt.Errorf("failed to get iframe ID: %v", err)
	}
	
	frame := page.Frame(frameId)
	if frame == nil {
		return nil, fmt.Errorf("failed to get iframe by ID: %s", frameId)
	}
	
	return frame, nil
}

func extractReviewsDirectly(ctx context.Context, page playwright.Page, limit int) (int, []DOMReview, error) {
	log.Printf("Attempting to extract reviews directly from main page")
	
	time.Sleep(2 * time.Second)
	
	hasReviews, err := page.Evaluate(`() => {
		const reviewElements = document.querySelectorAll('.jftiEf');
		const altReviewElements = document.querySelectorAll('div[data-review-id]');
		return (reviewElements && reviewElements.length > 0) || 
		       (altReviewElements && altReviewElements.length > 0);
	}`)
	
	if err != nil || hasReviews == nil || !hasReviews.(bool) {
		log.Printf("No reviews found directly on page")
		return 0, nil, fmt.Errorf("no reviews found on page")
	}
	
	// Similar logic to iframe reviews but directly on page
	reviewCount := 0
	var reviews []DOMReview
	scrollAttempts := 0
	maxScrollAttempts := 30
	
	for scrollAttempts < maxScrollAttempts {
		// Get current reviews
		reviewsJSON, err := page.Evaluate(`() => {
			try {
				const reviews = [];
				const reviewElements = document.querySelectorAll('.jftiEf, div[data-review-id]');
				
				for (const element of reviewElements) {
					try {
						const userElement = element.querySelector('.d4r55, .WNxzHc');
						const userName = userElement ? userElement.textContent.trim() : "";
						const userUrl = userElement && userElement.tagName.toLowerCase() === 'a' ? 
							userElement.getAttribute('href') : "";
						
						const ratingElement = element.querySelector('.kvMYJc, .pjemBf span');
						let rating = 0;
						if (ratingElement) {
							const ariaLabel = ratingElement.getAttribute('aria-label');
							if (ariaLabel) {
								const match = ariaLabel.match(/(\d+)[\s\S]*?(\d+)/);
								if (match && match.length >= 3) {
									rating = parseFloat(match[2]) || 0;
								}
							}
						}
						
						const timeElement = element.querySelector('.rsqaWe, .tTVLSc');
						const relativeTime = timeElement ? timeElement.textContent.trim() : "";
						
						const textElement = element.querySelector('.wiI7pd, .MyEned');
						let text = textElement ? textElement.textContent.trim() : "";
						
						if (userName) {
							reviews.push({
								author_name: userName,
								author_url: userUrl,
								rating: rating,
								relative_time_description: relativeTime,
								text: text
							});
						}
					} catch (e) {
						console.error("Error extracting review data:", e);
					}
				}
				
				return reviews;
			} catch (e) {
				console.error("Error extracting reviews:", e);
				return [];
			}
		}`)
		
		if err == nil && reviewsJSON != nil {
			rawReviews, ok := reviewsJSON.([]interface{})
			if ok {
				newReviews := make([]DOMReview, 0, len(rawReviews))
				
				for _, rawReview := range rawReviews {
					reviewMap, ok := rawReview.(map[string]interface{})
					if !ok {
						continue
					}
					
					review := DOMReview{}
					
					if authorName, ok := reviewMap["author_name"].(string); ok {
						review.AuthorName = authorName
					}
					
					if authorURL, ok := reviewMap["author_url"].(string); ok {
						review.AuthorURL = authorURL
					}
					
					if rating, ok := reviewMap["rating"].(float64); ok {
						review.Rating = rating
					}
					
					if relativeTime, ok := reviewMap["relative_time_description"].(string); ok {
						review.RelativeTimeDescription = relativeTime
					}
					
					if text, ok := reviewMap["text"].(string); ok {
						review.Text = text
					}
					
					if review.AuthorName != "" {
						newReviews = append(newReviews, review)
					}
				}
				
				oldCount := len(reviews)
				reviews = mergeUniqueReviews(reviews, newReviews)
				reviewCount = len(reviews)
				
				if oldCount < reviewCount {
					log.Printf("Found %d total reviews directly on page", reviewCount)
				}
				
				if limit > 0 && reviewCount >= limit {
					log.Printf("Reached limit of %d direct reviews", limit)
					break
				}
			}
		}
		
		// Scroll to get more reviews
		_, err = page.Evaluate(`() => {
			try {
				const reviewsContainer = document.querySelector('.m6QErb, .DxyBCb, div[role="feed"]');
				if (reviewsContainer) {
					reviewsContainer.scrollBy(0, 800);
				} else {
					window.scrollBy(0, 800);
				}
				return true;
			} catch (e) {
				console.error("Error scrolling:", e);
				window.scrollBy(0, 800);
				return false;
			}
		}`)
		
		scrollAttempts++
		time.Sleep(800 * time.Millisecond)
	}
	
	return reviewCount, reviews, nil
}

// Helper function to merge reviews while removing duplicates
func mergeUniqueReviews(existing []DOMReview, new []DOMReview) []DOMReview {
	if len(existing) == 0 {
		return new
	}
	
	result := make([]DOMReview, len(existing))
	copy(result, existing)
	
	for _, review := range new {
		isDuplicate := false
		for _, existingReview := range existing {
			// Match on author name and first part of text to detect duplicates
			if existingReview.AuthorName == review.AuthorName && 
			   (existingReview.Text == review.Text || 
				(len(existingReview.Text) > 20 && len(review.Text) > 20 && 
				 existingReview.Text[:20] == review.Text[:20])) {
				isDuplicate = true
				break
			}
		}
		
		if !isDuplicate && review.Text != "" {
			result = append(result, review)
		}
	}
	
	return result
}


