# Google Maps Scraper - SaaS Edition

A multi-user Google Maps scraping platform with REST API, admin UI, job queuing, and cloud deployment.

## Deploy

Requirements: **Docker** installed and running.

```bash
curl -fsSL https://raw.githubusercontent.com/gosom/google-maps-scraper/main/PROVISION | sh
```

The interactive wizard will guide you through:
1. Docker image setup (build your own or use the public image)
2. Cloud provider selection (VPS, DigitalOcean, or Hetzner)
3. Database creation
4. Deployment and admin user creation

State is saved to `~/.gmapssaas/` so you can resume if interrupted.

## Update

After the initial deployment, push updates with:

```bash
curl -fsSL https://raw.githubusercontent.com/gosom/google-maps-scraper/main/PROVISION | sh -s update
```

## REST API

All endpoints require an API key (`X-API-Key` header). Create keys from the admin UI.

```bash
# Submit a scrape job
curl -X POST https://your-server/api/v1/scrape \
  -H "X-API-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"keyword": "restaurants in New York", "lang": "en", "max_depth": 10}'

# List jobs
curl https://your-server/api/v1/jobs?state=completed \
  -H "X-API-Key: your-api-key"

# Get job results
curl https://your-server/api/v1/jobs/{job_id} \
  -H "X-API-Key: your-api-key"
```

Full Swagger docs available at `/swagger/` on your deployed instance.

## Admin UI

Available at `/admin` after login. Manage API keys, provision workers, monitor jobs, and configure 2FA.

## Development

```bash
make saas-dev          # Start local dev environment (Postgres + hot reload)
# Visit http://localhost:8080/admin  (admin / 1234#abcd)

make saas-dev-stop     # Stop
make saas-dev-reset    # Reset all data
```
