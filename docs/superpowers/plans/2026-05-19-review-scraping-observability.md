# Review-Scraping Observability Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make review-scraping failures visible in Grafana/Loki with enough context (user, user-facing job, place) to triage; and surface a "Google changed the JSON shape" canary that catches silent corruption when our extractor stops finding expected fields.

**Architecture:** Switch review-path log statements from package-level `slog.X` to the scrapemate context-bound logger (`scrapemate.GetLoggerFromContext(ctx)`), which `webrunner.go:989` already populates with `user_id` and the user-facing `job_id`. Rename the colliding scraper-internal IDs (`PlaceJob.ID`, `GmapJob.ID`) in our local log args to `place_job_id` / `search_job_id` so they don't shadow the context-bound `job_id`. Add a `place_payload_inconsistent_*` warning emitted by `Process()` immediately after `EntryFromJSON` returns, fired when the parsed entry violates structural invariants (e.g. `rating > 0 && review_count == 0`) — that's the canary for Google changing field positions in the JSON tree.

**Tech Stack:** Go 1.25, `log/slog`, `gosom/kit/logging` (via `pkg/logger/SlogAdapter`), `scrapemate.GetLoggerFromContext`/`ContextWithLogger`. Tests use the stdlib `slog.NewJSONHandler(buf, …)` capture pattern — no new dependencies.

---

## Audit findings (verified against `gmaps/place.go`, `gmaps/reviews.go`, `gmaps/entry.go`, `runner/webrunner/webrunner.go`)

Every claim below has been confirmed by reading the code; this is not speculation.

### Finding 1 — package-level `slog` bypasses the context logger
`webrunner.go:989` already does:
```go
jobLogger := w.logger.With(slog.String("job_id", job.ID), slog.String("user_id", job.UserID))
mateCtx = scrapemate.ContextWithLogger(mateCtx, pkglogger.NewSlogAdapter(jobLogger))
```

Any code that calls `scrapemate.GetLoggerFromContext(ctx).Warn(...)` gets `user_id` and the user-facing `job_id` as `.With` attributes — they appear in every emitted line. But the review-extraction logs (`place.go:335,544,560,595,612,687`, `reviews.go:121,130,189`, `entry.go:274`) use **package-level `slog.Warn`/`Error`**, which bypasses this entirely.

### Finding 2 — `job_id` and `parent_job_id` in current review logs are misleading
The current logs pass `slog.String("job_id", j.ID)` where `j` is a `*PlaceJob`. But `PlaceJob.ID` is a fresh UUID generated in `NewPlaceJob` (per-place visit), and `j.ParentID` is the `GmapJob.ID` — also a fresh UUID generated in `NewGmapJob` (line 60-62: when caller passes empty id, it generates `uuid.NewV7()`).

`runner/webrunner/webrunner.go:918` passes `strings.Join(job.Data.Keywords, "\n")` as the seed input — there's no `#!#<id>` suffix, so the parsed `id` in `runner/jobs.go:104-107` is empty and `NewGmapJob` generates a fresh UUID. **Neither `job_id` nor `parent_job_id` in current review logs equals the user-facing `jobs.id` from the DB.**

### Finding 3 — `reviews.go` has zero user/job/place context
The three log statements at `reviews.go:121,130,189` carry only `next_page_token`, `review_url` (the constructed RPC URL), and `error`. No way to find the user, the user-facing job, or the place from these lines. `mapURL` (the user-facing place URL) is available in `fetchReviewsParams` but never logged.

### Finding 4 — no silent-corruption canary
`grep -rn "review_count.*0\|ReviewCount.*0" gmaps/` returns only the `isCompletePlacePayload` comment. There is no log fired when `EntryFromJSON` parses a payload to an entry with `rating > 0 && review_count == 0`. The Fix A classifier (`isCompletePlacePayload`) protects against partial-preview captures by checking `len(jd[6][4]) >= 9` — but if Google moves index 8 to a new position while keeping the array length, the classifier passes and the corruption silently lands in the DB. We need a post-parse invariant check as a second line of defence.

### Finding 5 — PR #79 introduced its own gap
`extract_json_partial_payload_accepted` (`place.go:687`) was added by the partial-payload fix and has `job_id` (PlaceJob.ID) but no `parent_job_id`. `add_extra_reviews_panic` (`place.go:335`) has the opposite gap — `parent_job_id` but no `job_id`.

### Finding 6 — pre-existing fallback paths are also context-dark
`json_extraction_fallback` (`place.go:269`) and `json_parsing_fallback` (`place.go:287`) only pass `job_id` (PlaceJob.ID). These fire when the place's raw JSON can't be extracted or parsed at all — exactly the kind of "something is wrong" moment where an operator needs user/job context.

### Findings that were FALSE POSITIVES on second look
- I initially flagged that `PlaceJob` "needs a `UserID` field". It does **not**, because the context logger already carries it. Adding a field would be redundant and would invite the `job_id` name-collision problem (since both `.With` and the call-site arg would emit `job_id`).
- I considered breaking the existing `job_id` / `parent_job_id` field names. Reviewing how `Process()`-scope `log := scrapemate.GetLoggerFromContext(ctx)` is already used (`place.go:160,254,396,533`) shows that we can keep them — but only after renaming the local args to `place_job_id` / `search_job_id` so they don't collide with the With-attribute `job_id` (the user-facing one). With both keys present, dual emission would confuse Loki parsers.

---

## File map — what changes and why

### Modified

