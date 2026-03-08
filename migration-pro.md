# Migration Plan: Merge maps-scraper-pro into google-maps-scraper

## Context
`maps-scraper-pro` is a SaaS layer built on top of `google-maps-scraper`. It adds a REST API,
admin dashboard, River job queue, cloud provisioning, and multi-user auth. Both repos are open
source and being merged into a single module (`github.com/gosom/google-maps-scraper`) with one
`go.mod`. SaaS packages are merged INTO existing packages where they overlap, and added as new
top-level packages where they don't.

---

## Final Directory Layout

```
google-maps-scraper/
  # Existing packages — extended, not replaced
  gmaps/              ← unchanged
  runner/             ← unchanged
  web/                ← unchanged
  webdata/            ← unchanged
  postgres/           ← extended: SaaS Connect/pgxpool merged in as pool.go
  deduper/            ← unchanged
  exiter/             ← unchanged
  leadsdb/            ← unchanged
  s3uploader/         ← unchanged
  tlmt/               ← unchanged
  main.go             ← unchanged (existing CLI)

  # New packages from SaaS layer (no overlap with existing)
  admin/              ← admin dashboard, templates/, static/ (embedded)
  api/                ← REST API + api/docs/ (swagger, embedded)
  cli/                ← CLI prompt/UI utilities
  cryptoext/          ← encryption utilities
  env/                ← environment variable handling
  httpext/            ← HTTP utilities & middleware
  infra/              ← cloud provisioning (digitalocean, hetzner, planetscale, vps, cloudinit)
  log/                ← structured logging
  migrations/         ← SQL migrations (embedded *.sql files)
  saas/               ← root constants (package saas, env var names)
  ratelimit/          ← rate limiting + ratelimit/postgres
  rqueue/             ← River job queue integration
  scraper/            ← scraper manager (provider, writer, lifecycle)

  # New binary
  cmd/
    gmapssaas/        ← SaaS binary (serve, worker, provision, update, admin subcommands)

  go.mod              ← merged single go.mod
  # go.work + go.work.sum → DELETED
```

---

## Steps

### 1. Create a feature branch
```bash
git checkout -b feature/merge-saas
```

### 2. Delete go.work and go.work.sum
The existing `go.work` only contains `use .` and is no longer needed.

### 3. Copy packages from maps-scraper-pro

```bash
PRO=/home/giorgos/Development/github.com/gosom/maps-scraper-pro
DST=/home/giorgos/Development/github.com/gosom/google-maps-scraper

# Merge SaaS postgres into existing postgres/ (add as a new file, same package)
cp $PRO/postgres/postgres.go $DST/postgres/pool.go

# New packages (no overlap — recursive copy includes embedded assets)
cp -r $PRO/admin       $DST/admin
cp -r $PRO/api         $DST/api
cp -r $PRO/cli         $DST/cli
cp -r $PRO/cryptoext   $DST/cryptoext
cp -r $PRO/env         $DST/env
cp -r $PRO/httpext     $DST/httpext
cp -r $PRO/infra       $DST/infra
cp -r $PRO/log         $DST/log
cp -r $PRO/migrations  $DST/migrations
cp -r $PRO/ratelimit   $DST/ratelimit
cp -r $PRO/rqueue      $DST/rqueue
cp -r $PRO/scraper     $DST/scraper

# Root constants → saas/ subdirectory (package saas)
mkdir -p $DST/saas
cp $PRO/constants.go $DST/saas/constants.go

# New binary (rename gmapspro → gmapssaas)
mkdir -p $DST/cmd
cp -r $PRO/cmd/gmapspro $DST/cmd/gmapssaas
```

### 4. Rename package declaration and directory references

Change `package mapspro` to `package saas` in constants file:
```bash
sed -i 's|^package mapspro|package saas|' saas/constants.go
```

### 5. Rewrite import paths in all copied files

Run across all newly added directories. **Apply longest paths first** to avoid partial matches.

