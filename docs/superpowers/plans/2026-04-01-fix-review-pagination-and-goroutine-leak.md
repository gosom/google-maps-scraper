# Fix Review Pagination & Goroutine Leak — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix review scraping (0 reviews stored across entire DB) and gracefully handle goroutine leak on shutdown.

**Architecture:** Root cause confirmed via live API testing: `reviews.go:189` hardcodes `hl=el` (Greek) in the Google Maps review RPC URL. Google returns empty responses for Greek. Fix: use job's language code. Parsers (`extractNextPageToken`, `extractReviews`) are verified correct — they work when given actual data.

**Tech Stack:** Go, Google Maps RPC API, scrapemate, Playwright

---

## Evidence Summary

### Root Cause — CONFIRMED AND VERIFIED

**`gmaps/reviews.go:189` hardcodes `hl=el`:**
```go
"https://www.google.com/maps/rpc/listugcposts?authuser=0&hl=el&pb=%s"
```

| Request | Response | Reviews | Token |
|---------|----------|---------|-------|
| `hl=el` (current code) | 33 bytes: `[null,null,null,null,null,1]` | 0 | none |
| `hl=en` + cookies | 188,172 bytes | 20 (Catalina Ramos ★5, etc.) | 140-char token |

**Standalone Go parser test confirmed:** `extractNextPageToken` returns valid token, `extractReviews` returns 20 parsed reviews — both work perfectly with `hl=en` response data.

**Secondary finding:** Cookies are required for the review API. Without cookies, all languages return empty. The existing `fetchWithCookies` path handles this but the language bug prevented any data from arriving.

### Bug 2: Goroutine leak → false "failed"

Jobs with 57+ valid results are marked "failed" because `mate.Start` goroutine doesn't return within 5 seconds after exit monitor signals completion. Should be treated as success when results exist.

### Bug 3: Error message too vague

"job failed due to a runtime error" should say "job timed out" for timeout/leak failures.

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `gmaps/reviews.go:25-30,162-194` | Add `langCode` to params, use in URL instead of `hl=el` |
| Modify | `gmaps/place.go:468-480` | Pass `j.LangCode` to review fetcher params |
| Modify | `gmaps/place.go:463-510,296-299` | Graceful review failure with `recover()` + structured logging |
| Modify | `gmaps/entry.go:284-311` | `extractReviews` returns error instead of silent nil |
| Modify | `runner/webrunner/webrunner.go` | Goroutine leak → partial success; fix error message |

---

## Task 1: Fix hardcoded `hl=el` (ROOT CAUSE — one-line fix)

**Files:**
- Modify: `gmaps/reviews.go:25-30` (add `langCode` to `fetchReviewsParams`)
- Modify: `gmaps/reviews.go:188-191` (use `langCode` in URL)
- Modify: `gmaps/place.go:474-480` (pass `j.LangCode` to params)

- [ ] **Step 1: Add `langCode` to `fetchReviewsParams`**

In `gmaps/reviews.go`, add field to the struct:
```go
type fetchReviewsParams struct {
    page        playwright.Page
    mapURL      string
    reviewCount int
    maxReviews  int
    langCode    string // language code for the review API (e.g., "en", "de")
}
```

- [ ] **Step 2: Use `langCode` in `generateURL`**

In `gmaps/reviews.go:188-191`, replace hardcoded `hl=el`:
```go
lang := "en"
if f.params.langCode != "" {
    lang = f.params.langCode
}
fullURL := fmt.Sprintf(
    "https://www.google.com/maps/rpc/listugcposts?authuser=0&hl=%s&pb=%s",
    lang, strings.Join(pbComponents, ""),
)
```

- [ ] **Step 3: Pass language from PlaceJob**

In `gmaps/place.go`, where `fetchReviewsParams` is constructed (~line 474):
```go
params := fetchReviewsParams{
    page:        page,
    mapURL:      placeURL,
    reviewCount: reviewCount,
    maxReviews:  j.ReviewsMax,
    langCode:    j.LangCode, // ADD
}
```

Verify `j.LangCode` exists on `PlaceJob` struct — check `gmaps/place.go` and `gmaps/job.go`.

- [ ] **Step 4: Build and verify**

Run: `go build ./...`

- [ ] **Step 5: Commit**

```
fix: use job language code instead of hardcoded hl=el in review API

Google's review RPC returns empty for hl=el (Greek). The language
was hardcoded instead of using the job's configured language.
Verified: hl=en returns 188KB of reviews, hl=el returns 33 bytes.
```

---

## Task 2: Graceful review failure — "Lego piece" isolation

**Goal:** Review extraction should NEVER crash the parent PlaceJob. If reviews fail, log with full context for Grafana/Loki, skip reviews, continue scraping. Even if ALL places fail reviews, the job succeeds with places + images.

**Files:**
- Modify: `gmaps/place.go:463-510` (BrowserActions review block)
- Modify: `gmaps/place.go:296-299` (Process method — AddExtraReviews call)
- Modify: `gmaps/entry.go:284-311` (extractReviews + AddExtraReviews)

