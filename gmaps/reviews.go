package gmaps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
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
)

//go:embed review_dom.js
var reviewDOMScript string

// reviewPageBudget is how many pages of 20 a pagination loop may take for a place reporting
// `count` reviews, so a large or drifting count is not silently truncated at a fixed page total.
// `cap` bounds it -- `cap/20` pages is the most that could ever be kept -- and `count <= 0`
// (unknown) gets that same ceiling rather than a guess. `+2` pages of headroom plus roughly 5% of
// slack, at least one page, absorb a count that moved between the listing read and the pull.
func reviewPageBudget(count, limit int) int {
	ceiling := limit / 20
	if ceiling < 1 {
		ceiling = 1
	}

	if count <= 0 {
		return ceiling
	}

	pages := (count + 19) / 20

	slack := pages / 20
	if slack < 1 {
		slack = 1
	}

	pages += 2 + slack
	if pages > ceiling {
		return ceiling
	}

	return pages
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

// reviewPageURL is one page of a place's reviews: `pageToken` empty for the first, `requestID`
// held for the whole pagination. The fixed `pb` components are as Google Maps sent them in
// December 2025; when Google changes them, pages stop parsing (`errRPCParsedNothing`, then
// `stopParseError`) rather than coming back empty.
func reviewPageURL(mapURL, pageToken, requestID string, pageSize int) (string, error) {
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
	ReviewID                string
	AuthorName              string
	AuthorURL               string
	ProfilePicture          string
	Rating                  int
	RelativeTimeDescription string
	Text                    string
	Images                  []string
	PublishedAt             string
	ReplyText               string
}

// mergeDOMReviews indexes identities once instead of comparing every loaded card
// with every prior card on each scroll. Distinct IDs always remain distinct.
func mergeDOMReviews(reviews, incoming []DOMReview, index map[string]int, limit int) []DOMReview {
	if limit <= 0 {
		limit = defaultReviewCap
	}

	for position := range incoming {
		next := &incoming[position]

		key := next.ReviewID
		if key == "" {
			data, _ := json.Marshal([]any{next.AuthorURL, next.AuthorName, next.Rating, next.RelativeTimeDescription, next.Text})
			key = fmt.Sprintf("fallback:%x", sha256.Sum256(data))
		}

		if i, ok := index[key]; ok {
			old := &reviews[i]
			if len(next.Text) > len(old.Text) {
				old.Text = next.Text
			}

			if next.AuthorURL != "" {
				old.AuthorURL = next.AuthorURL
			}

			if next.PublishedAt != "" {
				old.PublishedAt = next.PublishedAt
			}

			if next.ReplyText != "" {
				old.ReplyText = next.ReplyText
			}

			if len(next.Images) > len(old.Images) {
				old.Images = next.Images
			}

			continue
		}

		if len(reviews) >= limit {
			break
		}

		index[key] = len(reviews)
		reviews = append(reviews, *next)
	}

	return reviews
}

// ConvertDOMReviewsToReviews converts DOMReview slice to Review slice
func ConvertDOMReviewsToReviews(domReviews []DOMReview) []Review {
	reviews := make([]Review, 0, len(domReviews))

	for i := range domReviews {
		dr := &domReviews[i]
		review := Review{
			Name:           dr.AuthorName,
			ProfilePicture: dr.ProfilePicture,
			Rating:         dr.Rating,
			Description:    dr.Text,
			When:           dr.RelativeTimeDescription,
			Images:         dr.Images,
			ReviewID:       dr.ReviewID,
			AuthorURL:      dr.AuthorURL,
			ReplyText:      dr.ReplyText,
			Source:         "Google Maps public page",
		}

		if published, err := time.Parse(time.RFC3339, dr.PublishedAt); err == nil && !published.Before(earliestReviewPublishedAt) && !published.After(time.Now().Add(reviewPublishedAtFutureSkew)) {
			review.PublishedAt = &published
		}

		if review.Name != "" || review.ReviewID != "" {
			reviews = append(reviews, review)
		}
	}

	return reviews
}

func decodeDOMReviews(raw []any) []DOMReview {
	decoded := make([]DOMReview, 0, len(raw))

	for _, rawReview := range raw {
		reviewMap, ok := rawReview.(map[string]interface{})
		if !ok {
			continue
		}

		review := DOMReview{}
		if v, ok := reviewMap["published_at"].(string); ok {
			review.PublishedAt = v
		}

		if v, ok := reviewMap["reply_text"].(string); ok {
			review.ReplyText = v
		}

		if v, ok := reviewMap["review_id"].(string); ok {
			review.ReviewID = v
		}

		if v, ok := reviewMap["author_name"].(string); ok {
			review.AuthorName = v
		}

		if v, ok := reviewMap["author_url"].(string); ok {
			review.AuthorURL = v
		}

		if v, ok := reviewMap["profile_picture"].(string); ok {
			review.ProfilePicture = v
		}

		// Playwright decodes whole JS numbers as int, fractional ones as float64.
		switch v := reviewMap["rating"].(type) {
		case int:
			review.Rating = v
		case float64:
			review.Rating = int(v)
		}

		// The rating selectors also match non-star aria-labels, e.g. "12 reviews".
		if review.Rating < 1 || review.Rating > 5 {
			review.Rating = 0
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

		decoded = append(decoded, review)
	}

	return decoded
}

// Why a pass through the DOM scroll loop stops.
const (
	domStopCap    = "cap"
	domStopTarget = "target"
	domStopStuck  = "stuck"
)

// domShouldStop is the scroll loop's stop decision, pulled out as a pure function so it is
// tested without a browser. `domCount` is this pass's own accumulated DOM count; `limit` the
// safety ceiling; `unionCount` is what DOM plus everything already known holds (typically
// `known.distinct() + netNewAgainst(known, reviews)`); `target` is the place's reported count, 0
// when unknown; `stuckPasses` is how many consecutive passes found nothing new.
func domShouldStop(domCount, limit, unionCount, target, stuckPasses, stuckLimit int) (stop bool, reason string) {
	if domCount >= limit {
		return true, domStopCap
	}

	if target > 0 && unionCount >= target {
		return true, domStopTarget
	}

	if stuckPasses >= stuckLimit {
		return true, domStopStuck
	}

	return false, ""
}

// extractReviewsFromPage scrolls the public review panel until it, together with whatever `known`
// already holds (typically the RPC pages a caller already fetched), reaches `target` -- not until
// its own count alone does, which used to mean re-scrolling past reviews RPC already had. `known`
// may be nil (nothing known yet); an id-less DOM review is never counted as one `known` has, the
// same rule the rest of this codebase uses for an id-less review.
func extractReviewsFromPage(ctx context.Context, page scrapemate.BrowserPage, known *reviewSet,
	target, limit int) []DOMReview {
	log.Printf("Attempting DOM-based review extraction")

	if limit <= 0 {
		limit = defaultReviewCap
	}

	// First, try to click the reviews section to open the reviews panel
	clickedReviews, _ := page.Eval(openReviewsPanelScript)

	if clickedReviews != nil && clickedReviews != false {
		log.Printf("Clicked reviews via: %v", clickedReviews)
	}

	// Wait for reviews panel to load
	time.Sleep(3 * time.Second)
	// Prefer the public UI's chronological ordering. If the control is unavailable,
	// retain the existing order and disclose it instead of claiming newest-first.
	sortOpened, _ := page.Eval(`() => {
        const button = [...document.querySelectorAll('button')].find(b => /sort reviews/i.test(b.getAttribute('aria-label') || ''));
        if (!button) return false;
        button.click(); return true;
    }`)
	if sortOpened == true {
		time.Sleep(time.Second)

		sorted, _ := page.Eval(`() => {
            const option = [...document.querySelectorAll('[role="menuitemradio"], [role="menuitem"], [role="option"]')].find(e => e.textContent.trim() === 'Newest');
            if (!option) return false;
            option.click(); return true;
        }`)
		log.Printf("Public review newest sort selected: %v", sorted == true)

		if sorted == true {
			time.Sleep(2 * time.Second)
		}
	}

	var reviews []DOMReview

	deadline := time.Now().Add(15 * time.Minute)
	reviewIndex := make(map[string]int)
	maxScrollAttempts := 1200
	maxStuckPasses := 15
	lastCount := 0
	stuckCount := 0

	for attempt := 0; attempt < maxScrollAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return reviews
		default:
		}

		if time.Now().After(deadline) {
			log.Printf("Review time budget reached at %d reviews", len(reviews))
			break
		}
		// Extract reviews from the public review panel.
		reviewsJSON, err := page.Eval(reviewDOMScript)

		if err != nil {
			log.Printf("Error extracting reviews from DOM: %v", err)
		} else if reviewsJSON != nil {
			rawReviews, ok := reviewsJSON.([]any)
			if ok {
				decoded := decodeDOMReviews(rawReviews)

				reviews = mergeDOMReviews(reviews, decoded, reviewIndex, limit)
			}
		}

		currentCount := len(reviews)
		if currentCount == lastCount {
			stuckCount++
		} else {
			stuckCount = 0
			lastCount = currentCount
		}

		if stop, reason := domShouldStop(currentCount, limit,
			known.distinct()+netNewAgainst(known, reviews), target, stuckCount, maxStuckPasses); stop {
			switch reason {
			case domStopCap:
				log.Printf("Review safety limit reached at %d reviews", currentCount)
			case domStopStuck:
				log.Printf("Review count stuck at %d, stopping scroll", currentCount)
			}

			break
		}

		// Scroll the review's actual scrollable ancestor, not a layout sibling.
		_, _ = page.Eval(`() => {
            for (const review of document.querySelectorAll('[data-review-id], .jftiEf')) {
                if (!review.getClientRects().length) continue;
                for (let parent = review.parentElement; parent; parent = parent.parentElement) {
                    if (parent.clientHeight > 0 && parent.scrollHeight > parent.clientHeight + 4 && /auto|scroll/.test(getComputedStyle(parent).overflowY)) {
                        parent.scrollBy(0, Math.max(800, parent.clientHeight * 0.9));
                        return true;
                    }
                }
            }
            return false;
        }`)

		select {
		case <-ctx.Done():
			return reviews
		case <-time.After(time.Second):
		}
	}

	log.Printf("DOM extraction completed: %d reviews found", len(reviews))

	return reviews
}

// netNewAgainst counts how many of these DOM reviews are not already known. An id-less review is
// never counted as one `known` has -- there is nothing to compare it by.
func netNewAgainst(known *reviewSet, reviews []DOMReview) int {
	n := 0

	for i := range reviews {
		if id := reviews[i].ReviewID; id != "" && !known.has(id) {
			n++
		}
	}

	return n
}

// openReviewsPanelScript opens the place's review panel, whichever control the page offers.
const openReviewsPanelScript = `() => {
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
}`
