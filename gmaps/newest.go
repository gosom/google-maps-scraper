package gmaps

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gosom/scrapemate"
)

// How a newest-only collection reads the public review panel. Google's signed-out limited view
// usually shows five to eight reviews, and some sessions are served more (30 on 27 Sep 2026), so
// the panel is scrolled for as long as it keeps loading reviews -- until newestStuckPasses passes
// in a row add none, the collection's cap, or the review budget (the context's deadline).
const (
	newestSortAttempts = 3
	newestStuckPasses  = 3
)

// openSortScript opens the review panel's sort menu; false when there is no such control.
const openSortScript = `() => {
    const button = [...document.querySelectorAll('button')].find(b =>
        /sort reviews/i.test(b.getAttribute('aria-label') || '') || /^\s*sort\s*$/i.test(b.textContent || ''));
    if (!button) return false;
    button.click();
    return true;
}`

// pickNewestScript picks Newest in the open sort menu. Only a visible option is clicked: the page
// can hold a hidden copy of the menu.
const pickNewestScript = `() => {
    const option = [...document.querySelectorAll('[role="menuitemradio"], [role="menuitem"], [role="option"]')]
        .find(e => e.textContent.trim() === 'Newest' && e.getClientRects().length);
    if (!option) return false;
    option.click();
    return true;
}`

// forgetReadCardsScript clears the extraction script's per-card cache, so the next read returns
// every card on the page rather than only the ones that changed.
const forgetReadCardsScript = `() => { window.__axiocredReviewCards = new WeakMap(); return true; }`

const scrollReviewsScript = `() => {
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
}`

// extractNewestFromPage opens the review panel, sorts it by Newest and reads what it shows, in
// page order. The sort is confirmed by the reviews' own dates, not by the click: a click that
// did not take leaves the "most relevant" order, which would otherwise pass for the newest.
func extractNewestFromPage(ctx context.Context, page scrapemate.BrowserPage, limit int) ([]DOMReview, bool) {
	if limit <= 0 {
		limit = defaultReviewCap
	}

	opened, _ := page.Eval(openReviewsPanelScript)
	log.Printf("newest: review panel opened via %v", opened)

	if sleepCtx(ctx, 3*time.Second) != nil {
		return nil, false
	}

	for attempt := 1; attempt <= newestSortAttempts; attempt++ {
		clicked := false
		if opened, _ := page.Eval(openSortScript); opened == true {
			if sleepCtx(ctx, 1200*time.Millisecond) != nil {
				return nil, false
			}

			picked, _ := page.Eval(pickNewestScript)
			clicked = picked == true
		}

		if sleepCtx(ctx, 2500*time.Millisecond) != nil {
			return nil, false
		}

		reviews := readShownReviews(ctx, page, limit)
		ordered := newestOrdered(reviews)
		log.Printf("newest: attempt %d, sort clicked %v, %d reviews, newest first %v",
			attempt, clicked, len(reviews), ordered)

		if clicked && ordered {
			return reviews, true
		}
	}

	return nil, false
}

// readShownReviews reads every review card the panel will show, in page order, scrolling for as
// long as scrolling loads more.
func readShownReviews(ctx context.Context, page scrapemate.BrowserPage, limit int) []DOMReview {
	_, _ = page.Eval(forgetReadCardsScript)

	var reviews []DOMReview

	index := make(map[string]int)

	for stuck := 0; stuck < newestStuckPasses && len(reviews) < limit; {
		before := len(reviews)

		if raw, err := page.Eval(reviewDOMScript); err == nil {
			if cards, ok := raw.([]any); ok {
				reviews = mergeDOMReviews(reviews, decodeDOMReviews(cards), index, limit)
			}
		}

		if len(reviews) > before {
			stuck = 0
		} else {
			stuck++
		}

		_, _ = page.Eval(scrollReviewsScript)

		if sleepCtx(ctx, time.Second) != nil {
			break
		}
	}

	return reviews
}

var relativeAgeRE = regexp.MustCompile(`(?i)\b(a|an|one|\d+)\s+(second|minute|hour|day|week|month|year)s?\s+ago\b`)

// relativeAgeHours reads "8 hours ago", "a week ago" and the like as an age in hours. An edited
// review ("Edited 2 months ago") and anything unreadable report false.
func relativeAgeHours(desc string) (float64, bool) {
	if strings.Contains(strings.ToLower(desc), "edited") {
		return 0, false
	}

	m := relativeAgeRE.FindStringSubmatch(desc)
	if m == nil {
		return 0, false
	}

	n := 1.0
	if v, err := strconv.Atoi(m[1]); err == nil {
		n = float64(v)
	}

	unit := map[string]float64{
		"second": 1.0 / 3600, "minute": 1.0 / 60, "hour": 1, "day": 24,
		"week": 24 * 7, "month": 24 * 30, "year": 24 * 365,
	}[strings.ToLower(m[2])]

	return n * unit, true
}

// newestOrdered reports whether these reviews, in page order, run from newest to oldest by the
// ages they show. Edited reviews and unreadable ages are left out of the comparison, but at least
// two ages must be readable to judge the order at all -- unless the panel shows two reviews or
// fewer, when the order does not matter: every review it has is in the read. One review or none is
// trivially in order.
func newestOrdered(reviews []DOMReview) bool {
	if len(reviews) < 2 {
		return true
	}

	last, readable := -1.0, 0

	for i := range reviews {
		age, ok := relativeAgeHours(reviews[i].RelativeTimeDescription)
		if !ok {
			continue
		}

		if age < last {
			return false
		}

		last = age
		readable++
	}

	return readable >= 2 || len(reviews) <= 2
}
