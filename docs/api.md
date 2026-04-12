# Brezel Scraper API Documentation

**Base URL:** `https://api.brezel.ai` (production) | `http://localhost:8080` (development)

**API Version:** `v1` — all endpoints are prefixed with `/api/v1`

---

## Authentication

Every authenticated endpoint requires an API key. You can create API keys from the dashboard under **Settings > API Keys**.

API keys use the prefix `bscraper_` and look like: `bscraper_abc123...`

### How to authenticate

We accept two methods — use whichever fits your setup:

#### Option 1: Authorization Bearer (recommended)

```bash
curl -H "Authorization: Bearer bscraper_YOUR_KEY" \
  https://api.brezel.ai/api/v1/jobs
```

This is the same pattern used by Stripe, OpenAI, and GitHub. Every HTTP client and integration tool (Postman, n8n, Make, Zapier) has built-in support for Bearer auth.

#### Option 2: X-API-Key header

```bash
curl -H "X-API-Key: bscraper_YOUR_KEY" \
  https://api.brezel.ai/api/v1/jobs
```

Useful when the `Authorization` header is already occupied by a proxy or gateway. This is the same pattern used by AWS API Gateway and Anthropic.

Both methods are equivalent — use whichever is more convenient.

### Authentication in popular tools

| Tool | Setup |
|------|-------|
| **curl** | `-H "Authorization: Bearer bscraper_..."` |
| **Postman** | Auth tab > Bearer Token > paste key |
| **n8n** | HTTP Request node > Header Auth > `Authorization` = `Bearer bscraper_...` |
| **Make** | HTTP module > API Key auth > Header: `Authorization`, Value: `Bearer bscraper_...` |
| **Python requests** | `headers={"Authorization": "Bearer bscraper_..."}` |
| **JavaScript fetch** | `headers: {"Authorization": "Bearer bscraper_..."}` |

### Rate limits

| Tier | Limit | Burst |
|------|-------|-------|
| Free API key | 2 req/s | 5 |
| Paid API key | 10 req/s | 30 |
| Session (web UI) | 5 req/s | 20 |
| Unauthenticated | 3 req/s per IP | 10 |

---

## Jobs

### Create a job

`POST /api/v1/jobs`

Start a new scraping job. The job runs asynchronously — poll `GET /api/v1/jobs/{id}` to track progress.

**Headers:**

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` / `X-API-Key` | yes | See [Authentication](#authentication) |
| `Content-Type` | yes | Must be `application/json` |
| `Idempotency-Key` | no | Opt-in deduplication key — see below |

#### Idempotency

Job creation is **billable**, so a network retry on `POST /api/v1/jobs` could double-charge you. To make retries safe, send an `Idempotency-Key` header with a unique value (a UUID is fine):

```bash
curl -X POST https://api.brezel.ai/api/v1/jobs \
  -H "Authorization: Bearer bscraper_YOUR_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 5a2c6b7e-3f9d-4f4f-9e1e-1d3b0a8c1c2e" \
  -d '{ "Name": "Cafes Berlin", ... }'
