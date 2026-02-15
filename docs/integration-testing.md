# Integration Testing (Single Source Of Truth)

This project has **live end-to-end integration tests** that validate the *real* Google Maps scraping job lifecycle through the Go API:

- create a scrape job
- wait for it to finish successfully
- verify results exist via the Results API
- verify the downloaded CSV exists, parses, and contains rows
- verify CSV row count matches API `total_count`

The goal is to catch real failure modes that a naive "status == ok" check misses (for example: `ok` but empty output, stuck jobs, progress stalls, or malformed CSV output).

## Where The Tests Live

- E2E test suite: `/Users/yasseen/Documents/google-maps-scraper-2/web/handlers/api_scrape_integration_test.go`

These tests are behind:

- build tag: `integration`
- env guard: `BRAZA_RUN_SCRAPE_E2E=1`

So they do **not** run during normal `go test ./...` unless explicitly enabled.

## What Gets Validated (Deep Checks)

The integration suite **fails** if any of the following happens:

- Job becomes terminal with a non-success status (`failed`, `cancelled`, `aborting`)
- Job gets stuck in `pending` past a timeout
- Job gets stuck in `working` with **0 results** past a timeout
- Job's results stop increasing for too long (progress stall)
- Job reaches `max_results` but stays non-terminal for too long
- Job is `ok` but:
  - Results API has `total_count == 0`
  - Results contain no record with non-empty `title` and `link`
  - When images/reviews are requested: Results API has no record with non-empty `images` / `user_reviews_extended`
  - CSV download is empty, malformed, missing required columns, or has **0 data rows**
  - When images/reviews are requested: CSV has no row with non-empty `images` / `user_reviews_extended`
  - CSV data row count does **not** match Results API `total_count`

CSV validation requires these columns to exist:

- `input_id`
- `title`
- `link`
- `images`
- `user_reviews_extended`

## Test Matrix (Current Scenarios)

All scenarios use the same keyword:

- `Berlin wedding cafe`

Scenarios in `TestAPIJobs_ScrapeParameterMatrix`:

1. `happy_path_default_depth_minimal_payload`
   - minimal settings at frontend default depth (`depth=5`), small max results (`max_results=10`)
2. `images_enabled_small_run`
   - `depth=5`, `images=true`, `reviews_max=5`, small run (`max_results=5`)
3. `unlimited_results_images_reviews_depth5`
   - `depth=5`, `images=true`, `reviews_max=9999` (unlimited), `max_results=0` (unlimited), timeboxed by `max_time`
4. `unlimited_results_images_reviews_depth6`
   - same as above, `depth=6`
5. `unlimited_results_images_reviews_depth7`
   - same as above, `depth=7`
6. `fast_mode_with_coordinates` (opt-in)
   - `fast_mode=true`, `lat/lon/zoom/radius`, small run (`max_results=8`)

## How To Run

### 1) Ensure The API Is Running

The tests call a running API, defaulting to:

- `http://localhost:8080`

Override with:

- `BRAZA_API_BASE_URL`

### 2) Provide Authentication (One Of These)

The tests need to authenticate against `/api/v1/*`.

Pick one:

1. **Session cookie (from frontend)**
   - `BRAZA_SESSION_COOKIE` = value of the cookie named `__session`
   - Do not use `__session_<suffix>` cookies.
2. **Bearer token**
   - `BRAZA_AUTH_TOKEN` can be either:
     - `Bearer <jwt>`
     - `<jwt>`
3. **Mint fresh JWTs from Clerk (recommended if you have backend secrets)**
   - `BRAZA_CLERK_SECRET_KEY`
   - `BRAZA_CLERK_SESSION_ID`
4. **Dev auth bypass (local-only, no Clerk dependency)**
   - Start the API with `BRAZA_DEV_AUTH_BYPASS=1`
   - Run tests with `BRAZA_DEV_USER_ID='<existing user_id>'`
   - This sets `X-Braza-Dev-User` on requests and the server trusts it only when bypass is enabled.

Notes:

- If Clerk minting is configured, the test suite mints a JWT and **caches it** until near expiry.
- If you paste a full cookie string that contains `__session=...; ...`, the test extracts the `__session` value automatically.

### 3) Run One Scenario First

```bash
cd /Users/yasseen/Documents/google-maps-scraper-2

BRAZA_RUN_SCRAPE_E2E=1 \
BRAZA_API_BASE_URL='http://localhost:8080' \
BRAZA_SESSION_COOKIE='<__session cookie value>' \
go test ./web/handlers -tags integration \
  -run 'TestAPIJobs_ScrapeParameterMatrix/happy_path_default_depth_minimal_payload' \
  -v -count=1 -timeout 1h
```

Dev auth bypass example:

```bash
cd /Users/yasseen/Documents/google-maps-scraper-2

# Terminal A (server):
# BRAZA_DEV_AUTH_BYPASS=1 ./brezel-api -web -debug -dsn 'postgres://...'

# Terminal B (tests):
BRAZA_RUN_SCRAPE_E2E=1 \
BRAZA_DEV_USER_ID='<existing user_id>' \
BRAZA_API_BASE_URL='http://localhost:8080' \
go test ./web/handlers -tags integration \
  -run 'TestAPIJobs_ScrapeParameterMatrix/happy_path_default_depth_minimal_payload' \
  -v -count=1 -timeout 1h
```

Run fast mode only:

