# Backend — BrezelScraper

## Running Locally

The backend must be started with the `-web` flag to run the HTTP API server. Without it, it runs as a standalone scraper.

```bash
# Standard local development (web server + scraper worker)
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" go run . -web

# With debug mode (opens visible browser window for scraping — useful for debugging scrape jobs)
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" go run . -web -debug
```

The `.env` file uses `host.docker.internal` for the DSN which only works inside Docker. When running natively on macOS, override with `localhost`:

```bash
DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" ./tmp/server -web
```

Or for a quick build+run:

```bash
go build -o ./tmp/server . && DSN="postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" ./tmp/server -web
```

## Key CLI Flags

| Flag | Description | Default |
|---|---|---|
| `-web` | **Required for API server.** Runs web server instead of CLI scraper. | `false` |
| `-debug` | Opens visible browser window for scraping (headful mode). Useful for debugging scrape jobs. | `false` |
| `-addr` | HTTP server listen address | `:8080` |
| `-dsn` | PostgreSQL connection string | (from env) |
| `-c` | Scraper concurrency. Accepts numbers, percentages (`75%`), fractions (`3/4`), or keywords (`auto`, `max`, `conservative`) | Half of CPU cores |
| `-depth` | Maximum scroll depth in search results | `10` |
| `-fast-mode` | Reduced data collection (faster scrapes) | `false` |
| `-email` | Extract emails from business websites | `false` |
| `-extra-reviews` | Enable extended review collection | `false` |

## Architecture

- `main.go` — Entry point, delegates to `runner/`
- `runner/` — CLI flag parsing, runner selection (web vs scraper vs lambda)
- `web/` — HTTP server: routes (`web.go`), handlers (`handlers/`), middleware (`middleware/`), services (`services/`)
- `web/auth/` — Clerk JWT validation + API key authentication
- `models/` — Data models (Job, User, APIKey, etc.)
- `postgres/` — Database repository implementations
- `billing/` — Stripe integration
- `scripts/migrations/` — SQL migrations (auto-applied on startup)

## Auth

- Uses Clerk Go SDK v2 (`github.com/clerk/clerk-sdk-go/v2`)
- JWT validation in `web/auth/auth.go`
- Dual auth: Clerk JWT sessions + API key tokens (`bscraper_` prefix)

## Database

- PostgreSQL with `pgx` driver
- Migrations in `scripts/migrations/` — applied automatically on web server startup
- Connection via `DSN` env var or `-dsn` flag

## API Endpoints

All protected routes require `Authorization: Bearer <clerk_jwt>` or `X-API-Key: bscraper_...` header.

Key endpoints:
- `GET /api/v1/jobs?page=1&limit=10&sort=created_at&order=desc&search=` — Paginated job list
- `POST /api/v1/jobs` — Create scraping job
- `POST /api/v1/jobs/costs/batch` — Batch cost lookup (accepts `{job_ids: [...]}`)
- `GET /api/v1/dashboard?limit=5` — Dashboard KPIs + recent jobs
- `GET /api/v1/credits/balance` — Credit balance
- `GET /health` — Health check