```

Behavior:

- **Replay window:** 24 hours. The first request with a given key is processed normally and its full response is cached. Subsequent requests with the same key + same body return the cached response — the job is created exactly once.
- **Same key, different body:** returns `409 Conflict` (`idempotency_key_in_use_with_different_body`). This catches a client bug — never reuse a key for a different request.
- **Same key, in flight:** if a retry arrives while the first request is still being processed, you get `409 Conflict` (`idempotency_key_in_use`) with a `Retry-After: 1` header. Wait one second and retry; you'll then receive the cached response.
- **Replayed responses** include the header `Idempotent-Replayed: true` so clients can distinguish them from fresh executions.
- **Maximum key length:** 255 bytes. Longer keys return `400 Bad Request`.
- **Scope:** keys are scoped per user — two users can use the same key without colliding.

The header is **opt-in**: requests without it work exactly as before, with no idempotency guarantee. Send it for every billable retry-loop you implement (CI pipelines, scheduled jobs, anything that might fire twice).

**Request body:**

```json
{
  "Name": "Coffee shops Berlin",
  "keywords": ["coffee shops Berlin Mitte"],
  "lang": "en",
  "depth": 5,
  "email": false,
  "images_max": 0,
  "reviews_max": 0,
  "max_results": 50,
  "max_time": 1800
}
```

| Field | Type | Required | Min | Max | Default | Description |
|-------|------|----------|-----|-----|---------|-------------|
| `Name` | string | yes | 1 | 200 | — | Human-readable job name |
| `keywords` | string[] | yes | 1 item | 5 items | — | Search terms (each ≤200 bytes) |
| `lang` | string | yes | 2 | 2 | — | Language code (ISO 639-1, see allowlist below) |
| `depth` | int | no | 1 | **20** | 5 | Search scroll depth (per-job) |
| `max_results` | int | no | 1 | **500** | 50 | Max places to return (per-job). No "unlimited" sentinel — pass the cap explicitly to opt in to the maximum |
| `reviews_max` | int | no | 0 | **500** | 0 | Max reviews **per place** (0 skips reviews entirely). Note the per-place scope: a 100-place job at `reviews_max=500` can return up to 50 000 reviews |
| `images_max` | int | no | 0 | **40 000** | 0 | Max images **per job total** across all places (0 skips images). Per-job-total scope is unique to this field — image counts on Google Maps are unbounded per business, so a per-place cap would not bound total billing |
| `max_time` | int | no | 60 | **3 600** | 1 800 | Wall-clock job timeout in seconds. Hard ceiling is 1 hour because headless Chromium scraping Google Maps degrades sharply over longer runs (memory creep, anti-bot escalation). Split larger workloads across multiple jobs |
| `radius` | int | no | 0 | **50 000** | 0 | Search radius in meters (0 = no constraint) |
| `email` | bool | no | — | — | false | Scrape contact emails from place websites |
| `lat` | string | no | -90 | 90 | — | Latitude for geo-targeted search |
| `lon` | string | no | -180 | 180 | — | Longitude for geo-targeted search |
| `zoom` | int | no | 0 | 21 | — | Map zoom level (geo-targeted search only) |
| `fast_mode` | bool | no | — | — | false | Skip detailed place data for faster results |
| `proxies` | string[] | no | 0 | 100 | — | Custom proxy URLs (`http://`, `https://`, `socks5://`, `socks5h://`). Each ≤2 048 bytes. Private/loopback/cloud-metadata IPs are rejected (SSRF defense) |

### Cap parameters convention

Every numeric cap field follows a uniform rule: there is no "unlimited" sentinel. Missing fields receive their documented default; values above the hard ceiling return `400 Bad Request` with a descriptive message. To opt in to the maximum, send the hard ceiling explicitly (the dashboard's "no cap" toggle does this). Defaults are deliberately conservative — a request that omits all cap fields runs a small, cheap job (OWASP API4:2023 fail-safe posture).

**Scope** (per the table above):
- **per-job** — `max_results`, `depth`, `radius`, `max_time` — the cap bounds the whole job
- **per place** — `reviews_max` — the cap bounds output per business; total reviews ≈ `max_results × reviews_max`
- **per job total** — `images_max` — the cap bounds the entire image count across all places in a single job

### Supported languages

`lang` must be one of the following ISO 639-1 codes (lowercase, exactly 2 characters):

`ar`, `bg`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fi`, `fr`, `he`, `hr`, `hu`, `id`, `it`, `ja`, `ko`, `lt`, `lv`, `ms`, `nl`, `no`, `pl`, `pt`, `ro`, `ru`, `sk`, `sl`, `sv`, `th`, `tr`, `uk`, `vi`, `zh`

**Response:** `201 Created`

```json
{
  "id": "d8d8a24e-cef2-4b02-8396-abc290b6f299"
}
```

**Example:**

```bash
curl -X POST https://api.brezel.ai/api/v1/jobs \
  -H "Authorization: Bearer bscraper_YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "Name": "Cafes in Berlin Wedding",
    "keywords": ["Cafe Berlin Wedding"],
    "lang": "en",
    "depth": 5,
    "email": false,
    "images_max": 0,
    "reviews_max": 0,
    "max_results": 20,
    "max_time": 1800
  }'
