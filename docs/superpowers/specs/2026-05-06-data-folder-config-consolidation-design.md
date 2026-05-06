# DataFolder Config Consolidation — Design

**Date:** 2026-05-06
**Status:** Proposed (awaiting user approval before implementation plan)
**Severity:** Medium — silent misconfiguration. `DATA_FOLDER=/gmapsdata` set in `docker-compose.production.yaml` has no effect; production CSVs land at `/webdata` (relative-to-CWD), never reach the `gmapsdata:/gmapsdata` named volume, and are lost on container restart.
**Scope:** Single field. Not a refresh of the broader env-config plan.

---

## Background — relationship to the prior plan

The 2026-04-27 env-config-consolidation plan (`docs/superpowers/plans/2026-04-27-env-config-consolidation.md`) introduced `pkg/config` as the single typed env-config boundary. Most of that plan has already landed:

| Plan chunk | Status | Evidence |
|---|---|---|
| Chunk 1 (`pkg/config` skeleton + tests) | Done | `pkg/config/config.go`, `pkg/config/bootstrap.go`, `pkg/config/config_test.go` |
| Chunk 2.1 (plumb `*config.Config` through runners) | Done | commit `ec03edc refactor(runner): plumb *config.Config from main through webrunner` |
| Chunk 3 (logger from typed config) | Done | commit `6866452 refactor(main): build root *slog.Logger from appCfg.LogLevel + appCfg.Log.*` |
| Chunk 4.2 (consolidate `MY_AWS_*` aliases) | Done | commit `dcf0c26 refactor: consolidate AWS_* aliases; drop MY_AWS_* fallbacks` |
| Chunk 5.1 (CI grep gate) | Done | `.github/workflows/build.yml:52-86` |

**The prior plan deliberately deferred DataFolder.** Line 22 of the plan reads:

> `runner.Config` (existing struct) is **flag-driven** for the CLI runner. Coexists with our new `pkg/config.Config` (env-driven for the web server). Don't merge them.

That blanket "don't merge them" was correct for most fields — `DSN` is required-fail-fast in `pkg/config`, the CLI flag is just an override path; `Concurrency` has different semantics in CLI scraper mode vs web mode; etc. But it produces a real bug for `DataFolder` specifically, because:

1. `pkg/config.Config.DataFolder` (`pkg/config/config.go:25`, `env:"DATA_FOLDER" envDefault:"./webdata"`) is read into the typed config object but never consulted by the file-writing code.
2. `runner.Config.DataFolder` (`runner/runner.go:214`, `runner/runner.go:256` flag default `"webdata"`) is what the web runner actually reads at `runner/webrunner/webrunner.go:207` (`os.MkdirAll(cfg.DataFolder, …)`) and `runner/webrunner/webrunner.go:809` (`filepath.Join(w.cfg.DataFolder, job.ID+".csv")`).
3. There is no merge function copying `appCfg.DataFolder` into `cfg.DataFolder`. `runner.MergeAWSDefaults(cfg, appCfg)` at `runner/runner.go:394` exists for AWS fields only — it explicitly does not touch `DataFolder`.
4. A third reader at `web/scrape.go:36` calls `getEnv("DATA_FOLDER", "./webdata")` for the parallel scraper-only `Config` struct, also independent of the typed config.

Result: setting `DATA_FOLDER` in the runtime environment has zero effect on where the web server writes job CSVs.

This spec finishes that one deferral. It does not revisit the prior plan's "don't merge them" rule for any other field.

---

## Verified facts

### What writes to DataFolder

| Site | Code | Purpose |
|---|---|---|
| `runner/webrunner/webrunner.go:207` | `os.MkdirAll(cfg.DataFolder, os.ModePerm)` | Creates the staging dir at startup |
| `runner/webrunner/webrunner.go:809` | `outpath := filepath.Join(w.cfg.DataFolder, job.ID+".csv")` | Per-job CSV path |
| `runner/webrunner/webrunner.go:811` | `os.Create(outpath)` | Open writer |
| `runner/webrunner/webrunner.go:1468` | `s3Uploader.Upload(...)` reads the same file | S3 upload after job completes |
| `runner/webrunner/webrunner.go:1510` | `os.Remove(csvFilePath)` | Local file deleted after S3 success |

### What reads from DataFolder