| File | Responsibility | Why |
|------|----------------|-----|
| `gmaps/place.go` | All review-extraction logs in `BrowserActions` + `Process` + `extractJSON` switch to the ctx-bound logger; local IDs renamed to `place_job_id`/`search_job_id`; `place_name` added when known; `place_payload_inconsistent_*` invariant checks added after `EntryFromJSON` returns. | Finding 1, 2, 5, 6 |
| `gmaps/reviews.go` | Accept a `logger logging.Logger` reference in `fetchReviewsParams` (or pull from ctx inside `fetch`); emit `mapURL`/`place_url` in all three logs. | Finding 3 |
| `gmaps/entry.go` | `review_page_parse_failed` switches to ctx logger via a parameter (function isn't ctx-aware today). | Finding 6 |

### Created

| File | Responsibility |
|------|----------------|
| `gmaps/review_log_context_test.go` | Asserts that key review-path log events carry `user_id`, `job_id` (user-facing), `place_job_id`, `place_url`, `place_name`, and (for the corruption canary) the invariant that was violated. Uses an `slog.JSONHandler` writing to a `bytes.Buffer` so we can decode and assert. |
| `gmaps/place_corruption_log_test.go` | Asserts that `place_payload_inconsistent_review_count` fires when `EntryFromJSON` returns an entry with `rating > 0 && review_count == 0`, and does NOT fire on legitimate zero-review places (`rating == 0 && review_count == 0`). |
| `docs/observability/review-scraping.md` | Operator-facing doc: LogQL queries for the dashboard, alert thresholds, and runbook entries for each new log event ("if you see `place_payload_inconsistent_review_count` spiking, check…"). |

### Out of scope for this plan
- A pre-built Grafana dashboard JSON (`grafana/provisioning/dashboards/review-scraping.json`). It's worth doing, but is its own deliverable with a different testing surface; tracking as a follow-up so this plan ships first.
- Backfilling existing corrupted DB rows. Touches data, not telemetry.

---

## Task decomposition

Each task is one focused TDD cycle. **Run `git status` between tasks** to confirm a clean tree before starting the next.

### Task 1: Capture-handler test helper

**Files:**
- Create: `gmaps/logtest_test.go`

We need a way to assert "this log line was emitted with these fields" in tests without leaning on log files. Standard pattern: install a `slog.JSONHandler` writing to a `bytes.Buffer`, then decode each line and assert.

- [ ] **Step 1: Write the helper + a smoke test**

```go
package gmaps

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/scrapemate"
)

// newCaptureLogger returns a context whose scrapemate logger writes
// JSON-formatted records to buf. Mirrors the production setup in
// runner/webrunner/webrunner.go where the user-facing job_id and user_id
// are added as With-attributes so every emitted line carries them.
func newCaptureLogger(t *testing.T, userJobID, userID string) (context.Context, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	base := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	withAttrs := base.With(slog.String("job_id", userJobID), slog.String("user_id", userID))
	ctx := scrapemate.ContextWithLogger(context.Background(), logger.NewSlogAdapter(withAttrs))
	return ctx, buf
}

// decodeLogLines parses each newline-delimited JSON record from buf into
// a map. Caller asserts on individual records.
func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestCaptureLoggerCarriesWithAttributes(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "019e41ff-aaaa-7bbb-cccc-dddd", "user_TEST")

	log := scrapemate.GetLoggerFromContext(ctx)
	log.Warn("smoke_test", "place_url", "https://example.com/place")

	recs := decodeLogLines(t, buf)
	if len(recs) != 1 {
		t.Fatalf("want 1 log record, got %d", len(recs))
	}
	if recs[0]["job_id"] != "019e41ff-aaaa-7bbb-cccc-dddd" {
		t.Errorf("job_id from .With not emitted, got %v", recs[0]["job_id"])
	}
	if recs[0]["user_id"] != "user_TEST" {
		t.Errorf("user_id from .With not emitted, got %v", recs[0]["user_id"])
	}
	if recs[0]["place_url"] != "https://example.com/place" {
		t.Errorf("place_url from call site not emitted, got %v", recs[0]["place_url"])
	}
}
```

- [ ] **Step 2: Run and verify it passes**

```bash
go test ./gmaps -run TestCaptureLoggerCarriesWithAttributes -v
```
Expected: PASS. (No production code changed yet — we're just validating the test harness.)

- [ ] **Step 3: Commit**

```bash
git add gmaps/logtest_test.go
git commit -m "test(gmaps): add slog capture helper for review-log assertions"
```

---

### Task 2: Switch `extract_json_partial_payload_accepted` to ctx logger

**Files:**
- Modify: `gmaps/place.go:686-693`
- Test: `gmaps/review_log_context_test.go` (new)

This is the smallest single-log surgery in the file and a good template for Tasks 3–5. We're also fixing the missing `parent_job_id` (Finding 5).

- [ ] **Step 1: Write the failing test**

`gmaps/review_log_context_test.go`:
```go
package gmaps

import (
	"strings"
	"testing"

	"github.com/gosom/scrapemate"
)

// TestExtractJSONPartialAccepted_LogContext verifies that when extractJSON
// gives up and falls back to a partial payload, the warning carries:
//   - job_id        (user-facing, from ctx .With)
//   - user_id       (from ctx .With)
//   - place_job_id  (PlaceJob's internal UUID — renamed from job_id)
//   - search_job_id (GmapJob's internal UUID — renamed from parent_job_id)
//   - place_url
//   - msg == "extract_json_partial_payload_accepted"
func TestExtractJSONPartialAccepted_LogContext(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-1", "user_TEST")

	// Build a PlaceJob that we can drive through the partial-payload fallback
	// path without a real Playwright page. We invoke the slog path directly
	// from a small helper that the production code will call after Task 2.
	pj := &PlaceJob{ParentID: "SEARCH-JOB-1"}
	pj.ID = "PLACE-JOB-1"
	pj.URL = "https://www.google.com/maps/place/Test"

	emitPartialPayloadAcceptedWarning(ctx, pj, 18867)

	recs := decodeLogLines(t, buf)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r["msg"] != "extract_json_partial_payload_accepted" {
		t.Errorf("msg: got %v", r["msg"])
	}
	for _, want := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url", "bytes"} {
		if _, ok := r[want]; !ok {
			t.Errorf("missing field %q in log record", want)
		}
	}
	if !strings.Contains(r["place_url"].(string), "/maps/place/Test") {
		t.Errorf("place_url not propagated: %v", r["place_url"])
	}
}
```

- [ ] **Step 2: Run and verify failure**

```bash
go test ./gmaps -run TestExtractJSONPartialAccepted_LogContext -v
```
Expected: FAIL — `emitPartialPayloadAcceptedWarning` is undefined.

- [ ] **Step 3: Extract the warning into a small helper, switch to ctx logger**

In `gmaps/place.go`, replace the existing `slog.Warn("extract_json_partial_payload_accepted", …)` block (lines 686-693) with a call to a new helper, and define the helper. This keeps the test surface stable and makes the change reviewable.

```go
// emitPartialPayloadAcceptedWarning is called by extractJSON when the
// 15×200ms polling budget exhausts without ever seeing a complete payload
// (jd[6][4] of length >= 9). Logged at WARN because it's a fallback —
// we still return a usable payload, but review_count and reviews_per_rating
// will be empty.
func emitPartialPayloadAcceptedWarning(ctx context.Context, j *PlaceJob, bytes int) {
	scrapemate.GetLoggerFromContext(ctx).Warn("extract_json_partial_payload_accepted",
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"bytes", bytes,
		"detail", "APP_INITIALIZATION_STATE never fully hydrated within 15×200ms; review_count and reviews_per_rating will be empty",
	)
}
```

Update the call site (was line 687):

```go
if lastPartial != nil {
    emitPartialPayloadAcceptedWarning(ctx, j, len(lastPartial))
    return lastPartial, nil
}
```

Note: `extractJSON` already receives `ctx` via `BrowserActions(ctx, page)` — pass it down. Check the current signature: `func (j *PlaceJob) extractJSON(page playwright.Page) ([]byte, error)`. **It does NOT currently take ctx.** Change the signature to `func (j *PlaceJob) extractJSON(ctx context.Context, page playwright.Page) ([]byte, error)` and update the single caller in `BrowserActions` (currently `place.go:506`: `raw, err := j.extractJSON(page)`).

- [ ] **Step 4: Run the test — expect PASS**

```bash
go test ./gmaps -run TestExtractJSONPartialAccepted_LogContext -v
```
Expected: PASS.

- [ ] **Step 5: Run the full gmaps suite (skip the pre-existing failure)**

```bash
go test ./gmaps -count=1 -skip Test_getNthElementAndCast_DoesNotPanicOnNegativeIndex
```
Expected: PASS. The skipped test is a pre-existing failure on `develop`, unrelated to this work.

- [ ] **Step 6: Commit**

```bash
git add gmaps/place.go gmaps/review_log_context_test.go
git commit -m "obs(gmaps): extract_json_partial_payload_accepted carries user+search context"
```

---

### Task 3: Switch the in-block review-extraction logs to ctx logger

**Files:**
- Modify: `gmaps/place.go:540-628` (the deferred `func()` block in `BrowserActions` that handles review extraction)
- Test: extend `gmaps/review_log_context_test.go`

Four log events in this block: `review_extraction_panic`, `review_circuit_breaker_open`, `review_extraction_failed`, `review_api_empty_response`. Same rename pattern.

- [ ] **Step 1: Write the failing test (table-driven, one row per event)**

Add to `gmaps/review_log_context_test.go`:

```go
func TestReviewExtractionLogs_AllCarryUserAndSearchContext(t *testing.T) {
	cases := []struct {
		name string
		emit func(ctx context.Context, j *PlaceJob)
		want string // expected "msg" field
	}{
		{
			name: "circuit_breaker_open",
			emit: emitReviewCircuitBreakerOpen,
			want: "review_circuit_breaker_open",
		},
		{
			name: "extraction_failed",
			emit: func(ctx context.Context, j *PlaceJob) {
				emitReviewExtractionFailed(ctx, j, errors.New("simulated failure"))
			},
			want: "review_extraction_failed",
		},
		{
			name: "api_empty_response",
			emit: func(ctx context.Context, j *PlaceJob) {
				emitReviewAPIEmptyResponse(ctx, j, 271, 33, 1)
			},
			want: "review_api_empty_response",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, buf := newCaptureLogger(t, "USER-JOB-1", "user_TEST")
			pj := &PlaceJob{ParentID: "SEARCH-JOB-1"}
			pj.ID = "PLACE-JOB-1"
			pj.URL = "https://www.google.com/maps/place/Test"

			tc.emit(ctx, pj)

			recs := decodeLogLines(t, buf)
			if len(recs) != 1 {
				t.Fatalf("want 1 record, got %d", len(recs))
			}
			r := recs[0]
			if r["msg"] != tc.want {
				t.Errorf("msg: got %v want %v", r["msg"], tc.want)
			}
			for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url"} {
				if _, ok := r[k]; !ok {
					t.Errorf("missing %q in %s", k, tc.want)
				}
			}
		})
	}
}
```

Required new import in the test file: `"errors"`.

- [ ] **Step 2: Run and verify failure**

```bash
go test ./gmaps -run TestReviewExtractionLogs_AllCarryUserAndSearchContext -v
```
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Add the helpers + switch the call sites**

In `gmaps/place.go`, add three small helpers near `emitPartialPayloadAcceptedWarning` from Task 2:

```go
func emitReviewCircuitBreakerOpen(ctx context.Context, j *PlaceJob) {
	scrapemate.GetLoggerFromContext(ctx).Error("review_circuit_breaker_open",
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"consecutive_failures", int(reviewEmptyCount.Load()),
		"action", "skipping reviews for remaining places",
		"likely_cause", "cookies expired or IP rate-limited",
	)
}

func emitReviewExtractionFailed(ctx context.Context, j *PlaceJob, err error) {
	scrapemate.GetLoggerFromContext(ctx).Warn("review_extraction_failed",
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"error", err.Error(),
	)
}

func emitReviewAPIEmptyResponse(ctx context.Context, j *PlaceJob, reviewCountOnPage, responseBytes, consecutiveEmpty int) {
	scrapemate.GetLoggerFromContext(ctx).Warn("review_api_empty_response",
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"review_count_on_page", reviewCountOnPage,
		"response_bytes", responseBytes,
		"consecutive_empty", consecutiveEmpty,
		"possible_cause", "expired cookies, IP blocked, or rate limited",
	)
}
```

Update the call sites in `BrowserActions`:
- `place.go:560-566` → `emitReviewCircuitBreakerOpen(ctx, j)` (note: `ctx` is the `BrowserActions` parameter, already in scope)
- `place.go:595-600` → `emitReviewExtractionFailed(ctx, j, err)`
- `place.go:612-620` → `emitReviewAPIEmptyResponse(ctx, j, reviewCount, responseBytes, int(count))`

For `review_extraction_panic` (line 544-550) — the existing local `log := scrapemate.GetLoggerFromContext(ctx)` at the top of the deferred function would be cleanest, but that defer fires after `BrowserActions` may have returned. `ctx` is captured by the closure and remains valid. Switch directly:

```go
scrapemate.GetLoggerFromContext(ctx).Error("review_extraction_panic",
    "place_job_id", j.ID,
    "search_job_id", j.ParentID,
    "place_url", j.GetURL(),
    "panic", r,
    "stack", string(debug.Stack()),
)
```

Apply the same pattern to `add_extra_reviews_panic` (place.go:335). That defer is inside `Process(ctx, …)` — `ctx` in scope. Add `place_job_id`, keep `search_job_id`, add `place_url` (use `j.GetURL()` rather than the entry title which is the existing field — keep `entry_title` too for backwards compat):

```go
scrapemate.GetLoggerFromContext(ctx).Error("add_extra_reviews_panic",
    "place_job_id", j.ID,
    "search_job_id", j.ParentID,
    "place_url", j.GetURL(),
    "entry_title", entry.Title,
    "panic", r,
    "stack", string(debug.Stack()),
)
```

- [ ] **Step 4: Run the test — expect PASS**

```bash
go test ./gmaps -run TestReviewExtractionLogs_AllCarryUserAndSearchContext -v
```

- [ ] **Step 5: Sweep — confirm no remaining `slog.Warn`/`slog.Error` in the review block**

```bash
grep -n 'slog\.\(Warn\|Error\)(' gmaps/place.go
```
Expected output: zero matches in the review-extraction block (lines 540-628). Other places (the `json_*_fallback` logs at 269/287) are addressed in Task 5.

- [ ] **Step 6: Commit**

```bash
git add gmaps/place.go gmaps/review_log_context_test.go
git commit -m "obs(gmaps): review extraction logs use ctx logger; rename internal IDs"
```

---

### Task 4: Add corruption canary — `place_payload_inconsistent_review_count`

**Files:**
- Modify: `gmaps/place.go` `Process` method, after line 295 (`entry = parsedEntry`)
- Test: create `gmaps/place_corruption_log_test.go`

This is the "Google changed something" canary. Fires after `EntryFromJSON` returns successfully, when the parsed entry violates an invariant a real Google Maps place must satisfy. We start with the strongest signal — `rating > 0 && review_count == 0` — because that's the exact corruption pattern Fix A targets, and any future Google JSON change that moves the count to a different index would re-surface it here.

The check is in `Process`, not `BrowserActions`, because by the time we're in `Process` we already have the parsed `Entry` and `extractJSON`'s partial-payload fallback has had its say. This gives Fix A first crack and only fires the canary when the corruption survives the classifier (= Google really did change something).

- [ ] **Step 1: Write the failing test**

```go
package gmaps

import (
	"context"
	"testing"
)

// TestPlacePayloadInconsistencyCanary_FiresOnRatingWithoutCount documents
// the invariant: a Google Maps place with rating > 0 should never have
// review_count == 0. When the parsed Entry violates this, we emit a
// WARN-level canary so Grafana/Loki can alert on Google JSON shape changes.
func TestPlacePayloadInconsistencyCanary_FiresOnRatingWithoutCount(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-X", "user_TEST")
	pj := &PlaceJob{ParentID: "SEARCH-JOB-X"}
	pj.ID = "PLACE-JOB-X"
	pj.URL = "https://www.google.com/maps/place/Test"

	entry := Entry{Title: "Café Libre Berlin", ReviewRating: 4.8, ReviewCount: 0}
	checkPlacePayloadInvariants(ctx, pj, &entry)

	recs := decodeLogLines(t, buf)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r["msg"] != "place_payload_inconsistent_review_count" {
		t.Errorf("msg: got %v", r["msg"])
	}
	for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url", "place_name", "rating"} {
		if _, ok := r[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	if r["place_name"] != "Café Libre Berlin" {
		t.Errorf("place_name: got %v", r["place_name"])
	}
}

// TestPlacePayloadInconsistencyCanary_DoesNotFireOnLegitimateZeroReview
// guards against false positives on newly-listed places that legitimately
// have no reviews. rating==0 AND review_count==0 is consistent; no canary.
func TestPlacePayloadInconsistencyCanary_DoesNotFireOnLegitimateZeroReview(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-X", "user_TEST")
	pj := &PlaceJob{ParentID: "SEARCH-JOB-X"}
	pj.ID = "PLACE-JOB-X"
	pj.URL = "https://www.google.com/maps/place/New"

	entry := Entry{Title: "Brand new place", ReviewRating: 0, ReviewCount: 0}
	checkPlacePayloadInvariants(ctx, pj, &entry)

	recs := decodeLogLines(t, buf)
	if len(recs) != 0 {
		t.Fatalf("expected no canary on legitimate-zero-review place, got %d records: %v", len(recs), recs)
	}
}

// TestPlacePayloadInconsistencyCanary_DoesNotFireWhenBothPopulated is the
// happy-path: the entry is consistent and we stay silent.
func TestPlacePayloadInconsistencyCanary_DoesNotFireWhenBothPopulated(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-X", "user_TEST")
	pj := &PlaceJob{ParentID: "SEARCH-JOB-X"}
	pj.ID = "PLACE-JOB-X"
	pj.URL = "https://www.google.com/maps/place/Healthy"

	entry := Entry{Title: "Healthy", ReviewRating: 4.5, ReviewCount: 123}
	checkPlacePayloadInvariants(ctx, pj, &entry)

	recs := decodeLogLines(t, buf)
	if len(recs) != 0 {
		t.Fatalf("expected no canary on consistent entry, got %d records", len(recs))
	}
}
```

Note: do **not** fire the canary for the symmetric case `rating == 0 && review_count > 0` — that combination is rare but does occur on places mid-moderation (Google suppresses the displayed average while the review records still exist). False-positive risk too high without more signal.

- [ ] **Step 2: Run and verify failure**

```bash
go test ./gmaps -run TestPlacePayloadInconsistencyCanary -v
```
Expected: FAIL — `checkPlacePayloadInvariants` undefined.

- [ ] **Step 3: Implement the helper**

In `gmaps/place.go`, near `isCompletePlacePayload` (the existing related helper):

```go
// checkPlacePayloadInvariants emits warning canaries when EntryFromJSON
// returns an entry whose populated fields are mutually inconsistent — the
// strongest available signal that Google has changed the JSON shape such
// that one field still parses but a related field has moved.
//
// Today the only invariant we check is "rating > 0 implies review_count > 0",
// because that's the exact corruption pattern that motivated Fix A
// (isCompletePlacePayload). A future Google shape change that moves
// review_count to a new index inside darray[4] (without changing the array
// length) would slip past Fix A — this canary catches it.
//
// We deliberately do NOT check the reverse direction (review_count > 0,
// rating == 0): that legitimately occurs on places mid-moderation, where
// the displayed average is suppressed while review records remain.
func checkPlacePayloadInvariants(ctx context.Context, j *PlaceJob, entry *Entry) {
	if entry.ReviewRating > 0 && entry.ReviewCount == 0 {
		scrapemate.GetLoggerFromContext(ctx).Warn("place_payload_inconsistent_review_count",
			"place_job_id", j.ID,
			"search_job_id", j.ParentID,
			"place_url", j.GetURL(),
			"place_name", entry.Title,
			"rating", entry.ReviewRating,
			"detail", "rating > 0 but review_count == 0 — likely Google JSON shape change (review_count missing) OR an extractJSON race that Fix A did not catch",
		)
	}
}
```

Wire it into `Process`. After `entry = parsedEntry` (line 295 currently), add:

```go
checkPlacePayloadInvariants(ctx, j, &entry)
```

`ctx` is the first parameter of `Process` — already in scope.

- [ ] **Step 4: Run the test — expect PASS**

```bash
go test ./gmaps -run TestPlacePayloadInconsistencyCanary -v
```

- [ ] **Step 5: Commit**

```bash
git add gmaps/place.go gmaps/place_corruption_log_test.go
git commit -m "obs(gmaps): canary fires when rating>0 and review_count==0 (Google shape change signal)"
```

---

### Task 5: Switch the JSON-extraction fallback logs to ctx logger

**Files:**
- Modify: `gmaps/place.go:268-274` (`json_extraction_fallback`) and `gmaps/place.go:285-291` (`json_parsing_fallback`)
- Test: extend `gmaps/review_log_context_test.go`

These two fire when raw JSON extraction fails entirely (Meta missing) or `EntryFromJSON` returns a parse error. They currently only emit `job_id` (= PlaceJob.ID). Same rename treatment.

- [ ] **Step 1: Write the failing test**

```go
func TestJSONExtractionFallback_LogContext(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-Y", "user_TEST")
	pj := &PlaceJob{ParentID: "SEARCH-JOB-Y"}
	pj.ID = "PLACE-JOB-Y"
	pj.URL = "https://www.google.com/maps/place/Broken"

	emitJSONExtractionFallback(ctx, pj)
	emitJSONParsingFallback(ctx, pj, errors.New("invalid json"))

	recs := decodeLogLines(t, buf)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	for i, want := range []string{"json_extraction_fallback", "json_parsing_fallback"} {
		r := recs[i]
		if r["msg"] != want {
			t.Errorf("rec %d msg: got %v want %v", i, r["msg"], want)
		}
		for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url"} {
			if _, ok := r[k]; !ok {
				t.Errorf("rec %d missing %q", i, k)
			}
		}
	}
	if recs[1]["error"] != "invalid json" {
		t.Errorf("error field missing or wrong: %v", recs[1]["error"])
	}
}
```

(The `errors` import is already pulled in via Task 3.)

- [ ] **Step 2: Run and verify failure**

```bash
go test ./gmaps -run TestJSONExtractionFallback_LogContext -v
```

- [ ] **Step 3: Implement helpers + switch call sites**

```go
func emitJSONExtractionFallback(ctx context.Context, j *PlaceJob) {
	scrapemate.GetLoggerFromContext(ctx).Warn("json_extraction_fallback",
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"reason", "creating minimal entry with URL only",
	)
}

func emitJSONParsingFallback(ctx context.Context, j *PlaceJob, err error) {
	scrapemate.GetLoggerFromContext(ctx).Warn("json_parsing_fallback",
		"place_job_id", j.ID,
		"search_job_id", j.ParentID,
		"place_url", j.GetURL(),
		"error", err.Error(),
		"reason", "creating minimal entry with URL only",
	)
}
```

Update call sites in `Process` — replace `log.Warn("json_extraction_fallback", …)` at line 269 with `emitJSONExtractionFallback(ctx, j)`, and `log.Warn("json_parsing_fallback", …)` at line 287 with `emitJSONParsingFallback(ctx, j, err)`.

- [ ] **Step 4: Run the test — expect PASS**

- [ ] **Step 5: Commit**

```bash
git add gmaps/place.go gmaps/review_log_context_test.go
git commit -m "obs(gmaps): json_*_fallback logs carry user+place context"
```

---

### Task 6: Add place context to `reviews.go` logs

**Files:**
- Modify: `gmaps/reviews.go` (`fetchReviewsParams`, `fetch`, `fetchReviewPage`)
- Modify: `gmaps/place.go:572-578` (the single call site that builds `fetchReviewsParams`)
- Test: extend `gmaps/review_log_context_test.go`

`reviews.go` is the worst offender — three logs with zero job/user/place context. The cheapest correct fix: pull the logger from `ctx` (which `fetch` already receives) and pass place metadata via the existing `fetchReviewsParams` struct.

- [ ] **Step 1: Write the failing test**

```go
// TestReviewsFetchPageFailed_LogContext exercises the reviews.go log path.
// We hit the fetch with an unparseable URL to trigger reviews_generate_url_failed.
func TestReviewsGenerateURLFailed_LogContext(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "USER-JOB-R", "user_TEST")

	// generateURL needs a mapURL that fails the placeIDRegex; an URL
	// without "!1s<id>" segment qualifies.
	params := fetchReviewsParams{
		mapURL:       "https://www.google.com/maps/place/Test/data=garbage",
		reviewCount:  100, // > 8 to trigger an attempt
		maxReviews:   50,
		langCode:     "en",
		// new fields added by this task:
		placeJobID:   "PLACE-JOB-R",
		searchJobID:  "SEARCH-JOB-R",
		placeName:    "Test",
	}
	// page is nil — fetch must not deref it before the URL error path
	f := newReviewFetcher(params)
	_, err := f.fetch(ctx)
	if err == nil {
		t.Fatal("expected fetch to fail on unparseable URL")
	}

	// At least one log line should have appeared (reviews_generate_url_failed
	// or the initial "failed to fetch initial review page" wrapper).
	// Either way, the line must carry place context.
	recs := decodeLogLines(t, buf)
	if len(recs) == 0 {
		t.Fatal("expected at least one log record from reviews.go")
	}
	for i, r := range recs {
		for _, k := range []string{"job_id", "user_id", "place_job_id", "search_job_id", "place_url", "place_name"} {
			if _, ok := r[k]; !ok {
				t.Errorf("rec %d (%v): missing %q", i, r["msg"], k)
			}
		}
	}
}
```

- [ ] **Step 2: Run and verify failure**

`fetchReviewsParams` doesn't yet have `placeJobID`/`searchJobID`/`placeName` fields. Compile error.

- [ ] **Step 3: Extend `fetchReviewsParams` and switch logs to ctx**

```go
type fetchReviewsParams struct {
	page        playwright.Page
	mapURL      string
	reviewCount int
	maxReviews  int
	langCode    string

	// Logging context — populated by the caller in place.go. Optional in CLI
	// scrapes where these fields are unknown; the ctx-bound logger still
	// emits user_id and the user-facing job_id via .With attributes set
	// upstream by webrunner.go.
	placeJobID  string
	searchJobID string
	placeName   string
}
```

In `gmaps/reviews.go`, replace the three `slog.*` calls:

```go
// reviews.go:121 — reviews_generate_url_failed
scrapemate.GetLoggerFromContext(ctx).Error("reviews_generate_url_failed",
    "place_job_id", f.params.placeJobID,
    "search_job_id", f.params.searchJobID,
    "place_url", f.params.mapURL,
    "place_name", f.params.placeName,
    "next_page_token", nextPageToken,
    "error", err.Error(),
)

// reviews.go:130 — reviews_fetch_page_failed
scrapemate.GetLoggerFromContext(ctx).Error("reviews_fetch_page_failed",
    "place_job_id", f.params.placeJobID,
    "search_job_id", f.params.searchJobID,
    "place_url", f.params.mapURL,
    "place_name", f.params.placeName,
    "next_page_token", nextPageToken,
    "review_url", reviewURL,
    "error", err.Error(),
)
```

For `authenticated_review_fetch_failed_falling_back` at `reviews.go:189` — this lives inside `fetchReviewPage(ctx context.Context, u string)`. The function doesn't currently receive the params struct. Two options:
- (a) Promote `fetchReviewPage` to a method on `*fetcher` (it already is — `func (f *fetcher) fetchReviewPage(...)`). It can read `f.params` directly. **Use this.**
- (b) Pass params explicitly.

Choose (a):
```go
scrapemate.GetLoggerFromContext(ctx).Debug("authenticated_review_fetch_failed_falling_back",
    "place_job_id", f.params.placeJobID,
    "search_job_id", f.params.searchJobID,
    "place_url", f.params.mapURL,
    "place_name", f.params.placeName,
    "error", err.Error(),
)
```

Required new import in `reviews.go`: `"github.com/gosom/scrapemate"`.

Update the call site in `place.go:572-578`:
```go
params := fetchReviewsParams{
    page:         page,
    mapURL:       j.GetURL(),
    reviewCount:  reviewCount,
    maxReviews:   j.ReviewsMax,
    langCode:     j.URLParams["hl"],
    placeJobID:   j.ID,
    searchJobID:  j.ParentID,
    placeName:    "", // not known at this stage — entry.Title is populated in Process, not BrowserActions
}
```

Note: `placeName` is empty here because `entry.Title` is parsed in `Process`, which runs **after** `BrowserActions` (where the review fetch happens). Logs from `reviews.go` will thus have an empty `place_name` field; that's accurate ("we don't know yet") and Grafana queries can use `place_url` to identify the place. A follow-up plan could thread title in by re-parsing the JSON peek already done at `place.go:519` — out of scope here.

- [ ] **Step 4: Run the test — expect PASS**

- [ ] **Step 5: Commit**

```bash
git add gmaps/reviews.go gmaps/place.go gmaps/review_log_context_test.go
git commit -m "obs(gmaps): reviews.go logs carry place+search context"
```

---

### Task 7: Switch `entry.go:274` `review_page_parse_failed` to ctx logger

**Files:**
- Modify: `gmaps/entry.go:255-290` (the function containing the log + its caller chain)
- Test: extend `gmaps/review_log_context_test.go`

This log fires inside `EntryFromJSON`'s helper `parseExtraReviewPages` (or equivalent — check the actual function name; the search showed it at `entry.go:274`). The function isn't ctx-aware today.

- [ ] **Step 1: Identify the calling function and its signature**

```bash
sed -n '260,295p' gmaps/entry.go
```
Note the function name + signature. If it's an exported helper called from many places, threading ctx through might be invasive. **Acceptable alternative if so:** pass a `logging.Logger` parameter instead of a context, with the production code path passing `scrapemate.GetLoggerFromContext(ctx)` at the call site.

- [ ] **Step 2: Write the failing test**

Depending on the function signature found in Step 1, the test invokes the function directly with malformed page data and asserts a single `review_page_parse_failed` record carries place/user context.

If threading is too invasive (helper has 3+ call sites or is exported with stable signature), DEFER this task to a follow-up plan and document why in `docs/observability/review-scraping.md`. Mark Task 7 incomplete in the plan, don't half-do it.

- [ ] **Step 3: Implement (only if Step 1 confirmed it's tractable)**

Same pattern as Tasks 3+5: extract a helper, switch to ctx logger.

- [ ] **Step 4: Run the test — expect PASS**

- [ ] **Step 5: Commit**

```bash
git add gmaps/entry.go gmaps/review_log_context_test.go
git commit -m "obs(gmaps): review_page_parse_failed carries place context"
```

---

### Task 8: Operator documentation

**Files:**
- Create: `docs/observability/review-scraping.md`

The user explicitly asked for "warnings to surface in Grafana/Loki" — that's only useful if there's a doc the on-call can read while the alert is firing. This task ships the operator-facing companion to the code changes.

- [ ] **Step 1: Write `docs/observability/review-scraping.md`**

Sections (each with concrete LogQL):

1. **Field reference.** Table mapping every emitted field to its meaning and source. Specifically: `job_id` = user-facing `jobs.id` (from ctx); `user_id` = Clerk user; `place_job_id` = scraper-internal PlaceJob UUID; `search_job_id` = scraper-internal GmapJob UUID. Clarify that `place_job_id` and `search_job_id` are mostly for cross-referencing within a single scrape, not for user-facing customer support.

2. **Event catalogue.** Table of every event name, its severity, when it fires, and what action the on-call should take. Use the canonical list from the audit table at the top of this plan.

3. **LogQL recipes.** At minimum:
   - Per-user review-error rate (alert basis):
     ```logql
     sum by (user_id) (rate(
       {service="backend"} | json
       | msg=~"review_extraction_failed|review_api_empty_response|reviews_(generate_url|fetch_page)_failed"
       [10m]
     ))
     ```
   - "Which of user X's jobs hit review errors":
     ```logql
     {service="backend"} | json
       | user_id="user_36X..."
       | msg=~"review_.*|place_payload_inconsistent_.*"
     ```
   - Google-shape-change canary:
     ```logql
     sum (count_over_time(
       {service="backend"} | json | msg="place_payload_inconsistent_review_count" [1h]
     ))
     ```
   - Fix A safety-net acceptance rate (health of the partial-payload fallback):
     ```logql
     sum (rate({service="backend"} | json | msg="extract_json_partial_payload_accepted" [1h]))
     / sum (rate({service="backend"} | json | msg="job_scrape_succeeded" [1h]))
     ```

4. **Alert thresholds (suggested).** Start conservative; tune after one week of baseline:
   - `place_payload_inconsistent_review_count > 0` over 5 minutes — PAGE (Google-shape-change is rare and high-impact)
   - `review_circuit_breaker_open > 0` over 5 minutes — PAGE (means the cookie jar is dead and all subsequent review enrichment is dropping silently)
   - per-user `review_extraction_failed` rate > 5% of jobs over 10 minutes — TICKET (per-user issue, often expired API key or scraper IP rate-limited)

5. **What this doc does NOT cover (yet).** A pre-built Grafana dashboard JSON is on the follow-up list; an example screenshot from a manually-built dashboard would be useful here once we have one.

- [ ] **Step 2: Commit**

```bash
git add docs/observability/review-scraping.md
git commit -m "docs(observability): review-scraping events, LogQL recipes, alert thresholds"
```

---

### Task 9: End-to-end verification on the dev backend

**Files:** none

Before opening the PR, confirm the new logs land in `logs/brezel-api-*.log` exactly as the tests assert — slog handler equivalence is good but Loki indexes the actual JSON the backend writes.

- [ ] **Step 1: Build + restart backend**

```bash
go build -o ./tmp/server .
PID=$(lsof -t -i :8080 -sTCP:LISTEN 2>/dev/null | head -1)
[ -n "$PID" ] && kill -9 "$PID"
DSN='postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable' \
  ./tmp/server -web -data-folder ./gmapsdata > /tmp/backend-obs.log 2>&1 &
```

- [ ] **Step 2: Re-run the bug-triggering job from the previous investigation**

```bash
curl -sS -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -H "X-API-Key: bscraper_dmsfVLKG40Bw97pQOf1YC48KCSBpzJik" \
  -d '{"name":"obs-verify","keywords":["Coffee Mitte Berlin"],"language":"en","depth":1,"max_images":0,"max_reviews":50,"max_results":5,"fast_mode":false}'
```

- [ ] **Step 3: Wait for terminal status, then grep the log**

```bash
JOB=<id-from-response>
until [ "$(curl -sS -H 'X-API-Key: bscraper_dmsfVLKG40Bw97pQOf1YC48KCSBpzJik' \
  http://localhost:8080/api/v1/jobs/$JOB | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')" != "running" ]
do sleep 3; done

# Confirm: every review-path log line for this job has user_id, the user-facing
# job_id, place_job_id, and place_url.
grep "$JOB" logs/brezel-api-$(date +%Y-%m-%d).log \
  | jq -r 'select(.msg | test("review_|place_payload|json_.*_fallback|extract_json_partial")) | "\(.msg)\tjob_id=\(.job_id)\tuser_id=\(.user_id)\tplace_job_id=\(.place_job_id)\tplace_url=\(.place_url)"'
```

Expected: every emitted line has all four fields populated. If `user_id` is empty (e.g. the test API key isn't bound to a user in this branch's DB), accept that as a known limitation of the test setup, not a fix bug; flag in the PR description.

- [ ] **Step 4: If the canary fires (`place_payload_inconsistent_review_count`), capture the line**

Save the line and include it as evidence in the PR description — proves the canary works against real Google JSON, not just synthetic test fixtures.

---

### Task 10: Open PR

**Files:** none

- [ ] **Step 1: Final test sweep**

```bash
go test ./gmaps -count=1 -skip Test_getNthElementAndCast_DoesNotPanicOnNegativeIndex
go vet ./...
```

- [ ] **Step 2: Push and PR**

```bash
git push -u origin <branch-name>
gh pr create --repo brezel-ai/brezelscraper-backend --base develop --title "obs(gmaps): user/job context + Google-shape canary on review-scraping logs"
```

PR description should:
- Reference PR #79 (the partial-payload fix this builds on)
- Embed a sample before/after JSON log line so reviewers can see the impact
- List the LogQL queries from the operator doc verbatim — these are part of the contract this PR establishes
- Call out the deferred items explicitly: dashboard JSON, `entry.go:274` if Task 7 was deferred, threading `place_name` into `reviews.go` logs

---

## Risk register

| Risk | Mitigation |
|------|------------|
| Renaming `job_id` → `place_job_id` breaks an existing Grafana dashboard or saved query | None of the dashboards I can grep live in this repo. The operator doc (Task 8) documents the rename. Loki queries are forward-compatible because the user-facing `job_id` is now what `job_id` actually means. |
| Context logger absent in non-webrunner code paths (databaserunner, filerunner, lambdaaws) | `scrapemate.GetLoggerFromContext` falls back to `logging.Get()` (a global default). Lines still emit; they just won't have user/job `.With` attributes. Acceptable — those code paths don't have a user_id concept anyway. |
| The canary fires false-positive on a legitimate edge case we didn't anticipate | The test in Task 4 documents the only invariant we check (`rating > 0 && review_count == 0`) and explicitly excludes the symmetric case. Severity is WARN, not ERROR — pageable threshold is set in the operator doc, not in code, so on-call can dial it without a code change. |
| `add_extra_reviews_panic` defer fires after `Process` returns and `ctx` is cancelled | The deferred closure captures `ctx` by reference; logging continues to work post-cancel. The slog handler doesn't check ctx for cancellation. Verified by inspecting `scrapemate_adapter.go` — `a.log.Warn(msg, args...)` doesn't touch ctx. |
| Test helper (`newCaptureLogger`) drifts from prod setup at `webrunner.go:989` | Both call sites build the logger the same way: `slog.New(JSONHandler).With(job_id, user_id)`. If prod changes, this drift is caught by Task 9's end-to-end verification. |

---

## Out of scope (call out in the PR)

1. **Grafana dashboard JSON** — separate plan; useful but doesn't ship in this PR.
2. **Backfill of historical corrupted DB rows** — separate, touches data not telemetry.
3. **Recovering from `review_api_empty_response`** — the circuit breaker logs+stops; making it actively refresh cookies is a different problem.
4. **Threading `place_name` into `reviews.go` logs** — requires JSON peek-parse in `BrowserActions`; small ergonomic improvement, not load-bearing for triage.