```

### List all jobs

`GET /api/v1/jobs`

Returns all jobs for the authenticated user, newest first.

**Response:** `200 OK`

```json
[
  {
    "ID": "d8d8a24e-...",
    "Name": "Cafes in Berlin Wedding",
    "Status": "completed",
    "source": "api",
    "created_at": "2026-03-23T07:14:57Z",
    "updated_at": "2026-03-23T07:16:02Z",
    "Data": { ... }
  }
]
```

The `source` field indicates how the job was created: `"api"` (via API key) or `"web"` (via dashboard).

### Get a job

`GET /api/v1/jobs/{id}`

**Response:** `200 OK` — full job object (same shape as list items).

### Delete a job

`DELETE /api/v1/jobs/{id}`

Soft-deletes the job. Results data is preserved internally.

**Response:** `200 OK`

### Cancel a job

`POST /api/v1/jobs/{id}/cancel`

Cancels a running or pending job.

**Response:** `200 OK`

```json
{
  "message": "Job cancellation initiated",
  "job_id": "d8d8a24e-..."
}
```

### Get job results

`GET /api/v1/jobs/{id}/results?page=1&limit=50`

Returns paginated scraped results for a completed job.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `page` | int | 1 | Page number |
| `limit` | int | 50 | Results per page (max **100**) |

**Response:** `200 OK`

```json
{
  "results": [ ... ],
  "total_count": 142,
  "page": 1,
  "limit": 50,
  "total_pages": 3,
  "has_next": true,
  "has_prev": false
}
```

### Download job results

`GET /api/v1/jobs/{id}/download`

Downloads results as a file.

### Get job costs

`GET /api/v1/jobs/{id}/costs`

Returns the cost breakdown for a job.

**Response:** `200 OK`

```json
{
  "job_id": "d8d8a24e-...",
  "items": [
    { "event_type": "actor_start", "quantity": 1, "cost_credits": "0.010000" },
    { "event_type": "place_scraped", "quantity": 20, "cost_credits": "0.200000" }
  ],
  "total_credits": "0.210000",
  "total_rounded": 1
}
```

### Estimate job cost

`POST /api/v1/jobs/estimate`

Estimate cost before creating a job. Same request body as `POST /jobs`.

**Response:** `200 OK`

```json
{
  "estimate": {
    "total_estimated_cost": 0.21,
    "estimated_places": 20,
    "estimated_reviews": 0,
    "estimated_images": 0,
    ...
  },
  "current_credit_balance": 4.50,
  "sufficient_balance": true
}
```

---

## Results

### Get all user results

`GET /api/v1/results?limit=50&offset=0`

Returns results across all jobs for the authenticated user.

---

## API Keys

### List API keys

`GET /api/v1/api-keys`

### Create an API key

`POST /api/v1/api-keys`

### Revoke an API key

`DELETE /api/v1/api-keys/{id}`

---

## Webhooks

### List webhooks

`GET /api/v1/webhooks`

### Create a webhook

`POST /api/v1/webhooks`

### Update a webhook

`PATCH /api/v1/webhooks/{id}`

### Delete a webhook

`DELETE /api/v1/webhooks/{id}`

---

## Credits & Billing

### Get credit balance

`GET /api/v1/credits/balance`

### Get billing history

`GET /api/v1/credits/history`

### Create checkout session

`POST /api/v1/credits/checkout-session`

Creates a Stripe checkout session to purchase credits.

---

## Google Sheets Integration

### Export job to Google Sheets

`POST /api/v1/jobs/{id}/export/google-sheets`

### Get integration status

`GET /api/v1/integrations/google/status`

### Get integration config

`GET /api/v1/integrations/config`

---

## Job Statuses

| Status | Description |
|--------|-------------|
| `pending` | Queued, waiting to start |
| `running` | Currently scraping |
| `completed` | Completed successfully |
| `failed` | Failed (check `failure_reason`) |
| `aborting` | Cancellation in progress |
| `cancelled` | Cancelled by user |

---

## Error Responses

All errors return a JSON body:

```json
{
  "code": 401,
  "message": "User not authenticated"
}
```

| Code | Meaning |
|------|---------|
| 400 | Bad request (validation failed) |
| 401 | Not authenticated (missing or invalid API key) |
| 402 | Insufficient credits |
| 404 | Resource not found |
| 422 | Unprocessable entity (invalid JSON or missing fields) |
| 429 | Rate limit or concurrent job limit reached (check `Retry-After` header) |
| 500 | Internal server error |

---

## Tips & Quirks

- **No "unlimited" cap fields.** Every numeric cap has a documented hard ceiling. Send the ceiling value explicitly to opt in to the maximum (the dashboard's "no cap" toggle does this) — there is no magic sentinel like `0 = unlimited` or `9999 = unlimited`. Defaults are conservative; a request that omits cap fields runs a small, cheap job.

- **`max_time` defaults to 1 800 seconds (30 min).** Hard ceiling is 3 600 seconds (1 hour). Headless Chromium scraping degrades sharply over longer runs — split larger workloads across multiple jobs.

- **Jobs are async.** `POST /jobs` returns immediately with a job ID. Poll `GET /jobs/{id}` to check status, or set up a webhook to get notified on completion.

- **`source` field is read-only.** It's automatically set to `"api"` for API key requests and `"web"` for dashboard requests. You cannot override it.

- **Soft deletes.** `DELETE /jobs/{id}` soft-deletes — results data is preserved internally. The job disappears from your list but can be restored by an admin.

- **Concurrent job limit.** You can only run a limited number of jobs simultaneously. If you hit the limit, the API returns `429` with a `Retry-After: 60` header.

- **Cost estimation.** Always call `POST /jobs/estimate` first to check if you have sufficient credits before creating a job.

- **Health check.** `GET /health` is unauthenticated and returns `{"status": "ok", "db": "ok", "version": "dev"}`.

---

## Database Backups

Brezel includes an automated PostgreSQL backup script that dumps the database to a compressed custom-format file and uploads it to S3 with retention-based cleanup.

### Running a backup manually

```bash
# Set required environment variables
export DATABASE_URL="postgres://user:pass@host:5432/brezel"
export S3_BACKUP_BUCKET="my-brezel-backups"
export AWS_ACCESS_KEY_ID="AKIA..."
export AWS_SECRET_ACCESS_KEY="..."
export AWS_REGION="eu-central-1"

