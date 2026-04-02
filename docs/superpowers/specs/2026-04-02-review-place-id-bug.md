# Bug Report: Review Extraction — Two Root Causes + Missing Resilience

**Date:** 2026-04-02
**Status:** Root causes verified with unit tests AND live API calls
**Severity:** High — 100% review extraction failure

---

## Verified Facts

### Root Cause 1: Wrong Place ID Extraction (HTTP 400)

**Verified by:** 4 unit tests in `gmaps/reviews_url_test.go`

| Test | Result | Proof |
|------|--------|-------|
| `TestGenerateURL_HexPlaceID` | PASS | Original DB URLs with hex IDs → valid RPC URL |
| `TestGenerateURL_Base64PhotoID_Bug` | FAIL | `page.URL()` with base64 photo ID → wrong ID extracted |
| `TestGenerateURL_OriginalJobURL_Workaround` | PASS | `j.GetURL()` always produces correct RPC URL |
| `TestGenerateURL_AnotherBase64Example` | FAIL | Same bug on second place — not coincidence |

**Verified by:** Live API call
```
BAD URL  (base64 from page.URL()) → HTTP 400
GOOD URL (hex from j.GetURL())    → HTTP 200
```

**Root cause:** `page.URL()` returns a redirected URL where Google inserts a photo/streetview segment `!1sCIHM0ogKEICAgIC...` (base64) BEFORE the hex place ID. The regex `!1s([^!]+)` grabs the first match — the photo ID.

**Fix:** `gmaps/place.go` line 376: `placeURL := page.URL()` → `placeURL := j.GetURL()`

---

### Root Cause 2: Expired Cookies → Empty Responses (HTTP 200, 0 reviews)

**Verified by:** Live API calls on 2026-04-02

| Test | IP | Cookies | HTTP | Reviews |
|------|-----|---------|------|---------|
| Expired cookies (Feb 17) | 193.32.248.144 | 6 weeks old | 200 | **0** |
| No cookies | 89.247.251.102 (VPN) | None | 200 | **0** |
| **Fresh cookies (today)** | **89.247.251.102 (VPN)** | **Fresh** | **200** | **20 + pagination token** |
| No cookies, same VPN IP | 89.247.251.102 | None | 200 | **0** |

**Key findings:**
- **NOT rate limiting** — same IP returns 0 without cookies and 20 with fresh cookies
- **NOT IP blocking** — VPN IP works fine with fresh cookies
- **Cookies are required** — Google returns valid HTTP 200 but empty data without cookies
- **Google does NOT signal the failure** — no HTTP 401, no error message, no "cookies expired" header. Just empty `[null,null,null,null,null,1]` (33 bytes) vs 188KB of actual reviews

---

## Missing Resilience: Fail-Fast + Observability

### Problem: Google gives no signal

When cookies are expired or IP is blocked, Google returns:
- HTTP 200 (not 401 or 403)
- Valid JSON: `[null,null,null,null,null,1]`
- No error message, no header, no indication of WHY

This means our code sees a "successful" response with 0 reviews. It doesn't know if:
- Cookies expired
- IP is blocked/rate-limited
- The place genuinely has no reviews
- The API format changed

### Problem: No fail-fast — 63 identical failures

When the first place gets 0 reviews from the API, the same thing will happen for all 63 places. Currently the scraper attempts ALL of them, wasting ~10 minutes. If the first 3 places all fail the same way, the issue is systemic (cookies/IP), not per-place.

### Recommended Fix: Circuit Breaker + Diagnostic Logging

**1. Detect "silent empty" responses**

When the API returns HTTP 200 but `[null,null,null,null,null,1]` (33 bytes), this is a distinct signal. A place with 7,000 reviews returning 0 is not normal. Log this as a specific warning:

```go
if len(reviewData.pages) == 1 && len(reviewData.pages[0]) < 100 {
    slog.Warn("review_api_empty_response",
        slog.String("job_id", j.ID),
        slog.String("parent_job_id", j.ParentID),
        slog.String("place_url", placeURL),
        slog.Int("review_count_on_page", reviewCount),
        slog.Int("response_bytes", len(reviewData.pages[0])),
        slog.String("possible_cause", "expired cookies, IP blocked, or rate limited — Google returns HTTP 200 with empty data instead of an error"),
    )
}
```

**2. Fail-fast circuit breaker**

Track consecutive review failures. After N consecutive empty responses (e.g., 3), stop trying reviews for the rest of the job and log a single summary:

```go
// In the review extraction block:
if consecutiveEmptyReviews >= 3 {
    slog.Error("review_circuit_breaker_open",
        slog.String("parent_job_id", j.ParentID),
        slog.Int("consecutive_failures", consecutiveEmptyReviews),
        slog.String("action", "skipping reviews for remaining places"),
        slog.String("likely_cause", "cookies expired or IP rate-limited — Google returns empty for all places"),
    )
    // Skip review extraction, continue with place + images
    return
}
```

The circuit breaker state needs to be shared across PlaceJobs in the same scraping session. Options:
- Atomic counter on the `ExitMonitor` (already shared across jobs)
- New field on the scrapemate context
- Package-level atomic (simplest, works because one job runs at a time per webrunner)

**3. Log levels for Grafana/Loki alerting**

| Scenario | Log Level | Log Message | Grafana Alert |
|----------|-----------|-------------|---------------|
| 1 place empty, place has <10 reviews | DEBUG | `review_api_no_reviews` | No |
| 1 place empty, place has >100 reviews | WARN | `review_api_empty_response` | Monitor |
| 3+ consecutive empty | ERROR | `review_circuit_breaker_open` | **Alert** |
| HTTP 400 from API | WARN | `review_api_bad_request` | Monitor |
| HTTP 429 from API | ERROR | `review_api_rate_limited` | **Alert** |
| Fetch timeout | WARN | `review_api_timeout` | Monitor |

**4. Cookie health check at job start**

Before starting review extraction, make a single probe request to the review API with a known popular place. If the response is empty, log a warning and skip reviews entirely for the whole job:

```go
// At job start, before scraping places:
if job.Data.ReviewsMax > 0 {
    if !probeReviewAPI(ctx) {
        slog.Error("review_api_probe_failed",
            slog.String("job_id", job.ID),
            slog.String("detail", "review API returned empty for probe — cookies likely expired or IP blocked"),
            slog.String("action", "disabling review extraction for this job"),
        )
        // Set a flag to skip reviews
    }
}
```

---

## Implementation Priority

| # | Fix | Impact | Effort |
|---|-----|--------|--------|
| 1 | `page.URL()` → `j.GetURL()` | Fixes HTTP 400 (wrong place ID) | One line |
| 2 | Detect empty responses + logging | Grafana visibility | Small |
| 3 | Circuit breaker (3 consecutive fails → stop) | Saves 10 min of wasted scraping | Medium |
| 4 | Cookie health probe at job start | Fail-fast before any scraping | Medium |
| 5 | Cookie expiry monitoring/alerting | Proactive ops | Separate task |
