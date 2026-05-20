# Env Config Consolidation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate ~63 scattered `os.Getenv` calls and 9 duplicate helper functions into a single typed `Config` struct loaded once at startup, then injected via existing dependency-injection seams. After this work, `os.Getenv` appears in zero files outside `pkg/config`.

**Architecture:** New `pkg/config` package using `caarlos0/env/v11` (the only candidate that is actively maintained in 2026, has zero runtime deps, supports generics, and matches the single-source env-only use case). Struct-tag-driven parsing with required/default/prefix support. `Load()` returns `*Config` or fails with an aggregated error at startup — no environment read ever happens at request time. Logger is constructed once from `cfg.LogLevel` and injected through the existing `handlers.Dependencies` pattern (same pattern we just used for `appenv.Environment`). The work is split into 5 incremental, independently-shippable PRs to keep blast radius small.

**Tech Stack:**
- Go 1.23+
- [`github.com/caarlos0/env/v11`](https://github.com/caarlos0/env) — env-config library (latest v11.4.0, 2026-02-22; commit activity within 2 weeks of plan date)
- Existing `pkg/appenv` (already in repo from prior commit `19851d8`)
- Existing `slog` + `pkg/logger` package
- `testify` (already a project dep)

**Verified facts (re-checked against source 2026-04-27):**
- `web/handlers/integration.go:46-50` — `googleConfig()` reads `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL` at every OAuth request (called from lines 89, 131, 344). **Correctness/perf bug.**
- `pkg/encryption/encryption.go:71-93` — deprecated package-level `Encrypt()`/`Decrypt()` read `ENCRYPTION_KEY` per call. **Zero callers in production code** → safe to delete.
- `runner/runner.go:257,261,265` — reads `MY_AWS_ACCESS_KEY` / `MY_AWS_SECRET_KEY` / `MY_AWS_REGION` as fallback after CLI flags. Different code path from `runner/webrunner/webrunner.go:401-404` which reads `AWS_*`. **Two names, same purpose.**
- `LOG_LEVEL` is read in **11 distinct files**: `main.go`, `web/web.go`, `web/handlers/integration.go`, `web/services/{results,costs,credit,estimation}.go`, `s3uploader/s3uploader.go`, `postgres/{migration,repository}.go`, `billing/service.go`. Logger should be DI'd, not re-constructed per package.
- `STRIPE_SUCCESS_URL`/`STRIPE_CANCEL_URL` are read via `config.Service.GetString()` which has an env-override at `config/config.go:envOverride()` — DB-backed dynamic config. Distinct concern from process-startup config; **must NOT be moved into `pkg/config`**.
- `runner.Config` (existing struct) is **flag-driven** for the CLI runner. Coexists with our new `pkg/config.Config` (env-driven for the web server). Don't merge them.
- `web/scrape.go` defines a parallel `Config` struct with its own `getEnv` helper for scraper-only settings. Out of scope for this plan; tracked as future cleanup.

---

## Library choice: `caarlos0/env/v11`

Validated 2026-04-27 via direct GitHub inspection:

| Candidate | Last commit | Generics | Deps | Verdict |
|---|---|---|---|---|
| **caarlos0/env v11** | 2026-04-09 | yes (`ParseAs[T]`) | 0 runtime | **PICK** |
| kelseyhightower/envconfig | 2025-06-28 (CI only) | no | 0 | freeze, skip |
| sethvargo/go-envconfig | 2025-05-01 | no | 0 | runner-up |
| ardanlabs/conf | 2026-02-28 | no | minimal | bundles flags+help, overkill |
| knadh/koanf | 2026-03-31 | partial | modular | multi-source, overkill |
| spf13/viper | 2025-10-15 | no | heavy | wrong tool — multi-source |

`caarlos0/env` v11 is the only candidate that is simultaneously (a) actively maintained (commit < 30 days ago), (b) zero runtime dependencies, (c) generics-aware, (d) feature-complete for typed parsing + required/default/prefix tags + aggregated error reporting. No newer entrant has eclipsed it as of 2026-04-27.

---

## Full env var manifest (canonical, all 33 Go-binary vars)

This is the authoritative list of env vars actually read by the Go binary (excluding docker-compose-only, CI-only, and dead vars). Every var below MUST appear in `pkg/config.Config`.

### Core / runtime (12)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `APP_ENV` | `appenv.Environment` | no | `development` | Already typed via `pkg/appenv` |
| `LOG_LEVEL` | string | no | `info` | 11 read sites today |
| `LOG_OUTPUT` | string | no | `both` | `pkg/logger/logger.go` |
| `LOG_FILE_PATH` | string | no | `""` | Overrides `LOG_DIR`+`LOG_FILE_NAME` |
| `LOG_DIR` | string | no | `logs` | |
| `LOG_FILE_NAME` | string | no | `brezel-api.log` | |
| `LOG_MAX_SIZE_MB` | int | no | (logger default) | |
| `LOG_RETENTION_DAYS` | int | no | (logger default) | |
| `INTERNAL_ADDR` | string | no | `:9090` | `runner/webrunner/webrunner.go:226` |
| `DATA_FOLDER` | string | no | `./webdata` | `web/scrape.go:getEnv` (also flag) |
| `CONCURRENCY` | int | no | auto | `runner/runner.go` (also flag) |
| `DISABLE_TELEMETRY` | bool | no | `false` | `runner/runner.go` |

### Database (6)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `DSN` | string | **always** | — | `runner/runner.go`, `webrunner.go` |
| `MIGRATION_DSN` | string | no | `""` | optional separate DSN |
| `DB_MAX_OPEN_CONNS` | int | no | `25` | webrunner + databaserunner |
| `DB_MAX_IDLE_CONNS` | int | no | `10` | |
| `DB_CONN_MAX_LIFETIME` | duration | no | `5m` | |
| `DB_CONN_MAX_IDLE_TIME` | duration | no | `2m` | webrunner only |

### Auth & crypto (3)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `CLERK_SECRET_KEY` | string | **always** | — | webrunner.go:178 |
| `API_KEY_SERVER_SECRET` | `[]byte` (≥32) | **prod-only** | — | HMAC root |
| `ENCRYPTION_KEY` | string (==32) | **prod-only** | — | AES-256-GCM key |

### Stripe (5)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `STRIPE_SECRET_KEY` | string | **prod-only** | — | |
| `STRIPE_WEBHOOK_SECRET` | string | **prod-only** | — | current signing secret |
| `STRIPE_WEBHOOK_SECRET_PREVIOUS` | string | no | `""` | rotation overlap |
| `STRIPE_WEBHOOK_ALLOWED_CIDRS` | `[]string` | no | `nil` | comma-separated |

### CORS (1)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `ALLOWED_ORIGINS` | `[]string` | **prod-only** | `nil` | comma-separated |

### Stuck-job + webhook cleanup (3)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `STUCK_JOB_CHECK_INTERVAL_MINUTES` | int | no | `10` | |
| `STUCK_JOB_TIMEOUT_HOURS` | int | no | `4` | |
| `WEBHOOK_EVENT_RETENTION_DAYS` | int | no | `90` | |

### AWS / S3 (4 — consolidate `MY_AWS_*` aliases)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `AWS_ACCESS_KEY_ID` | string | no | `""` | Drop `MY_AWS_ACCESS_KEY` alias |
| `AWS_SECRET_ACCESS_KEY` | string | no | `""` | Drop `MY_AWS_SECRET_KEY` alias |
| `AWS_REGION` | string | no | `us-east-1` | Drop `MY_AWS_REGION` alias |
| `S3_BUCKET_NAME` | string | no | `""` | |

### Google (4)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `GOOGLE_CLIENT_ID` | string | no | `""` | **request-time bug today** |
| `GOOGLE_CLIENT_SECRET` | string | no | `""` | **request-time bug today** |
| `GOOGLE_REDIRECT_URL` | string | no | `""` | **request-time bug today** |
| `GOOGLE_COOKIES_FILE` | string | no | `""` | |

### External services (3)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `WEBSHARE_API_KEY` | string | no | `""` | |
| `RESEND_API_KEY` | string | no | `""` | optional support email |
| `PROXIES` | `[]string` | no | `nil` | testing fallback |

### Build metadata (read once at /version) (4)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `GIT_COMMIT` | string | no | `""` | injected by CI |
| `BUILD_DATE` | string | no | `""` | injected by CI |
| `VERSION` | string | no | `""` | injected by CI |
| `ENVIRONMENT` | string | no | `development` | tag for `/version` endpoint; distinct from `APP_ENV` |

### CLI bootstrap (1)
| Var | Type | Required | Default | Notes |
|---|---|---|---|---|
| `PLAYWRIGHT_INSTALL_ONLY` | bool | no | `false` | `runner/runner.go:207` — read **before** `config.Load()` to short-circuit normal startup and just install Playwright browsers. Lives in `pkg/config.LoadCLIBootstrap()` (a tiny helper that reads only this one var) so the CI grep gate stays green. |

**Total Go-binary vars: 34** (33 long-lived + 1 CLI bootstrap). docker-compose-only vars (`DOCKER_IMAGE`, `GRAFANA_*`, `NGINX_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `API_PORT`, `INTERNAL_PORT`, `WEB_ADDR`) stay in compose files — they are NOT consumed by the Go binary.

### To delete (dead / deprecated)
- `MY_AWS_ACCESS_KEY`, `MY_AWS_SECRET_KEY`, `MY_AWS_REGION` — duplicate names; consolidate to `AWS_*`
- `pkg/encryption.Encrypt()` / `pkg/encryption.Decrypt()` — deprecated, zero callers
- `DATABASE_URL` (in `web/scrape.go`) — read by parallel scraper config but never used in main flow
- `AWS_LAMBDA_FUNCTION_NAME` — never read in Go code
- `CLERK_API_KEY` (only in `.env.development`) — should be `CLERK_SECRET_KEY`

---

## File structure

```
pkg/config/                    # NEW PACKAGE
  config.go                    # Config struct + Load + Validate
  config_test.go               # Unit tests
  doc.go                       # Package doc

pkg/encryption/encryption.go   # MODIFIED — delete deprecated Encrypt/Decrypt funcs
pkg/logger/logger.go           # MODIFIED — accept opts; remove envInt
runner/runner.go               # MODIFIED — drop MY_AWS_* fallbacks, use cfg.AWS
runner/webrunner/webrunner.go  # MODIFIED — replace ~30 os.Getenv reads with cfg.X
runner/databaserunner/databaserunner.go  # MODIFIED — drop dbEnvInt/dbEnvDuration
web/web.go                     # MODIFIED — accept *config.Config + *slog.Logger
web/handlers/handlers.go       # MODIFIED — Dependencies receives Logger + Google config
web/handlers/integration.go    # MODIFIED — googleConfig() uses injected config
web/handlers/version.go        # MODIFIED — read build metadata from cfg
web/services/*.go              # MODIFIED — accept logger via constructor
billing/service.go             # MODIFIED — accept logger via constructor
postgres/{migration,repository}.go  # MODIFIED — accept logger via constructor
s3uploader/s3uploader.go       # MODIFIED — accept logger via constructor
main.go                        # MODIFIED — Load config, build logger, pass everywhere

.env.example                   # MODIFIED — sync to canonical 33-var list
go.mod / go.sum                # MODIFIED — add caarlos0/env/v11
.github/workflows/ci.yml       # MODIFIED (or new) — add grep gate
```

---

## Chunk 1: Foundation — pkg/config package (additive, no callers wired)

This chunk adds the new package with full tests but does NOT touch any existing reader. After this chunk, `go build ./...` and existing tests continue to pass; nothing is cut over yet.

### Task 1.1: Add `caarlos0/env/v11` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add dependency**

```bash
cd /Users/yasseen/documents/brezel.ai/BrezelScraper/brezelscraper-backend
go get github.com/caarlos0/env/v11@v11.4.0
go mod tidy
```

- [ ] **Step 2: Verify it added cleanly**

```bash
grep 'caarlos0/env' go.mod
# expect: github.com/caarlos0/env/v11 v11.4.0
go list -m all | grep caarlos0
# expect a single line; no transitive runtime deps
```

- [ ] **Step 3: Build everything still compiles**

```bash
go build ./...
# expect: no output (success)
```

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): add caarlos0/env/v11 for typed env config"
```

### Task 1.2: Create `pkg/config` skeleton + doc

**Files:**
- Create: `pkg/config/doc.go`

- [ ] **Step 1: Write the package doc**

```go
// Package config is the single boundary between the operating-system
// environment and the Go process. APP_ENV, DSN, CLERK_SECRET_KEY, every
// secret and every tunable, are read here exactly once at startup.
//
// All other packages receive a *Config (or a focused sub-view of it) by
// dependency injection. After this package is wired in, the codebase
// must satisfy this invariant:
//
//	grep -rn 'os.Getenv\|os.LookupEnv' --include='*.go' .  | \
//	    grep -v '_test.go' | grep -v 'pkg/config/'
//	(empty result)
//
// Why one place:
//
//   - Single manifest. The Config struct *is* the documentation of what
//     this binary consumes from the environment.
//   - Fail-fast. Required fields are validated at Load() time before any
//     handler can serve a request.
//   - Test-friendly. Tests construct *Config directly; no t.Setenv plumbing.
//   - Immutable. *Config is treated as read-only after Load() returns.
//
// This package depends only on appenv and the caarlos0/env/v11 parser.
// It does not depend on any other internal package — keeping the import
// graph one-directional out of config into everything else.
package config
```

- [ ] **Step 2: Verify file compiles** — `go build ./pkg/config/...`. Expect: no output.
- [ ] **Step 3: Commit** — `git add pkg/config/doc.go && git commit -m "feat(config): add pkg/config package skeleton"`

### Task 1.3: Define `Config` struct + nested groups (TDD)

**Files:**
- Create: `pkg/config/config.go`
- Create: `pkg/config/config_test.go`

- [ ] **Step 1: Write the failing test for `Load()` happy path**

```go
// pkg/config/config_test.go
package config

import (
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/appenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("DSN", "postgres://localhost/test")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_x")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, appenv.Development, cfg.AppEnv)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 25, cfg.DB.MaxOpenConns)
	assert.Equal(t, "us-east-1", cfg.AWS.Region)
}
```

- [ ] **Step 2: Run test, verify it fails** — `go test ./pkg/config/... -run TestLoad_Defaults`. Expect: FAIL (`Load` undefined).

- [ ] **Step 3: Write minimal `Config` + `Load`** — full struct per the manifest above. Use `env:"NAME"`, `envDefault:"..."`, `envSeparator:","`, `envPrefix:"DB_"` for nested. AppEnv field uses a custom unmarshaler bridging to `appenv.Parse`. Implement `Load()` with `env.ParseAs[Config]()` (generic API). Return `(*Config, error)`.

- [ ] **Step 4: Run test, verify it passes** — `go test ./pkg/config/... -run TestLoad_Defaults -v`. Expect: PASS.

- [ ] **Step 5: Commit** — `git add pkg/config/ && git commit -m "feat(config): add Config struct and Load with defaults"`

### Task 1.4: Add validation for required-in-prod fields (TDD)

- [ ] **Step 1: Write failing test for production fail-fast**

```go
func TestLoad_ProductionRequiresEncryptionKey(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DSN", "postgres://prod/db")
	t.Setenv("CLERK_SECRET_KEY", "sk_live_x")
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_y")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_z")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("API_KEY_SERVER_SECRET", strings.Repeat("a", 32))
	t.Setenv("ENCRYPTION_KEY", "") // missing!

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ENCRYPTION_KEY")
}
```

- [ ] **Step 2: Run, fail.** `go test ./pkg/config/... -run TestLoad_Production -v`
- [ ] **Step 3: Implement `Validate(*Config) error`** — same logic as `runner/webrunner/webrunner.go:185-207` today, but in one place. Aggregate missing fields into a single `errors.Join`. Wire into `Load()` after `env.ParseAs`.
- [ ] **Step 4: Run, pass.**
- [ ] **Step 5: Add cases for**: `STRIPE_SECRET_KEY` missing in prod, `ALLOWED_ORIGINS` missing in prod, `STRIPE_WEBHOOK_SECRET` missing in prod, `API_KEY_SERVER_SECRET < 32 bytes`, `ENCRYPTION_KEY != 32 bytes`. One subtest per case.
- [ ] **Step 6: Add negative test** — invalid `APP_ENV` produces error with `"APP_ENV"` substring (delegates to `appenv.Parse`).
- [ ] **Step 7: Commit** — `git commit -m "feat(config): production validation with aggregated errors"`

### Task 1.5: Add `.env.example` parity test

This test asserts `.env.example` lists exactly the set of vars the struct declares — closes the drift loop.

- [ ] **Step 1: Write failing test** — parse struct via reflection, extract every `env:"NAME"` tag, read `.env.example`, assert symmetric difference is empty.
- [ ] **Step 2: Run, expect failure** (current `.env.example` is missing 30+ vars).
- [ ] **Step 3: Update `.env.example`** to match the canonical 33-var manifest exactly. Sort alphabetically within each category. Group by category headers identical to the struct's category groupings.
- [ ] **Step 4: Run, pass.**
- [ ] **Step 5: Commit** — `git add .env.example pkg/config/ && git commit -m "feat(config): enforce .env.example parity via test"`

### Task 1.6: Run full test suite, ensure nothing broke (no callers wired yet)

- [ ] `go test ./... 2>&1 | tail -30` — expect all green.
- [ ] `go vet ./...` — expect clean.
- [ ] `grep -rn 'os.Getenv' --include='*.go' . | grep -v '_test.go' | wc -l` — expect ~63 (unchanged; we haven't migrated yet).

---

## Chunk 2: Wire pkg/config into webrunner (replace ~30 reads)

After this chunk, `runner/webrunner/webrunner.go` reads from `*config.Config` instead of `os.Getenv`. Duplicate helper functions deleted.

### Task 2.1: Plumb `*config.Config` through `runner/webrunner.New`

**Files:**
- Modify: `main.go` — call `config.Load()`, pass `*config.Config` into runner.
- Modify: `runner/runner.go` (`runner.Runner` constructor) — accept `*config.Config`.
- Modify: `runner/webrunner/webrunner.go:269` — `New()` signature gains `cfg *config.Config`.
- Modify: `runner/databaserunner/databaserunner.go:New` — accept `*config.Config`.

- [ ] **Step 1**: In `main.go`, `cfg, err := config.Load(); if err != nil { log.Fatal(err) }`. Remove existing `os.Getenv("LOG_LEVEL")` (logger comes in Chunk 3).
- [ ] **Step 2**: Update `runner.New(cfg, logger)` and downstream signatures.
- [ ] **Step 3**: Build — expect failures listing every callsite that must update. Fix iteratively.
- [ ] **Step 4**: Run all tests — expect pass (signatures match, behavior unchanged).
- [ ] **Step 5**: Commit — `git commit -m "refactor(runner): plumb *config.Config from main"`

### Task 2.2: Replace `os.Getenv` reads in `runner/webrunner/webrunner.go`

**Files:**
- Modify: `runner/webrunner/webrunner.go` — every `os.Getenv` read becomes `cfg.X`.
- Modify: `runner/webrunner/webrunner_startup_test.go` — flip to constructing `*config.Config` directly.

- [ ] **Step 0 (TDD red)**: Update one assertion in `webrunner_startup_test.go` to expect `buildServerConfig` to take `*config.Config` instead of reading env. Run `go test ./runner/webrunner/... -run TestBuildServerConfig_FailsInProductionWhenEncryptionKeyMissing -v`. Expect: FAIL with signature mismatch — this is the failing test that gates the migration.

- [ ] **Step 1**: Replace at line 178 — `cfg.ClerkSecretKey`. Then 213 (`cfg.APIKeyServerSecret`), 226 (`cfg.InternalAddr`), 230-247 (the entire `web.ServerConfig` block), 246 (Resend, Google cookies), 401-404 (AWS), 188-203 (production validation block: replace with `cfg.Validate()` call — but Load already validated, so DELETE this block entirely).
- [ ] **Step 2**: Delete `parseCSVEnv` (line 65) — `caarlos0/env` handles `[]string` with `envSeparator`.
- [ ] **Step 3**: Delete `stripeWebhookSecretsFromEnv` (line 77) — replace with `cfg.Stripe.WebhookSecrets()` method that returns `[]string{Current, Previous}` filtered by non-empty.
- [ ] **Step 4**: Delete `envInt` (line 1567) and `envDuration` (line 1578) — DB pool fields come from `cfg.DB.*`, stuck-job fields from `cfg.StuckJob.*`, webhook retention from `cfg.WebhookRetentionDays`.
- [ ] **Step 5**: Delete the now-orphaned `isProduction := ...` reads (already removed in commit `19851d8` — just verify).
- [ ] **Step 6**: Build — `go build ./...`. Iterate until clean.
- [ ] **Step 7**: Run webrunner tests — `go test ./runner/webrunner/... -v`. Update `webrunner_startup_test.go` to construct `*config.Config` directly instead of `t.Setenv` + `appenv.Parse`. (The `t.Setenv` calls become unnecessary; tests construct typed config.)
- [ ] **Step 8**: Verify no `os.Getenv` remains in this file — `grep -n 'os.Getenv' runner/webrunner/webrunner.go`. Expect: empty.
- [ ] **Step 9**: Commit — `git commit -m "refactor(webrunner): replace os.Getenv with injected *config.Config"`

### Task 2.3: Replace `dbEnvInt`/`dbEnvDuration` in `databaserunner`

**Files:**
- Modify: `runner/databaserunner/databaserunner.go` — delete `dbEnvInt` (line 263), `dbEnvDuration` (line 272). Use `cfg.DB.MaxOpenConns` etc.

- [ ] **Step 1**: Replace reads with `cfg.DB.X`.
- [ ] **Step 2**: Delete the two helper funcs.
- [ ] **Step 3**: Build + test.
- [ ] **Step 4**: Commit — `git commit -m "refactor(databaserunner): use *config.Config; remove dbEnv helpers"`

### Task 2.4: Verify Chunk 2 invariants

- [ ] `grep -rn 'os.Getenv' --include='*.go' runner/ | grep -v '_test.go'` — expect: empty
- [ ] `grep -rnE 'func.*envInt|func.*envDuration|func.*parseCSVEnv|func.*stripeWebhookSecretsFromEnv' --include='*.go' runner/` — expect: empty (helpers deleted)
- [ ] `go test ./...` — all green

---

## Chunk 3: Inject `*slog.Logger` (remove 11 LOG_LEVEL reads)

### Task 3.1: Build logger once in `main.go`

- [ ] **Step 1**: After `config.Load()`, construct logger: `logger := pkglogger.New(cfg.LogLevel, cfg.LogOutput, ...)`.
- [ ] **Step 2**: Pass `logger` to `runner.New(cfg, logger)` (already in signature from Chunk 2).
- [ ] **Step 3**: Inside `webrunner.New`, build per-component loggers via `logger.With(slog.String("component", "webrunner"))` instead of `pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "webrunner")`.
- [ ] **Step 4**: Add `Logger *slog.Logger` to `web.ServerConfig` and `handlers.Dependencies`.
- [ ] **Step 5**: In `web.New`, use `cfg.Logger.With(slog.String("component", "api"))` instead of reading LOG_LEVEL.

### Task 3.2: Migrate the 11 reader sites — explicit TDD per file

Files to modify (separate commit per file for bisect-friendly history):
- `web/web.go` — replace `pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "api")` → derive from injected logger
- `web/handlers/integration.go:42`
- `web/services/results.go`, `costs.go`, `credit.go`, `estimation.go`
- `billing/service.go`
- `postgres/migration.go`, `postgres/repository.go`
- `s3uploader/s3uploader.go`

**Per-file TDD cycle** (apply to each of the 11 files):

- [ ] **Step A (red)**: Add a single test that constructs the service/handler with a captured `*slog.Logger` (use `slog.New(slog.NewTextHandler(buf, nil))` so output is inspectable). Assert that calling a method on the service writes to that logger — NOT to the global slog default. Run; expect FAIL because the constructor doesn't accept a logger yet.
- [ ] **Step B (green)**: Update the constructor signature to accept `logger *slog.Logger`. Replace internal `pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "<component>")` with `logger.With(slog.String("component", "<component>"))`. Update all callers in this PR's chunk. Run; expect PASS.
- [ ] **Step C (commit)**: One file, one commit. Format: `refactor(<package>): inject *slog.Logger via constructor`.

After all 11 files migrated:

- [ ] **Step D (verifier)**: `grep -rn 'os.Getenv("LOG_LEVEL")' --include='*.go' . | grep -v '_test.go' | grep -v 'pkg/config/'`. Expect: empty.
- [ ] **Step E (verifier)**: `grep -rn 'pkglogger.NewWithComponent' --include='*.go' . | grep -v 'pkg/logger/'`. Expect: empty (the helper now only used internally by `pkg/logger`).
- [ ] **Step F**: Delete `pkg/logger/logger.go:envInt` (the third duplicate — `LOG_MAX_SIZE_MB`, `LOG_RETENTION_DAYS` come from `*config.Config` now). Build + test + commit.

- [ ] **Step verifier**: `grep -rn 'os.Getenv("LOG_LEVEL")' --include='*.go' . | grep -v '_test.go'` — expect: empty.
- [ ] **Step verifier**: `pkg/logger/logger.go` — keep file and helpers, but only `pkg/logger.New(level, output, …)` is used now. Delete `pkg/logger/logger.go:envInt` (the third duplicate). `LOG_DIR`, `LOG_FILE_PATH`, etc. read inside `pkg/logger.New` are replaced by parameters from `*config.Config`.

---

## Chunk 4: Fix Google OAuth request-time bug + delete dead code

### Task 4.1: Inject Google config into `IntegrationHandler`

**Files:**
- Modify: `web/handlers/integration.go`

- [ ] **Step 1**: Write failing test asserting `googleConfig()` returns the same struct on repeated calls and does NOT consult `os.Getenv`.

```go
func TestIntegrationHandler_GoogleConfig_NoEnvAtRequestTime(t *testing.T) {
	h := NewIntegrationHandler(/*…*/, config.GoogleConfig{
		ClientID: "id-from-config", ClientSecret: "sec-from-config", RedirectURL: "https://x/cb",
	}, /*…*/)
	t.Setenv("GOOGLE_CLIENT_ID", "should-be-ignored")
	got := h.googleConfig()
	assert.Equal(t, "id-from-config", got.ClientID)
}
```

- [ ] **Step 2**: Add `cfg.Google.ClientID/Secret/RedirectURL` already present from Chunk 1; add Google config to `Dependencies`; pass to `NewIntegrationHandler` (constructor now takes 6 args including `appenv.Environment` from prior commit).
- [ ] **Step 3**: Replace `os.Getenv` reads at integration.go:48-50 with `h.google.ClientID`, `h.google.ClientSecret`, `h.google.RedirectURL`.
- [ ] **Step 3b**: Also replace the SECOND set of Google env reads at `web/handlers/integration.go:349-351` inside `HandleGetConfig`:
  ```go
  // BEFORE
  googleEnabled := os.Getenv("GOOGLE_CLIENT_ID") != "" &&
      os.Getenv("GOOGLE_CLIENT_SECRET") != "" &&
      os.Getenv("GOOGLE_REDIRECT_URL") != ""
  // AFTER
  googleEnabled := h.google.ClientID != "" && h.google.ClientSecret != "" && h.google.RedirectURL != ""
  ```
  Add a unit test for `HandleGetConfig` that returns `{"google_sheets": true}` when injected config has all three fields and `false` otherwise. Without this fix, the CI grep gate fails.
- [ ] **Step 4**: Run, pass.
- [ ] **Step 5**: Commit — `git commit -m "fix(integration): read Google OAuth config once at startup, not per request"`

### Task 4.2: Consolidate AWS aliases

**Files:**
- Modify: `runner/runner.go:255-266` — delete the `MY_AWS_*` fallback block.

- [ ] **Step 1**: Set `cfg.AWS.AccessKey/SecretKey/Region` from `*config.Config.AWS.*` (already populated in Chunk 1).
- [ ] **Step 2**: Delete the three `if cfg.AWS.X == "" { cfg.AWS.X = os.Getenv("MY_AWS_X") }` blocks.
- [ ] **Step 3**: Update `.env.example` if `MY_AWS_*` is mentioned — remove.
- [ ] **Step 4**: Test, commit — `git commit -m "refactor: consolidate AWS_* aliases; drop MY_AWS_* duplicates"`

### Task 4.3: Delete deprecated `pkg/encryption.Encrypt`/`Decrypt`

**Files:**
- Modify: `pkg/encryption/encryption.go` — delete lines 71-93.

- [ ] **Step 1**: Verify zero callers — `grep -rn 'encryption\.\(Encrypt\|Decrypt\)' --include='*.go' . | grep -v '_test.go'`. Expect: empty.
- [ ] **Step 2**: Delete the two functions.
- [ ] **Step 3**: Build + test.
- [ ] **Step 4**: Commit — `git commit -m "refactor(encryption): delete deprecated package-level Encrypt/Decrypt (no callers)"`

### Task 4.4: Delete other dead code

- [ ] `web/scrape.go:55` — remove `DATABASE_URL` field + the `getEnv("DATABASE_URL", "")` call. Note: this is via the `getEnv` helper, **not** direct `os.Getenv`, so it does NOT trip the Chunk 5 grep gate as currently scoped (the gate matches `os.Getenv`/`os.LookupEnv` only). However the value is also unused (the `Config.DatabaseURL` field is read by `GetDBConnectionString` at line 73 — itself unused in main flow). Verify with `grep -rn 'GetDBConnectionString' --include='*.go' .` that no caller exists, then delete field + helper read together.
- [ ] `web/scrape.go:66` — remove `AWS_LAMBDA_FUNCTION_NAME` field + `getEnv("AWS_LAMBDA_FUNCTION_NAME", "")` call. Same verification: confirm `cfg.AWSLambdaFunctionName` has no callers.
- [ ] `.env.development` — remove `CLERK_API_KEY` reference (typo of `CLERK_SECRET_KEY`).
- [ ] Commit each as a separate, single-purpose commit.

---

## Chunk 5: CI gate + final verification

### Task 5.1: Add grep gate to CI

**Files:**
- Modify (or Create): `.github/workflows/ci.yml`

- [ ] **Step 1**: Add a step. The gate has TWO clauses: (1) direct `os.Getenv`/`LookupEnv` outside the boundary; (2) the project-local helper functions whose names we know (`getEnv`, `getEnvOrDefault`, `envInt`, `envDuration`, `dbEnvInt`, `dbEnvDuration`, `parseCSVEnv`, `stripeWebhookSecretsFromEnv`) outside the boundary. After Chunks 1–4, all of these helpers are deleted; the gate ensures no one reintroduces them.

```yaml
- name: Enforce env-config boundary
  run: |
    set -e
    direct=$(grep -rn 'os\.Getenv\|os\.LookupEnv' --include='*.go' . \
      | grep -v '_test.go' \
      | grep -v 'pkg/config/' \
      | grep -v 'pkg/appenv/appenv.go' \
      || true)

    helpers=$(grep -rnE '\b(getEnv|getEnvOrDefault|envInt|envDuration|dbEnvInt|dbEnvDuration|parseCSVEnv|stripeWebhookSecretsFromEnv)\(' --include='*.go' . \
      | grep -v '_test.go' \
      | grep -v 'pkg/config/' \
      || true)

    if [ -n "$direct" ] || [ -n "$helpers" ]; then
      echo "::error::Env access found outside pkg/config boundary:"
      echo "DIRECT:"; echo "$direct"
      echo "HELPERS:"; echo "$helpers"
      exit 1
    fi