```bash
find admin api cli cryptoext env httpext infra log migrations ratelimit rqueue scraper saas cmd/gmapssaas postgres/pool.go -name "*.go" | \
xargs sed -i \
  -e 's|github.com/gosom/maps-scraper-pro/cmd/gmapspro/cmdadmin|github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdadmin|g' \
  -e 's|github.com/gosom/maps-scraper-pro/cmd/gmapspro/cmdcommon|github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdcommon|g' \
  -e 's|github.com/gosom/maps-scraper-pro/cmd/gmapspro/cmdprovision|github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdprovision|g' \
  -e 's|github.com/gosom/maps-scraper-pro/cmd/gmapspro/cmdserve|github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdserve|g' \
  -e 's|github.com/gosom/maps-scraper-pro/cmd/gmapspro/cmdupdate|github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdupdate|g' \
  -e 's|github.com/gosom/maps-scraper-pro/cmd/gmapspro/cmdworker|github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdworker|g' \
  -e 's|github.com/gosom/maps-scraper-pro/admin/postgres|github.com/gosom/google-maps-scraper/admin/postgres|g' \
  -e 's|github.com/gosom/maps-scraper-pro/api/docs|github.com/gosom/google-maps-scraper/api/docs|g' \
  -e 's|github.com/gosom/maps-scraper-pro/api/postgres|github.com/gosom/google-maps-scraper/api/postgres|g' \
  -e 's|github.com/gosom/maps-scraper-pro/infra/cloudinit|github.com/gosom/google-maps-scraper/infra/cloudinit|g' \
  -e 's|github.com/gosom/maps-scraper-pro/infra/digitalocean|github.com/gosom/google-maps-scraper/infra/digitalocean|g' \
  -e 's|github.com/gosom/maps-scraper-pro/infra/hetzner|github.com/gosom/google-maps-scraper/infra/hetzner|g' \
  -e 's|github.com/gosom/maps-scraper-pro/infra/planetscale|github.com/gosom/google-maps-scraper/infra/planetscale|g' \
  -e 's|github.com/gosom/maps-scraper-pro/infra/vps|github.com/gosom/google-maps-scraper/infra/vps|g' \
  -e 's|github.com/gosom/maps-scraper-pro/ratelimit/postgres|github.com/gosom/google-maps-scraper/ratelimit/postgres|g' \
  -e 's|github.com/gosom/maps-scraper-pro/admin|github.com/gosom/google-maps-scraper/admin|g' \
  -e 's|github.com/gosom/maps-scraper-pro/api|github.com/gosom/google-maps-scraper/api|g' \
  -e 's|github.com/gosom/maps-scraper-pro/cli|github.com/gosom/google-maps-scraper/cli|g' \
  -e 's|github.com/gosom/maps-scraper-pro/cryptoext|github.com/gosom/google-maps-scraper/cryptoext|g' \
  -e 's|github.com/gosom/maps-scraper-pro/env|github.com/gosom/google-maps-scraper/env|g' \
  -e 's|github.com/gosom/maps-scraper-pro/httpext|github.com/gosom/google-maps-scraper/httpext|g' \
  -e 's|github.com/gosom/maps-scraper-pro/infra|github.com/gosom/google-maps-scraper/infra|g' \
  -e 's|github.com/gosom/maps-scraper-pro/log|github.com/gosom/google-maps-scraper/log|g' \
  -e 's|github.com/gosom/maps-scraper-pro/migrations|github.com/gosom/google-maps-scraper/migrations|g' \
  -e 's|github.com/gosom/maps-scraper-pro/postgres|github.com/gosom/google-maps-scraper/postgres|g' \
  -e 's|github.com/gosom/maps-scraper-pro/ratelimit|github.com/gosom/google-maps-scraper/ratelimit|g' \
  -e 's|github.com/gosom/maps-scraper-pro/rqueue|github.com/gosom/google-maps-scraper/rqueue|g' \
  -e 's|github.com/gosom/maps-scraper-pro/scraper|github.com/gosom/google-maps-scraper/scraper|g' \
  -e 's|"github.com/gosom/maps-scraper-pro"|"github.com/gosom/google-maps-scraper/saas"|g'
```

Also update the import alias used in `cmd/gmapssaas/cmdserve/cmd_serve.go` (was `gmapspro`, now refers to `saas` package):
```bash
sed -i 's|gmapspro "github.com/gosom/google-maps-scraper/saas"|saas "github.com/gosom/google-maps-scraper/saas"|g' \
  cmd/gmapssaas/cmdserve/cmd_serve.go
```

Then grep every file that uses the old alias `gmapspro.` to reference constants and update to `saas.`:
```bash
find cmd/gmapssaas -name "*.go" | xargs sed -i 's|gmapspro\.|saas.|g'
```

> **Notes:**
> - `rqueue/rqueue.go` already imports `github.com/gosom/google-maps-scraper/gmaps` and `.../exiter` — those stay unchanged.
> - `postgres/pool.go` already has `package postgres` — no package declaration change needed.
> - `saas/constants.go` package declaration is changed from `package mapspro` to `package saas` in step 4.

### 6. Update swagger contact URL and regenerate docs

