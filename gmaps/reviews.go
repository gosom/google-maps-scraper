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

		patterns = make(map[string]*regexp.Regexp)
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
	OwnerResponse           string
	OwnerResponseTime       string
}

// ConvertDOMReviewsToReviews converts DOMReview slice to Review slice
func ConvertDOMReviewsToReviews(domReviews []DOMReview) []Review {
	reviews := make([]Review, 0, len(domReviews))

	for _, dr := range domReviews {
		review := Review{
			Name:              dr.AuthorName,
			ProfilePicture:    dr.ProfilePicture,
			Rating:            dr.Rating,
			Description:       dr.Text,
			When:              dr.RelativeTimeDescription,
			Images:            dr.Images,
			OwnerResponse:     dr.OwnerResponse,
			OwnerResponseTime: dr.OwnerResponseTime,
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
		reviewsJSON, err := page.Eval(`async () => {
			try {
				const reviews = [];

				// Pre-pass: click EVERY "More"/"Voir plus"/"Mehr"/etc. button across
				// the reviews panel to expand both review text AND owner responses.
				// Google's expansion is animated/async — clicking and reading in the
				// same synchronous tick reads the still-truncated text. We click all
				// expanders first, then await one frame + a short timeout, then read.
				document.querySelectorAll(
					'button[aria-expanded="false"], button[aria-label*="More" i], button[jsaction*="expand"], .w8nwRe'
				).forEach(b => { try { b.click(); } catch(e) {} });
				await new Promise(r => setTimeout(r, 800));

				// Selector priority: stable structural/semantic anchors first
				// (data-attributes, aria-*, role) then class names as fallback,
				// since Google's obfuscated class names rotate frequently.
				const reviewSelectors = [
					'div[data-review-id]',               // STABLE: review id attribute
					'[jsaction*="review"]',              // STABLE: jsaction-bound review block
					'.jftiEf',                           // Class fallback
					'.gws-localreviews__google-review',  // Class fallback
					'[data-hveid] .review-dialog-list > div', // Search results reviews
					'.WMbnJf',                           // Class fallback
					'.bwb7ce',                           // Class fallback
				];

				let reviewElements = [];
				for (const selector of reviewSelectors) {
					const elements = document.querySelectorAll(selector);
					if (elements && elements.length > 0) {
						reviewElements = Array.from(elements);
						// Dedupe: Google nests an empty <div data-review-id="..."> inside
						// each outer review wrapper, which double-counts. Drop any element
						// whose ancestor matches the same selector.
						reviewElements = reviewElements.filter(el => {
							let p = el.parentElement;
							while (p) {
								if (p.matches && p.matches(selector)) return false;
								p = p.parentElement;
							}
							return true;
						});
						console.log('Found reviews with selector:', selector, 'count:', reviewElements.length);
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
						// Author name - lead with semantic anchors (contributor links,
						// aria-label on buttons), fall back to class names.
						const userSelectors = [
							'a[href*="/maps/contrib/"]',           // STABLE: Maps contributor profile URL
							'button[data-href*="/contrib/"]',      // STABLE: contributor button
							'[aria-label*="Photo of"]',            // STABLE: localized "Photo of <name>"
							'.d4r55',                              // Class fallback
							'.WNxzHc',                             // Class fallback
							'.TSUbDb a',                           // Class fallback
							'.review-author',
							'button.al6Kxe',
							'.bHrnEe',
						];
						let userName = '';
						let userUrl = '';
						for (const sel of userSelectors) {
							const el = element.querySelector(sel);
							if (el) {
								userName = el.textContent?.trim() || '';
								// Strip localized "Photo of " prefix if pulled from aria-label.
								const aria = el.getAttribute('aria-label') || '';
								if (!userName && aria) {
									userName = aria.replace(/^[^:]*\s+(?:of|de|von|di|do|da)\s+/i, '').trim();
								}
								if (el.tagName?.toLowerCase() === 'a') {
									userUrl = el.getAttribute('href') || '';
								} else if (el.getAttribute('data-href')) {
									userUrl = el.getAttribute('data-href') || '';
								}
								if (userName) break;
							}
						}

						// Profile picture - lead with src host (stable hostnames)
						// before class names.
						const profilePicSelectors = [
							'img[src*="googleusercontent"]', // STABLE: Google CDN host
							'img[src*="lh3.google"]',        // STABLE: Google CDN host
							'.NBa7we',                       // Class fallback
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

						// Rating - try aria-label first (older format / a11y tools),
						// then text pattern N/5 which Google now renders as plain text
						// inside the metadata block (e.g. "4/5", "5/5").
						let rating = 0;
						const ratingSelectors = [
							'[role="img"][aria-label*="star" i]',
							'[role="img"][aria-label*="étoile" i]',
							'[role="img"][aria-label*="estrella" i]',
							'[role="img"][aria-label*="stern" i]',
							'[role="img"][aria-label]',
							'span[aria-label*="star" i]',
							'.kvMYJc',
							'.DU9Pgb span[aria-label]',
							'.pjemBf span',
							'.review-score',
						];
						for (const sel of ratingSelectors) {
							const ratingEl = element.querySelector(sel);
							if (ratingEl) {
								const ariaLabel = ratingEl.getAttribute('aria-label') || '';
								const match = ariaLabel.match(/(\d+(?:\.\d+)?)/);
								if (match) { rating = Math.round(parseFloat(match[1])) || 0; break; }
							}
						}
						// Text-format fallback: the modern review card renders the rating
						// as "4/5" in plain text. Scan short text nodes for that pattern.
						if (rating === 0) {
							const textNodes = element.querySelectorAll('span, div');
							for (const n of textNodes) {
								const t = (n.textContent || '').trim();
								if (t.length > 8) continue;
								const m = t.match(/^(\d+(?:\.\d+)?)\s*\/\s*5$/);
								if (m) { rating = Math.round(parseFloat(m[1])) || 0; break; }
							}
						}

						// Time/date: extract a relative-time substring like "3 months ago"
						// from anywhere in the review's text content. Using a regex
						// extract (not a node-text match) sidesteps the problem that
						// Google's rating + time render as sibling spans inside one
						// parent whose concatenated textContent looks like
						// "4/53 months ago on Google" with no separator.
						// Negative lookbehind rejects digits/slash before the number,
						// otherwise "4/53 months ago" (rating concatenated with time)
						// would match "53 months ago". Also covers French ("il y a 2 mois").
						const timeExtractRegex = /(?<![\d\/])(\d+\s+(?:year|month|week|day|hour|minute)s?\s+ago|a\s+(?:year|month|week|day|hour|minute)\s+ago|just\s+now|yesterday|today|il\s+y\s+a\s+(?:un|une|\d+)\s+(?:an|mois|semaine|jour|heure|minute)s?)\b/i;
						let relativeTime = '';
						const timeNodes = element.querySelectorAll('span, div');
						for (const n of timeNodes) {
							const text = (n.textContent || '').trim();
							if (!text || text.length > 200) continue;
							const m = text.match(timeExtractRegex);
							if (m) { relativeTime = m[0]; break; }
						}
						// Class-based fallbacks if text scan missed it.
						if (!relativeTime) {
							const timeSelectors = ['.rsqaWe', '.DU9Pgb', '.tTVLSc', '.review-date', '.dehysf'];
							for (const sel of timeSelectors) {
								const el = element.querySelector(sel);
								if (el) {
									const text = (el.textContent || '').trim();
									const m = text.match(timeExtractRegex);
									if (m) { relativeTime = m[0]; break; }
								}
							}
						}

						// Review text - try to expand and get full text. Lead with
						// data-* anchors then class names.
						const textSelectors = [
							'[data-expandable-section] span', // STABLE: data attribute
							'span[jsname]',                   // STABLE: jsname binding
							'.wiI7pd',                        // Class fallback (long-lived)
							'.MyEned span',
							'.review-full-text',
							'.Jtu6Td span',
						];
						let text = '';

						// "More" buttons were already clicked + awaited in the pre-pass
						// at the top of this function, so text is already expanded.
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

						// Owner response - prefer text-content / aria detection
						// over class names (most fragile selectors in this script).
						// Google localizes the "Response from the owner" header
						// across languages but always renders it as a header element
						// inside the review block.
						let ownerResponse = '';
						let ownerResponseTime = '';

						// Strategy 1 (semantic): find a header-like element whose text
						// matches a localized "Response from the owner" prefix, then
						// the response body is its sibling/next text node.
						const ownerHeaderRegex = /^(response from the owner|owner['']s reply|response from owner|réponse du propriétaire|respuesta del propietario|antwort des inhabers|risposta del proprietario|resposta do proprietário|antwoord van de eigenaar|odpowiedź właściciela|svar fra ejeren|ägarens svar|sahibinden yanıt)/i;
						const candidateHeaders = element.querySelectorAll('div, span');
						for (const h of candidateHeaders) {
							const t = (h.textContent || '').trim();
							// Header is short — full reply is in a different element.
							if (t.length > 0 && t.length < 80 && ownerHeaderRegex.test(t)) {
								// The response body is a sibling text container after the header.
								const container = h.parentElement;
								if (container) {
									// Pick the longest text descendant of the container that
									// isn't the header itself — that's the response body.
									let best = '';
									container.querySelectorAll('span, div').forEach(c => {
										if (c === h) return;
										const tt = (c.textContent || '').trim();
										if (tt.length > best.length && !ownerHeaderRegex.test(tt) && tt !== text) {
											best = tt;
										}
									});
									ownerResponse = best;
								}
								// Response time: extract via regex anywhere in the header
								// or its sibling nodes. The header text often looks like
								// "Response from the owner 3 months ago".
								const respTimeExtract = /(?<![\d\/])(\d+\s+(?:year|month|week|day|hour|minute)s?\s+ago|a\s+(?:year|month|week|day|hour|minute)\s+ago|il\s+y\s+a\s+(?:un|une|\d+)\s+(?:an|mois|semaine|jour|heure|minute)s?)\b/i;
								const headerText = (h.textContent || '').trim();
								const headerMatch = headerText.match(respTimeExtract);
								if (headerMatch) ownerResponseTime = headerMatch[0];
								if (!ownerResponseTime && h.parentElement) {
									for (const c of h.parentElement.children) {
										if (c === h) continue;
										const tt = (c.textContent || '').trim();
										const m = tt.match(respTimeExtract);
										if (m) { ownerResponseTime = m[0]; break; }
									}
								}
								if (ownerResponse) break;
							}
						}

						// Strategy 2 (class fallback): class-based selectors.
						if (!ownerResponse) {
							const responseSelectors = [
								'.CDe7pd',           // Owner response container
								'.wiI7pd.xwPlne',    // Alternative response text
								'.review-response',
								'.owner-response',
							];
							for (const sel of responseSelectors) {
								const responseEl = element.querySelector(sel);
								if (responseEl) {
									ownerResponse = responseEl.textContent?.trim() || '';
									const responseTimeEl = responseEl.closest('.review-response-container')?.querySelector('.rsqaWe') ||
									                       responseEl.parentElement?.querySelector('.rsqaWe, .dehysf');
									if (responseTimeEl) {
										ownerResponseTime = responseTimeEl.textContent?.trim() || '';
									}
									if (ownerResponse) break;
								}
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
								images: images,
								owner_response: ownerResponse,
								owner_response_time: ownerResponseTime
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

					if v, ok := reviewMap["owner_response"].(string); ok {
						review.OwnerResponse = v
					}

					if v, ok := reviewMap["owner_response_time"].(string); ok {
						review.OwnerResponseTime = v
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
