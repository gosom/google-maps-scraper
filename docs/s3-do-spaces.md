# Using DigitalOcean Spaces instead of AWS S3

BrezelScraper's S3 client is built on `aws-sdk-go-v2`, which speaks the
S3 REST API that DigitalOcean Spaces is fully compatible with. To switch
from AWS S3 to DO Spaces, change only env vars.

## Required env vars

| Var                       | AWS value           | DigitalOcean Spaces value                      |
|---------------------------|---------------------|------------------------------------------------|
| `AWS_ACCESS_KEY_ID`       | Your AWS access key | Spaces access key (API → Spaces Keys)          |
| `AWS_SECRET_ACCESS_KEY`   | Your AWS secret     | Spaces secret                                  |
| `AWS_REGION`              | e.g. `eu-central-1` | DO region slug — see [Spaces Availability](https://docs.digitalocean.com/products/spaces/details/availability/) for the current list |
| `AWS_ENDPOINT`            | *(empty)*           | `https://<region>.digitaloceanspaces.com`      |
| `AWS_FORCE_PATH_STYLE`    | `false`             | `false` (default; both styles supported). Set `true` only if your bucket name contains dots — virtual-hosted style fails TLS hostname validation in that case. |
| `AWS_SSE_ENABLED`         | `true` for SSE-S3   | `false` (default). Spaces auto-encrypts at rest with AES-XTS regardless of this header. The behaviour of `x-amz-server-side-encryption` against Spaces is undocumented; safest to leave off. |
| `AWS_CHECKSUM_MODE`       | *(empty)*           | *(empty)* normally. Set `required` only if you see `XAmzContentSHA256Mismatch` or 400 errors — disables the SDK's default CRC32 trailer. |
| `S3_BUCKET_NAME`          | Your bucket         | Your Space name                                |

Example `.env` for DO Spaces in Frankfurt:

```env
AWS_ACCESS_KEY_ID=DO00ABCDEFGHIJ
AWS_SECRET_ACCESS_KEY=...
AWS_REGION=fra1
AWS_ENDPOINT=https://fra1.digitaloceanspaces.com
AWS_FORCE_PATH_STYLE=false
AWS_SSE_ENABLED=false
AWS_CHECKSUM_MODE=
S3_BUCKET_NAME=brezel-csv-prod
```

## AWS deployments are unchanged

If you are an existing AWS S3 customer, do nothing. Every new env var
defaults to the previous behaviour: `AWS_ENDPOINT=""` → AWS default
endpoint; `AWS_FORCE_PATH_STYLE=false` → virtual-hosted (unchanged);
`AWS_SSE_ENABLED=false` → no SSE header (unchanged); `AWS_CHECKSUM_MODE=""`
→ SDK default checksums (unchanged).

## What's different from AWS S3

- **Versioning:** Spaces *does* support S3 Versioning since 2024 — see
  [Enable Spaces Versioning](https://docs.digitalocean.com/products/spaces/how-to/enable-versioning/).
  When enabled, `VersionID` in our `job_files` table is populated; when
  disabled, it is `NULL`.
- **SSE:** Spaces auto-encrypts at rest with AES-XTS — there's nothing
  to set on the request. The `x-amz-server-side-encryption` header is
  not documented as supported; we gate it behind `AWS_SSE_ENABLED`
  (default off) so it's never sent against Spaces.
- **Lifecycle, replication, object lock, batch, inventory, S3 Select:**
  not supported. We don't use them.
- **Default request checksums (2026):** `aws-sdk-go-v2/service/s3 v1.73+`
  ships a CRC32 trailer on every PutObject by default. Spaces tolerates
  this in our testing; if a future Spaces change rejects it, set
  `AWS_CHECKSUM_MODE=required` to opt out.
- **Spaces CDN:** if enabled in the DO control panel, public reads come
  from `<bucket>.<region>.cdn.digitaloceanspaces.com`. Uploads still
  hit the origin host. Out of scope for v1; revisit if we offer
  presigned-URL downloads via CDN.

## Verifying the switch

1. Set the env vars and restart the backend.
2. Watch startup logs for `s3_preflight_ok` (HeadBucket succeeded) — if you
   see `s3_preflight_failed` with status 403/404, your creds, bucket name,
   or endpoint are wrong before any user traffic hits the system.
3. Submit a small scrape job and wait for it to finish.
4. Check the Space in the DO control panel — the file should appear at
   `users/<user>/jobs/<job_id>.csv`.
5. Hit `GET /api/v1/jobs/<id>/download-url` (presigned URL) and the legacy
   `GET /api/v1/jobs/<id>/download` (proxied stream); confirm both return
   the CSV.

## Download URLs

Two endpoints serve CSV downloads. New integrations should use the
presigned variant; the legacy proxied stream is kept only for backward
compatibility while clients migrate.

| Endpoint                                  | Method | Behaviour                                                                                          |
|-------------------------------------------|--------|----------------------------------------------------------------------------------------------------|
| `GET /api/v1/jobs/{id}/download-url`      | GET    | Returns `{"url": "<presigned>", "expires_in": "300"}`. Client GETs `<presigned>` directly. 5-minute TTL. |
| `GET /api/v1/jobs/{id}/download` (legacy) | GET    | Streams the CSV through the backend (`text/csv`, `Content-Disposition: attachment`). Deprecated.   |

The legacy route emits a `download_legacy_route_used` info log on every
call; once the LogQL query at the bottom of this doc returns zero rows
for two weeks, the route can be removed (separate ticket). Both routes
require the same auth — Clerk JWT or API key — and resolve the bucket
via the `job_files` row, so the presigned URL works regardless of
whether the bucket lives on AWS S3 or DO Spaces (both speak SigV4).

If `job_files` has no row for the job (e.g. legacy jobs uploaded before
the table existed, or jobs whose CSV is still on local disk),
`/download-url` returns 404 and the client should fall back to
`/download`.

## Querying S3 activity in Grafana

Logs are shipped to Loki via the Docker logging driver
(`environment=production,service=backend`) — see
`docker-compose.production.yaml`. Open Grafana → Explore → Loki and try:

```logql
# All S3 errors in the last hour
{service="backend"} | json | component="s3uploader" | level="ERROR"

# Failed uploads grouped by bucket (rate over 5min)
sum by (bucket) (
  count_over_time({service="backend"} | json | component="s3uploader" |= "s3 put object" [5m])
)

# Slow upload-or-download events (size > 10 MB)
{service="backend"} | json | component="s3uploader" | size_bytes > 10485760

# Track a specific job end-to-end
{service="backend"} | json | job_id="<uuid>"

# Anyone still hitting the legacy /download route
{service="backend"} | json | msg="download_legacy_route_used"
```

What's queryable as a JSON field on every s3uploader line:
- `component` (always `s3uploader`)
- `bucket`, `object_key`
- `etag` (success path)
- `error` (failure path; full wrapped error including bucket/key)
- `request_id`, `user_id` (only when the operation was triggered by an
  HTTP request — see Step 2d in Task 7 of the implementation plan)

## Privacy note

S3 object keys include `user_id` (Clerk opaque ID) and `job_id` (UUID).
Both are logged into Loki via the `object_key` JSON field. `user_id` is
already logged elsewhere in this codebase (billing, request middleware),
so this does not introduce new PII surface — but Loki retention applies.

## Metrics in Grafana (status: not yet)

The backend exposes Prometheus metrics at `/metrics` on the internal
listener (`InternalAddr` env var), including:

- `brezel_s3_op_duration_seconds{op,result}` — histogram of op latency
- `brezel_s3_op_total{op,result}` — count of operations
- `brezel_s3_op_bytes_total{op}` — bytes transferred

These are **exposed but not yet scraped** in production — there is no
Prometheus container deployed, no Prometheus datasource provisioned in
Grafana, and no dashboard JSON. A separate ticket tracks deploying
Prometheus + datasource + dashboard. Until then, observability for S3
is via Loki logs only (queries above).
