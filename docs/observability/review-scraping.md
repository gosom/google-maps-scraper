# Review-Scraping Observability

On-call reference. Start at the section matching the alert that fired.

---

## Contents

1. [Field Reference](#1-field-reference)
2. [Event Catalogue](#2-event-catalogue)
3. [LogQL Recipes](#3-logql-recipes)
4. [Alert Thresholds](#4-alert-thresholds)
5. [Field-Naming Migration Notes](#5-field-naming-migration-notes)
6. [Out of Scope (for now)](#6-out-of-scope-for-now)

---

## 1. Field Reference

All review-path log events include these fields. JSON key names exactly as they appear in Loki after `| json`.

| Field | Type | What it contains | Source | Stable across retries? |
|---|---|---|---|---|
| `job_id` | string (UUID) | User-facing job ID — the row in `jobs` the customer sees in the UI | Struct field `PlaceJob.UserJobID`, passed as a field arg by emit helpers in `gmaps/place.go`, `gmaps/reviews.go`, and `gmaps/entry.go`. Populated by `runner/webrunner/webrunner.go` via `WithPlaceJobUserContext` (propagated from `SeedJobConfig.UserJobID = job.ID`). NOT inherited from ctx — scrapemate's `DoJob` replaces the ctx logger per job, stripping any With-attributes set upstream. | Yes — same UUID for all retries of the same customer job |
| `user_id` | string | Clerk user ID (e.g. `user_36Xabc123`) | Struct field `PlaceJob.UserID`, passed as a field arg by emit helpers. Populated by the webrunner from the authenticated session when the job is enqueued. Same ctx-replacement caveat as `job_id`. | Yes |
| `place_job_id` | string (UUID) | Scraper-internal `PlaceJob.ID` — one per place being scraped | Passed as a field arg at the call site | No — a re-scrape generates a fresh UUID |
| `search_job_id` | string (UUID) | Scraper-internal `GmapJob.ID` — the parent search that spawned this place | Passed as a field arg at the call site | No — same caveat as above |
| `place_url` | string | Full Google Maps URL for the place being scraped | Helper arg | Stable for the same physical place across jobs |
| `place_name` | string | Display name of the place as returned by Google | Parsed from page; present on events where the name is already resolved | Stable for the same place |
| `error` | string | Wrapped error message | Direct field at call site | — |
| `reason` | string | Human-readable fallback category | Direct field at call site | — |

**Scope:** Only the review-path emit helpers (the events catalogued in §2) carry `user_id` / `job_id`. Scrapemate-internal lifecycle lines (`starting job`, `job finished`, retry/backoff logs) do NOT carry these fields — they originate from scrapemate's own `s.log` and only carry the scrapemate-internal `jobid` attribute. Use webrunner lifecycle logs (`job_picked_up`, `job_scrape_succeeded`, etc.) for that cross-correlation.

**CLI / standalone scrapes:** `PlaceJob.UserID` and `PlaceJob.UserJobID` are empty in CLI / databaserunner / filerunner / lambda runs. Emit helpers omit the `user_id` and `job_id` keys entirely in that case (see `userArgs` in `gmaps/place.go`) to avoid polluting per-user Grafana alert buckets with empty-string keys.

**Customer-support workflow:** filter by `user_id` to see all of a customer's jobs, then narrow with `job_id` for a specific job. `place_job_id` and `search_job_id` are scraper-internal IDs useful only for cross-referencing within a single scrape run — they are not shown in the UI and have no meaning to the customer.

---

## 2. Event Catalogue

Events are ordered roughly by increasing severity / operational impact.

---

### `authenticated_review_fetch_failed_falling_back`

**Level:** DEBUG

**Fires when:** The authenticated (cookie-based) review fetch attempt failed; the scraper is falling back to the stealth (unauthenticated) path.

**On-call action:** No immediate action needed — this is an expected degradation path. If paired with a spike in `review_api_empty_response`, check whether production cookies have expired (see `google_cookies.json`).

**Extra fields:** `place_url`, `place_name`, `error`

---

### `review_page_parse_failed`

**Level:** WARN

**Fires when:** A single page of review JSON could not be parsed; the scraper continues to the next page in the batch.

**On-call action:** Check `error` for the malformed field. If many pages across many places are failing, the Google review JSON schema may have changed — escalate to a developer.

**Extra fields:** `place_url`, `place_name`, `page`, `total_pages`, `error`

---

### `json_extraction_fallback`

**Level:** WARN

**Fires when:** The browser actions returned no raw JSON at all for a place; a minimal entry (name/URL only) was persisted.

**On-call action:** `reason` is always the static string `"creating minimal entry with URL only"` (informational only — do not filter on it to diagnose the cause). Inspect `error` for the actual failure cause. If `error` shows a widespread timeout pattern, the extract-JSON timing budget may need increasing. If `error` indicates a missing DOM element, Google may have changed the page structure.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `error`, `reason`

---

### `json_parsing_fallback`

**Level:** WARN

**Fires when:** Raw JSON was present on the page but `EntryFromJSON` failed to parse it; a minimal entry was persisted.

**On-call action:** Check `error` for the parse failure — that is where the actual failure cause is recorded. `reason` is always the static string `"creating minimal entry with URL only"` (identical to `json_extraction_fallback`; informational only). If this fires for many places in the same job, a Google JSON schema change has likely broken the parser — escalate immediately.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `error`, `reason`

---

### `extract_json_partial_payload_accepted`

**Level:** WARN

**Fires when:** The 15 × 200 ms polling budget was exhausted waiting for the full place-detail JSON, but a usable partial payload was found and accepted as the fallback (Fix A safety net).

**On-call action:** Occasional occurrences are normal for slow connections. A sustained spike means Google has slowed place-detail hydration or the timing budget is too tight — check the acceptance-rate query in §3 and consider raising `maxAttempts` or the per-attempt delay.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `bytes`, `detail`

---

### `place_payload_inconsistent_review_count`

**Level:** WARN

**Fires when:** The parsed `Entry` has `rating > 0` but `review_count == 0` — catches the corruption pattern where a place ends up stored with rating>0 but review_count=0, which is impossible for a legitimate Google Maps place. Fires when the parsed Entry violates this invariant.

**On-call action:** Any occurrence is a signal that Google has changed the review-count field path. Pull the `place_url` and verify manually. File a developer task to update the JSON parser. Check whether rows in the DB already have corrupted `review_count = 0` alongside non-zero ratings.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `place_name`, `rating`, `detail`

---

### `review_extraction_failed`

**Level:** WARN

**Fires when:** The review fetcher returned a hard error for a specific place (network failure, unexpected HTTP status, etc.).

**On-call action:** Check `error`. Isolated failures are normal. If the error is widespread across a job, check proxy health and whether cookies are valid.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `error`

---

### `review_api_empty_response`

**Level:** WARN

**Fires when:** Google returned HTTP 200 but the review data was empty — typically caused by expired cookies or rate-limiting.

**On-call action:** Check `possible_cause` and `consecutive_empty`. If `consecutive_empty` is climbing toward 3, a `review_circuit_breaker_open` is imminent. Rotate cookies or pause the job before the breaker opens.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `review_count_on_page`, `response_bytes`, `consecutive_empty`, `possible_cause`

---

### `reviews_generate_url_failed`

**Level:** ERROR

**Fires when:** The scraper failed to build the review-API URL for a place (either the initial URL or a pagination token URL).

**On-call action:** Check `error`. A URL-generation failure is almost always a code bug or an unexpected `place_url` format. If `next_page_token` is non-empty, the pagination loop hit an edge case — investigate the token value.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `place_name`, `next_page_token`, `error`

---

### `reviews_fetch_page_failed`

**Level:** ERROR

**Fires when:** A pagination loop fetch failed (network error or non-retryable HTTP status).

**On-call action:** Check `error` and `review_url`. If many places in the same job hit this, proxy health or IP blocking is the most likely cause. Check proxy pool status.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `place_name`, `next_page_token`, `review_url`, `error`

---

### `review_extraction_panic`

**Level:** ERROR

**Fires when:** A runtime panic occurred inside the review-fetch goroutine and was recovered by the deferred panic handler.

**On-call action:** The `stack` field contains the full goroutine stack trace. This is always a code bug. File a P1 issue, include `place_url`, `panic`, and `stack`. The place was skipped; the job continued, but the scrape result for this place is incomplete.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `panic`, `stack`

---

### `add_extra_reviews_panic`

**Level:** ERROR

**Fires when:** A runtime panic occurred inside `AddExtraReviews` and was recovered.

**On-call action:** Same as `review_extraction_panic` — always a code bug. Use `entry_title` and `place_url` to identify the affected row in the DB. File a P1 issue with the full `stack`.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `entry_title`, `panic`, `stack`

---

### `review_circuit_breaker_open`

**Level:** ERROR

**Fires when:** 3 consecutive empty review-API responses were observed; the circuit breaker tripped and review extraction is now skipped for all remaining places in this worker process.

**On-call action:** **High urgency.** The `action` field is always the literal string `"skipping reviews for remaining places"` (hardcoded — do not filter with `action="skipping_reviews"`, that will return zero results). All subsequent places in the same process produce zero reviews until the binary restarts — the breaker is process-global. The `likely_cause` field is always the literal string `"cookies expired or IP rate-limited"` (hardcoded diagnostic string, not an enum). Immediately rotate cookies and restart the backend container. If restarting is not feasible, cancel the running job from the admin panel to stop further empty-review writes.

**Extra fields:** `place_job_id`, `search_job_id`, `place_url`, `consecutive_failures`, `action`, `likely_cause`

---

## 3. LogQL Recipes

All queries assume:
- Stream selector: `{service="backend"}` (label set by the docker-loki driver)
- Log lines are JSON; `| json` parses all fields

Open Grafana → Explore → Loki data source, paste query, run.

---

### (a) Per-user review-error rate — alert basis

```logql
sum by (user_id) (rate(
  {service="backend"} | json
  | msg=~"review_extraction_failed|review_api_empty_response|reviews_(generate_url|fetch_page)_failed"
  [10m]
))
```

Returns errors per second broken out by customer. Use as the backing query for the TICKET-level alert in §4.

---

### (b) Which of user X's jobs hit review issues — customer-support workflow

```logql
{service="backend"} | json
  | user_id="user_36X..."
  | msg=~"review_.*|place_payload_inconsistent_.*"
```

Replace `user_36X...` with the actual Clerk `user_id` from the customer's account page (Admin → Users → copy ID). This surfaces every review-path warning/error across all of that customer's jobs. Narrow further by adding `| job_id="<uuid>"` once you know which job is in question.

---

### (c) Google-shape-change canary count — hourly sensor

```logql
sum (count_over_time(
  {service="backend"} | json | msg="place_payload_inconsistent_review_count" [1h]
))
```

Any value above zero warrants investigation. A sudden step-change means Google updated the review-count field path. Cross-reference with the DB (`SELECT COUNT(*) FROM entries WHERE rating > 0 AND review_count = 0 AND updated_at > now() - interval '1 hour'`).

---

### (d) Fix A safety-net acceptance rate — partial-payload fallback health

```logql
sum (rate({service="backend"} | json | msg="extract_json_partial_payload_accepted" [1h]))
/ sum (rate({service="backend"} | json | msg="job_scrape_succeeded" [1h]))
```

Interpretation: fraction of completed scrapes where at least one place hit the partial-payload fallback. Expected baseline near 0. A sustained value above ~10% means either Google has slowed place-detail hydration or the timing budget (`maxAttempts × delay`) is too tight — raise `maxAttempts` or per-attempt delay before considering other fixes.

Note: `job_scrape_succeeded` fires even on partial failures; the ratio is approximate but trending is what matters.

---

### (e) Circuit-breaker open events — cookies / IP / rate-limit

```logql
{service="backend"} | json | msg="review_circuit_breaker_open"
```

Interpretation: when this fires, **all subsequent places in the same worker process skip review extraction** until the binary restarts. This is process-global — there is no per-job isolation. Treat as high urgency regardless of frequency. Include `likely_cause` in your incident notes.

---

### (f) Per-place corruption trace — deep-dive a specific URL

```logql
{service="backend"} | json
  | place_url=~"https://www.google.com/maps/place/Caf.+Libre.+Berlin.*"
  | msg=~"extract_json_partial_payload_accepted|place_payload_inconsistent_review_count|review_extraction_failed"
```

Replace the regex with the URL (or a substring) of the place under investigation. Use this to reconstruct the exact sequence of fallbacks that produced a suspicious row in the DB.

---

## 4. Alert Thresholds

Start conservative; tune after one week of production baseline.

### `place_payload_inconsistent_review_count` — PAGE

**Query:**
```logql
count_over_time({service="backend"} | json | msg="place_payload_inconsistent_review_count" [5m])
```

**Threshold:** > 0

**Severity:** PAGE

**Rationale:** This event is the primary canary for the corruption pattern (rating>0 but review_count=0). Even a single occurrence in 5 minutes is worth waking someone up — the entire affected job's `review_count` data is suspect, and the pattern tends to be systematic once Google changes the shape. False-positive rate is effectively zero.

---

### `review_circuit_breaker_open` — PAGE

**Query:**
```logql
count_over_time({service="backend"} | json | msg="review_circuit_breaker_open" [5m])
```

**Threshold:** > 0

**Severity:** PAGE

**Rationale:** When the breaker opens, all remaining places in the worker process silently produce zero reviews. The damage compounds with every additional place scraped before the binary is restarted. There is no self-healing today — human intervention (cookie rotation + container restart) is required.

---

### Per-user review-error rate — TICKET

**Query:**
```logql
sum by (user_id) (rate(
  {service="backend"} | json
  | msg=~"review_extraction_failed|review_api_empty_response|reviews_(generate_url|fetch_page)_failed"
  [10m]
)) > 0.05
```

**Threshold:** Any `user_id` bucket exceeds 0.05 errors/second over 10 minutes (roughly 30 errors in 10 min for a typical job)

**Severity:** TICKET

**Rationale:** A per-user spike (rather than global) suggests a job-specific proxy or cookie issue rather than a platform-wide outage. A ticket is appropriate; proactive outreach to the affected customer may follow if the job completes with missing reviews.

---

### `extract_json_partial_payload_accepted` acceptance rate — TICKET

**Query:**
```logql
sum (rate({service="backend"} | json | msg="extract_json_partial_payload_accepted" [1h]))
/ sum (rate({service="backend"} | json | msg="job_scrape_succeeded" [1h]))
```

**Threshold:** > 0.10 (10%)

**Severity:** TICKET

**Rationale:** Occasional partial-payload fallbacks are benign — the safety net is working as intended. A sustained rate above 10% signals that Google has slowed place-detail hydration broadly, or that our timing budget is too tight for current network conditions. The fix is a config change (raise `maxAttempts` or delay), not an incident, but it warrants investigation within the same business day.

---

## 5. Field-Naming Migration Notes

Prior to this PR, review-extraction logs used `job_id` and `parent_job_id` with scraper-internal meanings:

| Old field name | Old meaning | New field name |
|---|---|---|
| `job_id` (in review-path logs) | Scraper-internal `PlaceJob.ID` | `place_job_id` |
| `parent_job_id` | Scraper-internal `GmapJob.ID` | `search_job_id` |

With this PR:

- **`job_id`** (top-level, injected by ctx) = user-facing `jobs.id` — the UUID shown in the product UI and stored in the `jobs` table
- **`place_job_id`** = what used to be called `job_id` in review-path logs
- **`search_job_id`** = what used to be called `parent_job_id`

**Impact on existing Grafana dashboards:** Any saved panel that filters or groups by `parent_job_id` will stop matching after this PR is deployed. Update those panels to use `search_job_id`. If you need to correlate across the boundary (old logs use the old names, new logs use the new names), filter by `place_url` or `user_id` instead — both are stable across the rename.

**Loki query migration example:**

```logql
# Before (old field names)
{service="backend"} | json | parent_job_id="<uuid>"

# After (new field names)
{service="backend"} | json | search_job_id="<uuid>"

# Works across both old and new logs
{service="backend"} | json | place_url="https://www.google.com/maps/place/..."
```

---

## 6. Out of Scope (for now)

The following are known gaps — follow-up tasks, not omissions:

- **Pre-built Grafana dashboard JSON.** The LogQL recipes in §3 are the source of truth; a dashboard export will be added in a follow-up.
- **DB backfill of historically corrupted rows.** Rows where `rating > 0 AND review_count = 0` written before this PR are not automatically corrected. A one-time migration script is a separate task.
- **Active recovery from `review_api_empty_response`.** Today the circuit breaker simply stops extracting reviews — it does not attempt to refresh cookies or switch proxy pools. Cookie auto-rotation is a follow-up feature.
- **Per-job circuit breaker isolation.** Today the breaker is process-global; one bad job can affect all concurrent jobs in the same process. Scoping the breaker to a single `GmapJob.ID` is a future improvement.
