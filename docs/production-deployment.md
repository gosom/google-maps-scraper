# Production Deployment Checklist

Things to verify, configure, and do before running BrezelScraper in production.

## Environment Variables

All required in production mode (the app refuses to start without them):

| Variable | Purpose | Example |
|----------|---------|---------|
| `DSN` | PostgreSQL connection string | `postgres://user:pass@host:5432/dbname?sslmode=require` |
| `CLERK_SECRET_KEY` | Clerk authentication | `sk_live_...` |
| `STRIPE_SECRET_KEY` | Stripe payments | `sk_live_...` |
| `STRIPE_WEBHOOK_SECRET` | Stripe webhook signature verification | `whsec_...` |
| `ALLOWED_ORIGINS` | CORS allowed origins (comma-separated) | `https://app.brezelscraper.com` |
| `ENCRYPTION_KEY` | Encrypts integration credentials at rest | 32+ random bytes, hex-encoded |
| `API_KEY_SERVER_SECRET` | Signs and verifies API keys; also used to derive the webhook signing KEK | 32+ random bytes |

Optional but recommended:

| Variable | Purpose | Default |
|----------|---------|---------|
| `RESEND_API_KEY` | Send support emails via Resend | Falls back to log-only |
| `STRIPE_WEBHOOK_ALLOWED_CIDRS` | IP allowlist for Stripe webhooks | No restriction |
| `PORT` or `-addr` flag | HTTP listen address | `:8080` |

## Secret Generation

Generate production secrets with:

```bash
# API_KEY_SERVER_SECRET (32 bytes, hex-encoded = 64 chars)
openssl rand -hex 32

# ENCRYPTION_KEY (32 bytes, hex-encoded = 64 chars)
openssl rand -hex 32
```

These secrets derive the webhook signing KEK via `HMAC-SHA256(API_KEY_SERVER_SECRET, "webhook-signing-key-encryption")`. If you rotate `API_KEY_SERVER_SECRET`, all existing webhook signing secrets become undecryptable and users must recreate their webhooks.

## Database

### Fresh deployment

Migrations run automatically on app startup. No manual steps needed.

### Schema changes from this release

If upgrading from a previous deployment (not a fresh DB), these migrations must apply:

- `000027`: Webhook tables (`webhook_configs`, `job_webhook_deliveries`). Column is `encrypted_secret` (not the old `secret_hash`).
- `000017`: Billing system. Event type `job_start` (was `actor_start` in earlier versions).
- Job statuses are `completed` and `running` (were `ok` and `working`).

If the old schema had `secret_hash`, `actor_start`, or `ok`/`working` statuses, drop and recreate the database. There are no data migration scripts for these renames.

### Connection pool

The default `sql.DB` pool settings apply. For production, consider setting via DSN parameters:

```
?pool_max_conns=25&pool_min_conns=5
```

### SSL

Use `sslmode=require` or `sslmode=verify-full` in the DSN for production. Never use `sslmode=disable` outside of local development.

## Stripe

### Webhook endpoint

Register a Stripe webhook endpoint pointing to:

```
https://api.brezelscraper.com/api/v1/billing/stripe-webhook
```

Subscribe to these events:
- `checkout.session.completed`
- `charge.refunded`

Set the signing secret as `STRIPE_WEBHOOK_SECRET`.

### IP allowlist

For defense in depth, set `STRIPE_WEBHOOK_ALLOWED_CIDRS` to Stripe's published IP ranges. The app logs a warning on startup if this is not configured.

## DNS and TLS

### HSTS

The app sets `Strict-Transport-Security: max-age=63072000; includeSubDomains` on all responses. Make sure your deployment terminates TLS (via a reverse proxy, load balancer, or cloud provider) before traffic reaches the app. The app itself listens on HTTP.

### CORS

Set `ALLOWED_ORIGINS` to your frontend domain only (e.g., `https://app.brezelscraper.com`). Do not use `*` in production.

## Webhook Delivery

The webhook delivery worker starts automatically with the web server. It polls every 5 seconds for pending deliveries.

### Rate limits

- 100 deliveries per user per hour
- 50 deliveries per destination IP per hour

These are hardcoded constants. To change them, update `web/services/webhook_delivery.go` and redeploy.

### Signing secret rotation

If `API_KEY_SERVER_SECRET` changes, the webhook KEK changes, and existing webhook signing secrets cannot be decrypted. Users must delete and recreate their webhook configurations to get new signing secrets.

Plan: before rotating, notify users to save their webhook configs. After rotation, existing deliveries will fail with decryption errors and be marked as failed after 5 attempts.

## Signup Bonus

Currently set to `$2.00` in `web/auth/auth.go`:

```go
const SignupBonusAmount = 2.0
```

To change: update the constant, rebuild, deploy. Existing users are unaffected (the unique index prevents double-granting). No migration needed.

## Monitoring

### Health check

```
GET /health
```

Returns `{"status":"ok","db":"ok","version":"..."}`. No authentication required. Use this for load balancer health checks and uptime monitoring.

### Logs

The app logs structured JSON to stdout via `slog`. Key log events to alert on:

| Log message | Meaning | Action |
|-------------|---------|--------|
| `webhook_delivery_request_failed` | Outbound webhook POST failed | Check if user's endpoint is down |
| `webhook_rate_limit_user_exceeded` | User hit 100/hour delivery cap | Normal for heavy users |
| `webhook_cross_user_mismatch` | Delivery row references wrong user's webhook | Bug. Investigate immediately. |
| `job_start_charge_failed` | Billing charge failed at job start | User may have insufficient credits |
| `stuck_job_detected` | Job running longer than expected | Runner may be hung |

## Pre-launch Checklist

- [ ] All environment variables set
- [ ] PostgreSQL accessible with `sslmode=require`
- [ ] Stripe webhook endpoint registered and tested
- [ ] `ALLOWED_ORIGINS` set to production frontend domain
- [ ] DNS points `api.brezelscraper.com` to the deployment
- [ ] TLS termination configured (reverse proxy or cloud LB)
- [ ] Health check responds at `/health`
- [ ] Create a test API key and verify `GET /api/v1/jobs` returns 200
- [ ] Create a test webhook and verify the signing secret is returned
- [ ] Drop the dev database and start fresh (no legacy `secret_hash`, `actor_start`, `ok`/`working` data)