```bash
cd /Users/yasseen/Documents/google-maps-scraper-2

BRAZA_RUN_SCRAPE_E2E=1 \
BRAZA_INCLUDE_FAST_MODE=1 \
BRAZA_API_BASE_URL='http://localhost:8080' \
BRAZA_SESSION_COOKIE='<__session cookie value>' \
go test ./web/handlers -tags integration \
  -run 'TestAPIJobs_ScrapeParameterMatrix/fast_mode_with_coordinates' \
  -v -count=1 -timeout 1h
```

### 4) Run The Whole Matrix

```bash
cd /Users/yasseen/Documents/google-maps-scraper-2

BRAZA_RUN_SCRAPE_E2E=1 \
BRAZA_API_BASE_URL='http://localhost:8080' \
BRAZA_SESSION_COOKIE='<__session cookie value>' \
go test ./web/handlers -tags integration \
  -run 'TestAPIJobs_ScrapeParameterMatrix' \
  -v -count=1 -timeout 4h
```

## Useful Environment Variables

Required:

- `BRAZA_RUN_SCRAPE_E2E=1`

Target API:

- `BRAZA_API_BASE_URL` (default `http://localhost:8080`)

Auth (pick one strategy):

- `BRAZA_SESSION_COOKIE`
- `BRAZA_AUTH_TOKEN`
- `BRAZA_CLERK_SECRET_KEY` + `BRAZA_CLERK_SESSION_ID`
- `BRAZA_DEV_USER_ID` (requires server started with `BRAZA_DEV_AUTH_BYPASS=1`)

Job retention:

- `BRAZA_KEEP_JOBS=1` keeps created jobs (no DELETE cleanup). Useful when debugging.

Optional scenario toggles:

- `BRAZA_INCLUDE_FAST_MODE=1` adds the `fast_mode_with_coordinates` scenario to the matrix.

Lifecycle timeouts (these are **test** timeouts; they do not change server behavior):

- `BRAZA_POLL_INTERVAL` (default `10s`)
- `BRAZA_JOB_TIMEOUT` (default `35m`)
- `BRAZA_PENDING_TIMEOUT` (default `3m`)
- `BRAZA_ZERO_RESULTS_TIMEOUT` (default `12m`)
- `BRAZA_PROGRESS_STALL_TIMEOUT` (default `8m`)
- `BRAZA_MAX_RESULTS_GRACE_TIMEOUT` (default `4m`)

## Editing / Adding New Scenarios

Edit:

- `/Users/yasseen/Documents/google-maps-scraper-2/web/handlers/api_scrape_integration_test.go`

Add a new entry to the `scenarios := []scrapeScenario{...}` slice in `TestAPIJobs_ScrapeParameterMatrix`.

Guidelines:

- Keep `keywords` set to `Berlin wedding cafe` unless you intentionally want a different dataset.
- Always include `max_results` for predictable test bounds.
  - `max_results=0` means "unlimited" and should be paired with a reasonable `max_time`.
- `reviews_max=0` means "do not scrape reviews" (NOT unlimited).
  - Use `reviews_max=9999` to simulate "unlimited reviews" (frontend sentinel).
- `max_time` in the API request is sent as **seconds** (e.g. `420`), but the Job payload returned by the API serializes Go's `time.Duration` as **nanoseconds** (e.g. `420000000000`). Do not assert strict equality against the request value.
- If you set `fast_mode=true`, you must also set:
  - `lat`, `lon` (strings)
  - `zoom` (1-21)
  - optionally `radius`
- If you change expected output shape, update the CSV required columns only if the CSV schema truly changed.

## Debugging Failures

### If a job is `failed` / `ok but empty`

1. Re-run a single scenario with `BRAZA_KEEP_JOBS=1` to keep the job ID:

```bash
BRAZA_KEEP_JOBS=1 ... go test ./web/handlers -tags integration -run 'TestAPIJobs_ScrapeParameterMatrix/happy_path_default_depth_minimal_payload' -v -count=1
```

2. Inspect the job and output:

- `GET /api/v1/jobs/<job_id>`
- `GET /api/v1/jobs/<job_id>/results?limit=100&page=1`
- `GET /api/v1/jobs/<job_id>/download`

3. Check server logs for that job ID (local dev tends to write JSON logs under `logs/`).

### Common real-world reasons for "0 results"

- Google consent/captcha/blocking
- network/proxy issues
- Playwright not installed / browser launch failures (non-fast mode)
- backend credit/billing checks blocking job start

The E2E tests intentionally fail in these cases because the scraping is not producing usable output.

## Implementation Notes (Why These Tests Are Trustworthy)

These tests were designed to validate the "full chain":

- API lifecycle correctness
- Results persistence correctness
- CSV generation correctness
- consistency between API results and CSV output

Key backend changes supporting this:

- CSV is flushed reliably to avoid truncated/malformed output during forced cancellation paths:
  - `/Users/yasseen/Documents/google-maps-scraper-2/runner/webrunner/writers/synchronized_dual_writer.go`
- Fast mode (`tbm=map`) parsing hardened (handles already-decoded bodies and prevents panics on schema drift):
  - `/Users/yasseen/Documents/google-maps-scraper-2/gmaps/searchjob.go`
  - `/Users/yasseen/Documents/google-maps-scraper-2/gmaps/entry.go`

## CI Recommendation

These tests hit real external systems (Google + Clerk) and can be flaky due to rate-limiting or anti-bot behavior.

Recommended approach:

- Run unit tests on every PR: `go test ./...`
- Run integration E2E tests:
  - manually before release
  - or on a scheduled workflow with stable IP/proxy and long timeouts
