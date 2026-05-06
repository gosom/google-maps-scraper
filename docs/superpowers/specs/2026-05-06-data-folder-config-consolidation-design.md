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
| `web/service.go:235` | same | Third lookup site |

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
| `DSN` | yes | only `pkg/config` (required) | no — `pkg/config` fails fast if unset; CLI flag is just an override |
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
| `-data-folder` CLI flag | binds to `runner.Config.DataFolder` with default `"webdata"` | binds to a local string with default `""` (empty = "not set"); if non-empty, overrides `appCfg.DataFolder` post-Load |
| `web/scrape.go:36` `getEnv("DATA_FOLDER", "./webdata")` | active reader for parallel scraper Config | **deleted**; parallel scraper Config also reads from `appCfg.DataFolder` |

### Composition root (sketch, for shape only — actual code in implementation plan)

In `main.go`, the order becomes:

1. `appCfg, err := pkgconfig.Load()` — single read of `DATA_FOLDER` env var
2. `cfg, err := runner.ParseConfig()` — CLI flags parsed; `-data-folder` if set lands in a new `cfg.DataFolderOverride string` (empty default)
3. `runner.MergeAWSDefaults(cfg, appCfg)` — unchanged
4. **New:** `if cfg.DataFolderOverride != "" { appCfg.DataFolder = cfg.DataFolderOverride }`
5. `runnerFactory(cfg, appCfg, logger)` — webrunner reads `appCfg.DataFolder`

Webrunner constructor at `runner/webrunner/webrunner.go:269` already accepts `*config.Config` (Chunk 2.1 already plumbed it through). So the change in webrunner is just: `cfg.DataFolder` → `appCfg.DataFolder` at the four call sites listed above.

### Why a `DataFolderOverride` field instead of mutating the existing `runner.Config.DataFolder`

The prior plan explicitly kept `runner.Config` and `pkg/config.Config` as separate types with separate ownership. This design preserves that separation: the CLI flag's job is to *capture an override intent*, not to hold the resolved value. The override is applied to the typed config (the canonical source) and `runner.Config` no longer carries this field at all. After this change, anyone reading `cfg.DataFolder` from `runner.Config` will get a compile error — a useful tripwire for catching residual stale references.

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
- **The Dockerfile chown question.** After this lands, set `DATA_FOLDER=/gmapsdata` in `/etc/brezel/secrets/backend.env` and the existing chowned directory just works. No Dockerfile change needed.
- **CWD-relative `./webdata` default.** The default stays `./webdata` to preserve local-dev semantics. Production explicitly sets `DATA_FOLDER`.

---

## Test plan

Existing tests that must continue passing:

- `pkg/config/config_test.go:48` — `assert.Equal(t, "./webdata", cfg.DataFolder)` (default value test)
- `web/service_test.go:72,119` — `filepath.Join(dataFolder, jobID+".csv")` paths (use `t.TempDir()`)

New tests required:

1. **Precedence test** (new file, e.g. `main_datafolder_resolution_test.go` or in `runner/`):
   - flag set, env unset → flag wins
   - flag unset, env set → env wins
   - both unset → default `"./webdata"`
   - both set → flag wins
2. **webrunner integration**: `webrunner_startup_test.go` assertion that `os.MkdirAll` is called with `appCfg.DataFolder`, not `cfg.DataFolder`. (May already implicitly test this via the constructor signature.)
3. **CI grep gate stays green**: `.github/workflows/build.yml:52-86` env-boundary check still passes — `web/scrape.go` no longer reads `DATA_FOLDER`, and the new override resolution lives in `main.go` which is allow-listed (or, if not yet allow-listed, route the override through a `pkg/config` helper similarly to `LoadCLIBootstrap`).

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
| CI env-boundary check fails on the new override resolution code in `main.go` | Medium | Low | Route the override through `pkg/config` (e.g., add `Config.ApplyDataFolderOverride(s string)` method) so the env access — there is none, the flag value is just a string — happens inside the allowed boundary |
| Anyone setting `DATA_FOLDER` in production has been getting silent misconfiguration; fixing it changes their on-disk path on next deploy | Low | Low | Production `compose.yaml` already sets `DATA_FOLDER=/gmapsdata` and a named volume mount waits at `/gmapsdata`; this PR merely makes that mount actually used. Document in PR description. |
| Field rename leaves a fossil reference in `web/scrape.go`'s parallel `Config` struct | Medium | Low | Spec explicitly deletes `web/scrape.go:36` reader; review checklist must verify |

---

## Open questions for the reviewer

1. **`DataFolderOverride` field name.** Acceptable, or prefer something like `DataFolderFlag` to make it explicit it comes from CLI? My pick: `DataFolderOverride` — it describes the *role*, not the *origin*.
2. **Should `pkg/config.Config.DataFolder` get a Validate-time invariant** (e.g., reject empty string in production)? Lean: no, keep `envDefault` as the only safety; the prior plan kept production validation focused on secrets, and an empty DataFolder is recoverable (mkdir-of-empty is the CWD, which fails fast on read-only rootfs anyway).
3. **`web/scrape.go` parallel `Config` struct still has `getEnv(...)` for other fields.** This spec deletes only the `DATA_FOLDER` reader. The rest stays as future cleanup, consistent with the 2026-04-27 plan's "out of scope" note for `web/scrape.go`. Confirm.

---

## Sequencing

Single PR, three logical commits:

1. **`refactor(config): make pkg/config.Config.DataFolder the canonical source`** — webrunner switches reads from `cfg.DataFolder` to `appCfg.DataFolder`; delete `runner.Config.DataFolder`; introduce `runner.Config.DataFolderOverride` bound to `-data-folder` flag with empty default; main.go applies override to `appCfg.DataFolder` post-Load.
2. **`refactor(web/scrape): drop legacy DATA_FOLDER getEnv reader`** — `web/scrape.go:36` reads from injected config instead.
3. **`test(config): cover DataFolder precedence resolution`** — new precedence test plus updated webrunner startup test.

Estimated diff: ~150 lines including tests. No new dependencies.

---

## Execution handoff

Once this spec is approved by the user, transition to `superpowers:writing-plans` to produce the step-by-step implementation plan. Do not begin implementation until the plan is also reviewed and approved.