`api/doc.go` line 8 contains `@contact.url https://github.com/gosom/maps-scraper-pro` — this is a
URL (not a Go import), so the sed in step 5 won't catch it. Fix manually:
```bash
sed -i 's|https://github.com/gosom/maps-scraper-pro|https://github.com/gosom/google-maps-scraper|g' api/doc.go
```

Then regenerate swagger docs (requires `swag` CLI):
```bash
swag init -g api/doc.go -o api/docs
```

### 7. Merge go.mod

Add new **direct** dependencies from SaaS layer:
```
github.com/digitalocean/godo                        v1.173.0
github.com/go-chi/chi/v5                            v5.2.4
github.com/hetznercloud/hcloud-go/v2                v2.36.0
github.com/pquerna/otp                              v1.5.0
github.com/riverqueue/river                         v0.30.1
github.com/riverqueue/river/riverdriver/riverpgxv5  v0.30.1
github.com/riverqueue/river/rivertype               v0.30.1
github.com/rubenv/sql-migrate                       v1.8.1
github.com/skip2/go-qrcode                          v0.0.0-20200617195104-da1b6568686e
github.com/speps/go-hashids/v2                      v2.0.1
github.com/swaggo/http-swagger/v2                   v2.0.2
github.com/swaggo/swag                              v1.16.6
github.com/urfave/cli/v3                            v3.6.2
riverqueue.com/riverui                              v0.14.0
```

Promote `golang.org/x/crypto` from indirect to **direct** (used by `rqueue/worker_jobs.go` for SSH):
```
golang.org/x/crypto                                 v0.47.0
```

Bump conflicting packages to the higher version:
- `github.com/jackc/pgx/v5`: v5.7.4 → v5.8.0
- `github.com/prometheus/client_golang`: v1.19.1 → v1.23.2
- `github.com/gabriel-vasile/mimetype`: v1.4.9 → v1.4.12
- `github.com/go-playground/validator/v10`: v10.26.0 → v10.30.1
- `github.com/stretchr/testify`: v1.10.0 → v1.11.1
- `golang.org/x/sync`: v0.16.0 → v0.19.0
- `golang.org/x/net`: v0.42.0 → v0.49.0
- `golang.org/x/sys`: v0.39.0 → v0.40.0
- `golang.org/x/text`: v0.27.0 → v0.33.0
- `golang.org/x/mod`: v0.25.0 → v0.32.0
- `golang.org/x/tools`: v0.34.0 → v0.41.0
- `google.golang.org/protobuf`: v1.36.6 → v1.36.8

Remove `github.com/gosom/google-maps-scraper` from require (SaaS layer used it as external dep; now it's local).

Then run: `go mod tidy`

### 8. Build verification
```bash
go build ./...
go build -o bin/google-maps-scraper .
go build -o bin/gmapssaas ./cmd/gmapssaas/
go test ./...
```

---

## Critical Files
- `go.mod` — merged deps + version bumps
- `postgres/pool.go` — SaaS Connect() added to existing postgres package; no symbol clash (verified: existing package only exports NewProvider, NewResultWriter, ProviderOption, WithBatchSize using `*sql.DB`)
- `cmd/gmapssaas/cmdserve/cmd_serve.go` — most import-dense file (13 internal imports); first to validate
- `rqueue/rqueue.go` — imports both SaaS packages AND existing gmaps/exiter; verify no rewrites break it
- `rqueue/worker_jobs.go` — imports cryptoext, infra, infra/cloudinit, log from SaaS layer
- `saas/constants.go` — verify package declaration is `package saas`
- `admin/state.go` — contains `//go:embed templates/*.html` and `//go:embed static/*`; verify embedded assets copied correctly
- `migrations/migrations.go` — contains `//go:embed *.sql`; verify SQL files are in place

## Embedded Assets Checklist
These files travel with their packages via `cp -r` but must be present for `//go:embed` to work:
- `admin/templates/` — 12 HTML files (dashboard, login, 2fa, jobs, workers, etc.)
- `admin/static/` — 5 files (pico.min.css, styles.css, xterm.css, xterm.min.js, xterm-addon-fit.min.js)
- `migrations/*.sql` — 7 SQL migration files

## Out of Scope (handle separately)
- **Dockerfile**: Target repo already has its own. Consider adding `Dockerfile.saas` for the SaaS binary.
- **Makefile**: Target repo already has its own. Merge SaaS targets (dev, migrate, gen) as needed.
- **docker-compose**: Target repo has `docker-compose.dev.yaml`. Merge postgres service from SaaS compose.
- **examples-api/**: API usage examples from the SaaS repo — copy if desired.