```

- [ ] **Step 2**: Verify locally — `bash -c '<the same script>'`. Expect: empty + exit 0.
- [ ] **Step 3**: Commit — `git commit -m "ci: enforce single env-read boundary in pkg/config"`

### Task 5.2: Final invariants

- [ ] `go test ./... -count=1` — all green
- [ ] `go vet ./...` — clean
- [ ] `golangci-lint run` — clean
- [ ] `grep -rn 'os.Getenv' --include='*.go' . | grep -v '_test.go' | grep -v 'pkg/config/'` — empty
- [ ] `grep -rnE 'func.*envInt|func.*envDuration|func.*parseCSVEnv' --include='*.go' .` — empty (no scattered helpers)
- [ ] `grep -rn 'os.Getenv("LOG_LEVEL")' --include='*.go' .` — empty
- [ ] `grep -rn 'MY_AWS_' --include='*.go' .` — empty

### Task 5.3: Update operator docs

**Files:**
- Modify: `docs/production-deployment.md` — point to `pkg/config/config.go` as the canonical env var manifest.
- Modify: `README.md` (or equivalent) — env var section refers to struct, not duplicate list.

---

## Risk register

| Risk | Mitigation |
|---|---|
| Hidden caller of `os.Getenv` or wrapped helper we missed | Chunk 5 grep gate is the safety net (covers BOTH direct calls and the project's named helpers `getEnv`/`envInt`/`envDuration`/etc.) |
| `caarlos0/env` semantic drift in a future bump | Pin minor version; package isolated to `pkg/config` so swap cost is bounded |
| Test that relies on env var without `t.Setenv` (i.e., reads global env) | Chunk 1 + Chunk 2 tests construct `*Config` directly, so production code paths no longer depend on env at test time. Existing tests using `t.Setenv` keep working because `Load()` is still env-driven. |
| `LOG_LEVEL` reads in service constructors gated something we don't know about | Each service migration is its own commit — bisect-friendly. Roll back any single commit independently. |
| Dropping `MY_AWS_*` breaks a forgotten deploy | Before merging Chunk 4: `grep -rn 'MY_AWS_' ~/Documents/brezel.ai/BrezelScraper/` (search secrets repo, deployment/ dir, ALL configs). |
| **Production deploy sets env via systemd `EnvironmentFile=` or DigitalOcean droplet env, not just docker-compose.** Removed/renamed vars (e.g. `MY_AWS_*`, `IS_PRODUCTION`, `DATABASE_URL`) may still be in those files. | Before each chunk merge: SSH to the prod box and run `systemctl cat brezel-api 2>/dev/null \| grep -E 'EnvironmentFile=\|Environment='` and `cat /etc/brezel/secrets/backend.env \| grep -vE '^#\|^$' \| cut -d= -f1 \| sort > /tmp/prod-env.txt`, then diff against the new `.env.example` keyspace. Per project memory, prod is on Netcup compute with the secrets repo cloned to `/srv/brezel/secrets-repo` and decrypted into `/etc/brezel/secrets/backend.env` by `brezel-decrypt-secrets`. Stale env keys in the decrypted file are inert (the binary ignores them) but should be cleaned to avoid confusion. |
| Dropping `PLAYWRIGHT_INSTALL_ONLY` from the manifest breaks the install path | Plan keeps it as a CLI bootstrap var read once inside `pkg/config.LoadCLIBootstrap()` before normal `config.Load()`. The grep gate excludes the entire `pkg/config/` directory, so this specific read is allowed. Verified at `runner/runner.go:207`. |

---

## Sequencing summary

| PR | Title | Lines changed | Reversible | Blast radius |
|---|---|---|---|---|
| Chunk 1 | `feat(config): add pkg/config package` | ~400 (additive) | trivial revert | none — no callers |
| Chunk 2 | `refactor(runner): use *config.Config` | ~250 | revert PR | runner only |
| Chunk 3 | `refactor: inject *slog.Logger` | ~150 (across 11 files) | revert PR | logging only |
| Chunk 4 | `fix(integration) + cleanup` | ~80 | revert PR | OAuth + dead-code |
| Chunk 5 | `ci: enforce env boundary` | ~20 | revert PR | CI only |

Total wall-clock: ~1 day for one engineer. Each chunk is independently shippable + revertable.

---

## Why this is "production-ready" by Uncle-Bob criteria

- **SRP**: `pkg/config` has exactly one reason to change (the schema of env vars).
- **DIP**: All consumers depend on `*config.Config` (or sub-views), not on the OS environment.
- **Boundary architecture**: env is an external system; reading it lives at the system boundary, not in domain code.
- **Fail fast**: required values are validated at process start, before any handler can run.
- **Make illegal states unrepresentable**: `cfg.AppEnv` is `appenv.Environment`, not a string; `cfg.AllowedOrigins` is `[]string`, not a comma-string requiring re-parsing per request.
- **DRY**: the three `envInt`s and two `envDuration`s collapse to zero — `caarlos0/env` does the conversion once, generically.
- **YAGNI**: no Viper, no koanf, no multi-source. We have one source — env — and we use the smallest tool that solves it.
- **Testability**: tests construct `*Config` directly; no `t.Setenv` plumbing for unit tests. `Load()` is the only function under test that touches the OS.

---

## Open questions for the reviewer

1. **Should `*config.Config` be a singleton accessed via `config.Get()`?** No — explicit injection is preferred. Singletons hide dependencies and break test parallelism.
2. **Should we migrate the `web/scrape.go` parallel `Config` struct in this plan?** No — out of scope. Separate concern (scraper-only). Track as follow-up.
3. **Should we migrate the `config.Service` (DB-backed dynamic config) into `pkg/config`?** No — different concern (runtime-tunable, DB-stored). Consider renaming it to `dynconfig` to avoid future confusion. Track as follow-up.
4. **Should `STRIPE_SUCCESS_URL`/`STRIPE_CANCEL_URL` move into `*config.Config`?** No — they go through `config.Service.GetString()` with DB fallback. They are runtime-tunable per-deploy; that's the right home. The env-override path inside `config.Service` covers the bootstrap case.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-27-env-config-consolidation.md`. Ready to execute via superpowers:subagent-driven-development (one fresh subagent per Chunk, two-stage review).