# Run the backup
./scripts/backup-db.sh
```

To preview what the script would do without executing anything:

```bash
./scripts/backup-db.sh --dry-run
```

### Required environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DATABASE_URL` | yes* | — | PostgreSQL connection string |
| `POSTGRES_DSN` | yes* | — | Alternative to `DATABASE_URL` (either one must be set) |
| `S3_BACKUP_BUCKET` | yes | — | S3 bucket name for storing backups |
| `AWS_ACCESS_KEY_ID` | yes | — | AWS access key |
| `AWS_SECRET_ACCESS_KEY` | yes | — | AWS secret key |
| `AWS_REGION` | yes | — | AWS region (e.g. `eu-central-1`) |
| `BACKUP_RETENTION_DAYS` | no | `30` | Number of days to keep old backups in S3 |

### Setting up a cron job

To run backups automatically every day at 03:00 UTC:

```bash
# Edit crontab
crontab -e

# Add this line:
0 3 * * * DATABASE_URL="postgres://..." S3_BACKUP_BUCKET="..." AWS_ACCESS_KEY_ID="..." AWS_SECRET_ACCESS_KEY="..." AWS_REGION="eu-central-1" /path/to/scripts/backup-db.sh >> /var/log/brezel-backup.log 2>&1
```

Alternatively, source a `.env` file in the cron entry:

```bash
0 3 * * * . /path/to/.env && /path/to/scripts/backup-db.sh >> /var/log/brezel-backup.log 2>&1
```

### Restoring from a backup

Download the backup from S3 and restore it with `pg_restore`:

```bash
# Download the backup file
aws s3 cp s3://my-brezel-backups/db-backups/brezel-backup-2026-03-23-030000.dump ./backup.dump

# Restore into the target database
pg_restore -d "$DATABASE_URL" --clean --if-exists backup.dump
```

The `--clean --if-exists` flags drop existing objects before restoring, which is suitable for a full restore. To restore into a fresh empty database, omit those flags.
