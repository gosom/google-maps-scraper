# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build          # build main binary → bin/google_maps_scraper
make test           # run all tests with race detection
make lint           # run golangci-lint
make vet            # run go vet
make format         # gofmt -s -w .
make test-cover     # test + coverage stats

go test ./runner/... # run a single package's tests
```

SaaS-specific:
```bash
make build-saas         # build SaaS binary → bin/gmapssaas
make saas-dev           # start postgres, run migrations, create admin, hot-reload server
make saas-migrate-up    # run pending migrations (requires sql-migrate)
make saas-migrate-new name=xxx  # create migration file
```

## Architecture

Entry point `main.go` reads CLI flags via `runner.ParseConfig()`, then uses a factory (`runnerFactory`) to select a `runner.Runner` implementation based on `cfg.RunMode`.

**Run modes** (constants in `runner/runner.go`):

| Mode | Trigger | Runner |
|------|---------|--------|
| `RunModeFile` | `-input` flag, no `-dsn` | `runner/filerunner` |
| `RunModeDatabase` | `-dsn` | `runner/databaserunner` |
| `RunModeDatabaseProduce` | `-dsn -produce` | `runner/databaserunner` |
| `RunModeWeb` | `-web` or no input/dsn | `runner/webrunner` |
| `RunModeAwsLambda` | `-aws-lambda` | `runner/lambdaaws` |
| `RunModeAwsLambdaInvoker` | `-aws-lambda-invoker` | `runner/lambdaaws` |
| `RunModeInstallPlaywright` | `PLAYWRIGHT_INSTALL_ONLY=1` | `runner/installplaywright` |

**Core packages:**

- `gmaps/` — scraping domain model: `Entry` (result struct), `SearchJob`, `PlaceJob`, `EmailJob`, `ReviewsJob`. Jobs implement `scrapemate.IJob`.
- `scraper/` — `Scraper` wraps `scrapemate` + Playwright; `CentralWriter` fans out results to multiple output backends; `Provider` interface (file or DB-backed).
- `runner/` — `Config` struct, flag parsing, telemetry init. Each sub-package implements `Runner`.
- `web/` — chi HTTP router serving REST API + embedded static SPA + WebSocket live updates. SQLite-backed via `web/sqlite/`.
- `postgres/` — PostgreSQL `Provider` and `ResultWriter` for distributed multi-worker mode.

**SaaS edition** (`cmd/gmapssaas/`) is a separate binary with its own CLI (urfave/cli v3). It adds multi-tenancy, API keys, admin UI, River job queue (PostgreSQL-backed), DigitalOcean/Hetzner cloud provisioning, and Prometheus metrics. Migrations live in `migrations/` and are managed with `sql-migrate`.

**Underlying framework:** [`gosom/scrapemate`](https://github.com/gosom/scrapemate) handles concurrency, job scheduling, and the crawl loop. Playwright-go drives the headless Chromium browser.

## Code Style

- Import order: stdlib → third-party → local (`github.com/gosom/google-maps-scraper/...`)
- Errors: `fmt.Errorf("...: %w", err)` wrapping; return don't panic (except config validation in `ParseConfig`)
- Interfaces: `-er` suffix (`Runner`, `S3Uploader`); `context.Context` as first param
- Constants: CamelCase (`RunModeFile`, not `RUN_MODE_FILE`)
- `nolint` comments require inline explanation
