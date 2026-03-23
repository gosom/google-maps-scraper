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

**Request body:**

```json
{
  "Name": "Coffee shops Berlin",
  "keywords": ["coffee shops Berlin Mitte"],
  "lang": "en",
  "depth": 1,
  "email": false,
  "images": false,
  "reviews_max": 0,
  "max_results": 50,
  "max_time": 600
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Name` | string | yes | Human-readable job name |
| `keywords` | string[] | yes | Search terms (1-5) |
| `lang` | string | yes | Language code, 2 chars (e.g. `"en"`, `"de"`) |
| `depth` | int | yes | Search depth (1-20) |
| `email` | bool | no | Scrape contact emails |
| `images` | bool | no | Scrape place images |
| `reviews_max` | int | no | Max reviews per place (0 = skip reviews, max 9999) |
| `max_results` | int | no | Max places to return (0 = unlimited, max 1000). Unlimited requires $5+ balance |
| `max_time` | int | yes | Timeout in seconds |
| `lat` | string | no | Latitude for geo-targeted search |
| `lon` | string | no | Longitude for geo-targeted search |
| `zoom` | int | no | Map zoom level (0-21) |
| `radius` | int | no | Search radius in meters |
| `fast_mode` | bool | no | Skip detailed place data for faster results |
| `proxies` | string[] | no | Custom proxy list |

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
    "depth": 1,
    "email": false,
    "images": false,
    "reviews_max": 0,
    "max_results": 20,
    "max_time": 600
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
    "Status": "ok",
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
| `limit` | int | 50 | Results per page (max 1000) |

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
| `working` | Currently scraping |
| `ok` | Completed successfully |
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

- **`max_time` is required.** Set it to `600` (10 minutes) for most jobs. The API returns `422 missing max time` without it.

- **`max_results: 0` means unlimited** and requires a credit balance of $5+. Set an explicit limit if your balance is lower.

- **Jobs are async.** `POST /jobs` returns immediately with a job ID. Poll `GET /jobs/{id}` to check status, or set up a webhook to get notified on completion.

- **`source` field is read-only.** It's automatically set to `"api"` for API key requests and `"web"` for dashboard requests. You cannot override it.

- **Soft deletes.** `DELETE /jobs/{id}` soft-deletes — results data is preserved internally. The job disappears from your list but can be restored by an admin.

- **Concurrent job limit.** You can only run a limited number of jobs simultaneously. If you hit the limit, the API returns `429` with a `Retry-After: 60` header.

- **Cost estimation.** Always call `POST /jobs/estimate` first to check if you have sufficient credits before creating a job.

- **Health check.** `GET /health` is unauthenticated and returns `{"status": "ok", "db": "ok", "version": "dev"}`.