**Current gaps:**
- No `recover()` around review parsing — Google format change causes PlaceJob panic, entire place lost
- `AddExtraReviews` can panic — entry with all other data (title, address, images) is lost
- Log messages use scrapemate PlaceJob ID, not the user-facing job ID — unsearchable in Grafana
- `extractReviews` silently returns nil on error — no way to know WHY reviews are missing

- [ ] **Step 1: Wrap review extraction in BrowserActions with recover()**

```go
func() {
    defer func() {
        if r := recover(); r != nil {
            slog.Error("review_extraction_panic",
                slog.String("job_id", j.ID),
                slog.String("parent_job_id", j.ParentID),
                slog.String("place_url", placeURL),
                slog.Any("panic", r),
            )
        }
    }()
    // ... existing review extraction logic ...
}()
```

- [ ] **Step 2: Wrap AddExtraReviews in Process() with recover()**

```go
func() {
    defer func() {
        if r := recover(); r != nil {
            slog.Error("add_extra_reviews_panic",
                slog.String("parent_job_id", j.ParentID),
                slog.String("entry_title", entry.Title),
                slog.Any("panic", r),
            )
        }
    }()
    allReviewsRaw, ok := resp.Meta["reviews_raw"].(fetchReviewsResponse)
    if ok && len(allReviewsRaw.pages) > 0 {
        entry.AddExtraReviews(allReviewsRaw.pages)
    }
}()
```

- [ ] **Step 3: Add `parent_job_id` to ALL review log lines**

Add `slog.String("parent_job_id", j.ParentID)` to: `review_extraction_decision`, `review_extraction_succeeded`, `review_extraction_failed`, and all other review logs. This is the user-facing job ID that support searches in Grafana.

- [ ] **Step 4: Make `extractReviews` return error**

```go
func extractReviews(data []byte) ([]Review, error) {
    // ... unmarshal ...
    reviewsI := getNthElementAndCast[[]any](jd, 2)
    if len(reviewsI) == 0 {
        return nil, fmt.Errorf("no reviews at index 2 (array length: %d)", len(jd))
    }
    return parseReviews(reviewsI), nil
}
```

Update `AddExtraReviews` to log per-page failures and continue:
```go
func (e *Entry) AddExtraReviews(pages [][]byte) {
    for i, page := range pages {
        reviews, err := extractReviews(page)
        if err != nil {
            slog.Warn("review_page_parse_failed",
                slog.Int("page", i+1),
                slog.Int("total_pages", len(pages)),
                slog.Any("error", err),
            )
            continue
        }
        e.UserReviewsExtended = append(e.UserReviewsExtended, reviews...)
    }
}
```

- [ ] **Step 5: Build and test**
- [ ] **Step 6: Commit**

---

## Task 3: Goroutine leak → partial success + fix error message

**Files:**
- Modify: `runner/webrunner/webrunner.go`

- [ ] **Step 1: In the forced-completion path, check for results before marking failed**

When `shutdownMate` returns a leak after the exit monitor fired, count results. If results exist, treat as success:

```go
case <-forcedCompletionCh:
    mateErr, leaked := w.shutdownMate(job.ID, cancel, closeMate, resultCh)
    if leaked {
        var resultCount int
        if w.db != nil {
            countCtx, countCancel := context.WithTimeout(context.Background(), 10*time.Second)
            w.db.QueryRowContext(countCtx, `SELECT COUNT(*) FROM results WHERE job_id=$1`, job.ID).Scan(&resultCount)
            countCancel()
        }
        if resultCount > 0 {
            w.logger.Info("goroutine_leaked_but_results_exist",
                slog.String("job_id", job.ID),
                slog.Int("result_count", resultCount),
            )
            err = nil // treat as success
        } else {
            err = mateErr
        }
    } else {
        err = mateErr
    }
```

- [ ] **Step 2: Fix the vague error message**

In the runtime error failure path:
```go
if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "leaked") {
    job.FailureReason = "job timed out"
} else {
    job.FailureReason = "job failed due to a runtime error"
}
```

- [ ] **Step 3: Build and verify**
- [ ] **Step 4: Commit**

---

## Task 4: End-to-end verification

- [ ] **Step 1: Run a job with depth=20, reviews_max=9999, all enrichments**
- [ ] **Step 2: Check logs: `grep parent_job_id=<id>` shows full review context**
- [ ] **Step 3: Verify `user_reviews_extended` has data in results table**
- [ ] **Step 4: Verify job completes as "ok" even if goroutine leaks**
- [ ] **Step 5: If job does fail, verify message says "timed out" not "runtime error"**

---

## Dependency & Parallelism

- **Task 1** (root cause fix) — independent, do first
- **Task 2** (graceful failure) — independent, can parallel with Task 1
- **Task 3** (goroutine leak) — independent, can parallel with Task 1
- **Task 4** (verification) — depends on all
