# Reviews Debug Handoff (2026-02-16)

This file is the persistent handoff for Google Maps review extraction debugging.

## Scope
- Debug-only work in `scripts/debug_maps_reviews/main.go`.
- No production scraper code changes in this phase.

## Key Findings
1. There are two separate failure modes:
- `reviews_dom_more_reviews_click_failed`: DOM did not expose a clickable `More reviews (N)` button.
- `empty app state after extraction` / empty scraper JSON: `window.APP_INITIALIZATION_STATE` did not yield usable JSON.

2. A real bug existed in debug script click matcher and was fixed:
- JS regexes were over-escaped (`\\s`, `\\(`) so `aria-label="More reviews (366)"` did not match.
- Fixed to proper JS regex escapes (`\s`, `\(`) in `clickMoreReviewsButtonAndExtractTotal`.

3. "Limited view" is the dominant blocker in failing runs:
- When DOM contains `You're seeing a limited view of Google Maps`, review CTA is often absent.
- In those runs: `More reviews` count = 0, `data-review-id` count = 0.

4. Verified non-limited sample artifact exists (full reviews available):
- `/var/folders/rd/p52d0r6d3013113hc147yw7w0000gn/T/gmaps-debug-20260216-115834`
- Counts in `page.html`: `More reviews=2`, `data-review-id=29`, `limited view=0`.

5. Verified limited-view sample artifacts:
- `/var/folders/rd/p52d0r6d3013113hc147yw7w0000gn/T/gmaps-debug-20260216-120105`
- `/var/folders/rd/p52d0r6d3013113hc147yw7w0000gn/T/gmaps-debug-20260216-120720`
- Counts in `page.html`: `More reviews=0`, `data-review-id=0`, `limited view=1`.

6. Verified successful Wachmacher scrape after debug fixes:
- Run output dir: `/var/folders/rd/p52d0r6d3013113hc147yw7w0000gn/T/gmaps-debug-20260216-124128`
- Key output:
  - `LIMITED_VIEW_FINAL=0`
  - `MORE_REVIEWS_ARIA=More reviews (366)`
  - `MORE_REVIEWS_TOTAL=366`
  - `DOM_REVIEWS_SCRAPED=325`

## Debug Script Changes Already Applied
File: `scripts/debug_maps_reviews/main.go`
- Fixed regex in `clickMoreReviewsButtonAndExtractTotal` to correctly match `More reviews (N)`.
- Removed outdated forced UA (`Chrome/91`) so debug uses Playwright Chromium default UA.
- Added hard-reset limited-view recovery in debug flow:
  - if limited view persists, reload root Maps and reopen place by search.
- Fixed `parseReviewCount` regex escaping so `More reviews (366)` parses to an integer.
- Fixed DOM extraction regex literals that previously caused:
  - `SyntaxError: Invalid regular expression flags`
- Improved review-node targeting to prefer review card nodes (`.jftiEf[data-review-id]`) before broad fallback.

## How To Run Debug (Recommended)
For manual investigation use headful first:
```bash
cd /Users/yasseen/Documents/google-maps-scraper-2
mkdir -p /tmp/gocache
GOCACHE=/tmp/gocache go run ./scripts/debug_maps_reviews -headful -timeout 180s -url '<PLACE_URL>'
```

Then inspect emitted `OUT_DIR` artifacts:
- `page.html`
- `dom_info.json`
- `dom_reviews.json` (if reviews scraping succeeded)
- captured network files (`*.meta.json`, `*.body`)

## Quick Triage Checklist
1. Check if run is limited view:
- Search `page.html` for `limited view of Google Maps`.

2. Check review CTA presence:
- Search `page.html` for `More reviews (`.

3. Check review cards present:
- Search `page.html` for `data-review-id`.

4. If CTA is present but not clicked:
- Confirm debug script version includes regex fix above.

## Important Note
- Use headful mode for debugging visibility.
- Headful is useful for investigation, but in observed runs it does **not** guarantee avoiding limited-view mode.

## Latest Failure Analysis (2026-02-16 Afternoon Run)
- CSV checked: `/Users/yasseen/Downloads/2850d1a1-037f-464e-94e4-3f03fcab3711.csv`
- Rows: 40
- `user_reviews_extended` populated: 2 rows only
- `reviews_max` in job request log: `9999` (so the low review output was not due to review limit).

### What failed (from logs)
1. Most failures were limited-view mode:
- `reviews_dom_more_reviews_click_failed` with `dom_state.has_limited_view=true`
- `dom_state.has_more_reviews_button=false`
- `dom_state.review_nodes=0`
- Then fallback path returned empty RPC parse (`reviews_rpc_empty_dom_failed_returning_rpc` with `rpc_reviews=0`).

2. In non-limited cases, DOM fallback often returned only 3 reviews:
- `reviews_dom_fallback_used_after_empty_rpc_parse` with high `dom_total` (e.g. 146, 276, 678) but `dom_reviews=3`.
- This indicates review-list scrolling/render pacing was too aggressive and captured only first visible cards.

3. Reproduced with debug script on failing place (`Café Bades`):
- `CONSENT_ACTION=accept:button:has-text("Accept all")`
- `LIMITED_VIEW_FINAL=1`
- `DOM_REVIEW_COUNT=0`
- `MORE_REVIEWS_ERROR=could not find/click the More reviews button after scrolling`

### Production fixes now applied
1. `gmaps/reviews.go`
- Added limited-view detection before More Reviews click attempt.
- Recovery is **in-page only** now (dialog-dismiss retry); no page reload/search navigation in review path.
- Added explicit warning logs:
  - `reviews_dom_limited_view_detected_before_scrape`
- Increased DOM review scroll wait to `650ms` (from `250ms`) to match debug-proven virtualized list behavior.

2. `gmaps/job.go`
- Cookie handling now prefers **Accept all** selectors first (fallback to reject selectors).
- This reduces chance of entering Google Maps limited-view mode at session start.

### Verification done
- `go test ./gmaps` passes after changes.

---

## 2026-04-01: Review API Root Cause Found + Goroutine Leak Investigation

### Root Cause: `hl=el` hardcoded in review API
- `gmaps/reviews.go:189` hardcoded `hl=el` (Greek) in Google Maps RPC URL
- Google returns empty `[null,null,null,null,null,1]` for `hl=el`
- Returns 188KB + 20 reviews + pagination token for `hl=en`
- **Fix applied:** use job's language code via `fetchReviewsParams.langCode`
- Parsers (`extractNextPageToken`, `extractReviews`) are NOT broken

### Goroutine leak → success fix: NOT FIRING (active investigation)
- Code at `webrunner.go:900-922` should check DB for results on goroutine leak
- Strings ARE in binary (`shutdown_mate_result`, `result_count_after_leak_check`)
- `forcedCompletionCh` case IS entered (log confirms)
- `shutdownMate` IS called and returns (log confirms leaked=true)
- **BUT:** diagnostic log immediately after `shutdownMate` return NEVER appears
- Next log after leaked is `context_after_mate_start` — as if lines 901-922 are skipped
- **Hypothesis:** Binary may need clean rebuild, or there's a code path issue