| Site | Code | Purpose |
|---|---|---|
| `web/service.go:100` | `filepath.Join(s.dataFolder, id+".csv")` | CSV-by-ID lookup (current API path) |
| `web/service.go:189` | same | Legacy `GetCSV` method |
| `web/service.go:235` | same | Third lookup site (legacy `GetCSV` deprecated path; do not remove in this spec — call sites may still exist) |

The `web.Service` receives `dataFolder` via constructor (`web.NewService(repo, cfg.DataFolder)` at `runner/webrunner/webrunner.go:268`), so it inherits whichever value the webrunner uses — not an independent reader.

### What DataFolder *is* — first principles

A **per-job CSV staging buffer for S3 uploads, used only by `RunModeWeb`.** Streamed scrapemate rows are buffered to local disk (cheap append), then a single multipart upload ships the file to S3 and the local copy is deleted. Postgres holds the canonical metadata (`JobFile` table). Other run modes do not use this path:

- `RunModeFile` → writes to `cfg.ResultsFile` (different field, different default `stdout`)
- `RunModeAwsLambda` → hardcodes `/tmp/output.csv` (`runner/lambdaaws/lambdaaws.go:65`), bypasses DataFolder entirely
- `RunModeDatabase` → no disk writes, results go straight to Postgres
- `RunModeInstallPlaywright` → does not run any of this code

### Why the prior plan's "don't merge them" was correct in general but wrong for this field

| Field | Both configs have it? | Both have a default? | Merge needed? |
|---|---|---|---|
| `DSN` | yes | only `pkg/config` (required); the CLI flag at `runner/runner.go:247` defaults to `""` and `runner.ParseConfig` reads `os.Getenv("DSN")` as a fallback when the flag is empty | no — `pkg/config` fails fast if `DSN` is unset; the runner-side fallback exists only to keep the legacy CLI path working and converges on the same value |
| `Concurrency` | yes | yes | no — semantics differ across run modes |
| `LogLevel` | only `pkg/config` after Chunk 3 | yes | n/a — already collapsed |
| `AccessKey` etc. | yes | only `pkg/config` | yes — handled by `MergeAWSDefaults` |
| **`DataFolder`** | **yes** | **yes (`"webdata"` vs `"./webdata"`)** | **yes — both assert authority over the same path; neither yields** |

DataFolder is the only field where both configs (a) have a default, (b) point at a real on-disk path, and (c) are read at runtime by code that picks one and ignores the other. That's the structural marker for "this needs one source of truth."

---

## Design

### One sentence

`pkg/config.Config.DataFolder` becomes the single source of truth; the `-data-folder` CLI flag stays but acts as a runtime override applied to `appCfg.DataFolder` before runner construction; the legacy `web/scrape.go:36` reader is deleted.

### Precedence (explicit, single arbitration point)

```
explicit -data-folder flag  >  DATA_FOLDER env var  >  envDefault "./webdata"
```

Resolved in `main.go` after `pkgconfig.Load()` and before `runnerFactory()`. Implemented manually (no Viper) since the project does not use Viper — but the resulting precedence matches the standard Cobra+Viper convention so the rule is familiar.

### Field changes

| Field | Before | After |
|---|---|---|
| `pkg/config.Config.DataFolder` | exists, `env:"DATA_FOLDER" envDefault:"./webdata"`, never read by webrunner | exists, same tag, **read by webrunner** |
| `runner.Config.DataFolder` | exists as primary | **deleted** — webrunner reads from `appCfg.DataFolder` |
| `-data-folder` CLI flag | binds to `runner.Config.DataFolder` with default `"webdata"` | binds to a local `string` declared at flag-parse time, default `""` (empty = "flag not set"); not stored on `runner.Config` |
| `web/scrape.go:36` `getEnv("DATA_FOLDER", "./webdata")` | active reader for parallel scraper Config | **deleted**; parallel scraper Config reads from injected `appCfg.DataFolder` |

### Composition root (sketch, for shape only — actual code in implementation plan)

In `main.go`, the order becomes:

1. `appCfg, err := pkgconfig.Load()` — single read of `DATA_FOLDER` env var
2. `cfg, dataFolderFlag, err := runner.ParseConfig()` — CLI flags parsed; `-data-folder` lands in a returned local string with default `""`
3. `runner.MergeAWSDefaults(cfg, appCfg)` — unchanged
4. **New:** `if dataFolderFlag != "" { appCfg.DataFolder = dataFolderFlag }`
5. `runnerFactory(cfg, appCfg, logger)` — webrunner reads `appCfg.DataFolder`

The flag value lives only as a local variable in `main.go`'s startup sequence — it is never stored on `runner.Config`. This intentionally avoids resurrecting the dual-storage problem the spec is removing: there is exactly one resolved value (`appCfg.DataFolder`), and the override is a transient applied at composition time.

Webrunner constructor at `runner/webrunner/webrunner.go:199` (`func New(cfg *runner.Config, appCfg *pkgconfig.Config, logger *slog.Logger) (runner.Runner, error)`) already accepts `*config.Config` — the prior plan's Chunk 2.1 plumbed this through. So the change in webrunner is just: `cfg.DataFolder` → `appCfg.DataFolder` at the four call sites listed above.

### Why delete `runner.Config.DataFolder` rather than keep it as the "resolved" value

`runner.Config` and `pkg/config.Config` are separate types with separate ownership per the prior plan. Once `appCfg.DataFolder` becomes canonical, leaving the field on `runner.Config` (even renamed) would re-create the duplication this spec is removing. Deleting it makes any stale `cfg.DataFolder` reference fail at compile time — a useful tripwire for catching residual references that survived the migration.

### What this fixes downstream

- `DATA_FOLDER` set in `docker-compose.production.yaml` controls where CSVs land (today: ignored)
- The `gmapsdata:/gmapsdata` named volume becomes the actual job-CSV staging path (today: empty, decorative)
- The Dockerfile's `mkdir -p /gmapsdata && chown brezel:brezel /gmapsdata` step from the security PR becomes the relevant chown (today: applied to a directory nothing writes to)
- One reader, one default, one merge point — matches Cobra+Viper precedence semantics
- Sets the established pattern for any future field that develops the same disease

---

## Out of scope (explicit non-goals)

These are real issues but each is its own change. Bundling them dilutes blast radius.

- **`Concurrency`, `Debug`, and other duplicated fields.** They have different semantics across run modes; the "don't merge them" rule from the 2026-04-27 plan still holds for those.
- **Storage abstraction (`JobArtifactStore` interface).** Considered in the brainstorming session as option C. Rejected as premature: only one real implementation exists, and the trigger that would justify a second (read-only rootfs hardening) is not yet on the roadmap. Re-evaluate when `read_only: true` is enabled on the backend service.
- **Lambda mode's hardcoded `/tmp/output.csv`** at `runner/lambdaaws/lambdaaws.go:65`. Different code path, different runtime, not affected by this fix. Could be tidied later.
- **Other `getEnv(...)` readers in `web/scrape.go`** (e.g. `SERVER_PORT`, `CLERK_SECRET_KEY`, parallel scraper-only fields). Only the `DATA_FOLDER` reader at line 36 is removed in this spec. Remaining readers stay as future cleanup, consistent with the 2026-04-27 plan's "out of scope" note for `web/scrape.go`.
- **The Dockerfile chown question.** After this lands, set `DATA_FOLDER=/gmapsdata` in `/etc/brezel/secrets/backend.env` and the existing chowned directory just works. No Dockerfile change needed.
- **CWD-relative `./webdata` default.** The default stays `./webdata` to preserve local-dev semantics. Production explicitly sets `DATA_FOLDER`.

---

## Test plan

Existing tests that must continue passing:

- `pkg/config/config_test.go:48` — `assert.Equal(t, "./webdata", cfg.DataFolder)` (default value test)
- `web/service_test.go:72,119` — `filepath.Join(dataFolder, jobID+".csv")` paths (use `t.TempDir()`)

New tests required:

1. **Precedence test** (new file in `runner/`, e.g. `runner/datafolder_resolution_test.go` — package `runner` avoids the `package main` test-build complications):
   - flag set, env unset → flag wins
   - flag unset, env set → env wins
   - both unset → default `"./webdata"` (from `pkg/config` `envDefault`)
   - both set → flag wins
   - flag set to `""`, env set → env wins (empty flag must not override; this is what `dataFolderFlag != ""` guards)
   - env set to `""`, flag unset → `caarlos0/env` treats empty as "use default" → `"./webdata"`
2. **webrunner integration**: `webrunner_startup_test.go` assertion that `os.MkdirAll` is called with `appCfg.DataFolder`, not `cfg.DataFolder`. (May already implicitly test this via the constructor signature.)
3. **CI grep gate stays green**: `.github/workflows/build.yml:52-86` env-boundary check still passes. The check matches `os.Getenv` / `os.LookupEnv` calls; the override resolution this spec adds is a plain string assignment in `main.go`, not an env read, so the gate is not relevant. The only env-access change is the *deletion* of `web/scrape.go:36`'s `getEnv("DATA_FOLDER", …)` call, which strictly reduces matches.

Manual verification:

- `docker compose up -d backend` with `DATA_FOLDER=/gmapsdata` set in `/etc/brezel/secrets/backend.env`
- Run a real scrape job
- Verify CSV appears at `/gmapsdata/{job_id}.csv` inside the container, not `/webdata/{job_id}.csv`
- Verify CSV is uploaded to S3 and then deleted from `/gmapsdata`
- Verify the Postgres `JobFile` row is recorded

---

## Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Compile errors in unknown call sites still using `runner.Config.DataFolder` | Medium | Low | `go build ./...` will catch all of them; the field deletion is the tripwire |
| Local dev workflows that rely on `-data-folder webdata` default | Low | Low | Default unchanged (`./webdata` via `envDefault`); explicit flag still works |
| Test using `t.Setenv("DATA_FOLDER", ...)` in webrunner tests breaks because the read site moved | Low | Low | Update affected tests to construct `*config.Config` directly (matches Chunk 2 pattern already in use) |
| Production `DATA_FOLDER=/gmapsdata` starts being honored on next deploy; CSV staging path moves from `/webdata` (bug) to `/gmapsdata` (intended) | High (the brezel production deploy already sets `DATA_FOLDER`, so this fires on first deploy after merge) | Medium (any in-flight job CSV not yet uploaded to S3 at deploy time would be left behind in the old `/webdata` path) | (a) Document the path change explicitly in the PR description and the deploy runbook; (b) deploy-time check before swap: `docker compose exec backend sh -c "ls /webdata/*.csv 2>/dev/null"` — if non-empty, drain in-flight jobs before cutover; (c) the local CSV is only the S3-upload buffer — once uploaded, the canonical artifact is in S3 and Postgres metadata, so the worst case is "rare in-flight job needs to be re-run," not data loss |
| Field rename leaves a fossil reference in `web/scrape.go`'s parallel `Config` struct | Medium | Low | Spec explicitly deletes `web/scrape.go:36` reader; review checklist must verify |

---

## Open questions for the reviewer

1. **Should `pkg/config.Config.DataFolder` get a Validate-time invariant** (e.g., reject empty string in production)? Lean: no, keep `envDefault` as the only safety; the prior plan kept production validation focused on secrets, and an empty DataFolder is recoverable (mkdir-of-empty is the CWD, which fails fast on read-only rootfs anyway).
2. **`web/scrape.go` parallel `Config` struct still has `getEnv(...)` for other fields.** This spec deletes only the `DATA_FOLDER` reader. The rest stays as future cleanup, consistent with the 2026-04-27 plan's "out of scope" note for `web/scrape.go`. Confirm.

---

## Sequencing

Single PR, three logical commits:

1. **`refactor(config): make pkg/config.Config.DataFolder the canonical source`** — webrunner switches reads from `cfg.DataFolder` to `appCfg.DataFolder`; delete `runner.Config.DataFolder`; `-data-folder` flag binds to a local string in `runner.ParseConfig` (returned alongside `*runner.Config`, not stored on it); `main.go` applies the override to `appCfg.DataFolder` post-Load.
2. **`refactor(web/scrape): drop legacy DATA_FOLDER getEnv reader`** — `web/scrape.go:36` reads from injected config instead.
3. **`test(config): cover DataFolder precedence resolution`** — new precedence test plus updated webrunner startup test.

Estimated diff: ~150 lines including tests. No new dependencies.

---

## Execution handoff

Once this spec is approved by the user, transition to `superpowers:writing-plans` to produce the step-by-step implementation plan. Do not begin implementation until the plan is also reviewed and approved.
