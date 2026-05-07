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

**The prior plan's blanket "don't merge" rule covers DataFolder by default; this spec carves out the structural exception.** Line 22 of the plan reads:

> `runner.Config` (existing struct) is **flag-driven** for the CLI runner. Coexists with our new `pkg/config.Config` (env-driven for the web server). Don't merge them.

That blanket "don't merge them" was correct for most fields — `DSN` is required-fail-fast in `pkg/config`, the CLI flag is just an override path; `Concurrency` has different semantics in CLI scraper mode vs web mode; etc. But it produces a real bug for `DataFolder` specifically, because:

1. `pkg/config.Config.DataFolder` (`pkg/config/config.go:25`, `env:"DATA_FOLDER" envDefault:"./webdata"`) is read into the typed config object but never consulted by the file-writing code.
2. `runner.Config.DataFolder` (`runner/runner.go:214`, `runner/runner.go:256` flag default `"webdata"`) is what the web runner actually reads at `runner/webrunner/webrunner.go:200` (the `cfg.DataFolder == ""` guard), `:207` (`os.MkdirAll(cfg.DataFolder, …)`), `:268` (`web.NewService(repo, cfg.DataFolder)` — passes the value to the web service), and `:809` (`filepath.Join(w.cfg.DataFolder, job.ID+".csv")`).
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
| `web/service.go:235` | same | Same legacy `GetCSV` path as :189, just the inner `filepath.Join` after a `filepath.Clean(s.dataFolder)` at :234. Counted because it textually references the data folder; do not remove in this spec — callers may still exist. |

The `web.Service` receives `dataFolder` via constructor (`web.NewService(repo, cfg.DataFolder)` at `runner/webrunner/webrunner.go:268`), so it inherits whichever value the webrunner uses — not an independent reader.

### What DataFolder *is* — first principles

A **per-job CSV staging buffer for S3 uploads, used only by `RunModeWeb`.** Streamed scrapemate rows are buffered to local disk (cheap append), then a single multipart upload ships the file to S3 and the local copy is deleted. Postgres holds the canonical metadata (`JobFile` table). Other run modes do not use this path:

- `RunModeFile` → writes to `cfg.ResultsFile` (different field, different default `stdout`)
- `RunModeAwsLambda` → hardcodes `/tmp/output.csv` (`runner/lambdaaws/lambdaaws.go:65`), bypasses DataFolder entirely
- `RunModeDatabase` → no disk writes, results go straight to Postgres
- `RunModeInstallPlaywright` → does not run any of this code

### Why this field warrants the exception to the prior plan's "don't merge them" note

The prior plan's "Don't merge them" line is a brief heads-up about `runner.Config` being flag-driven, not a reasoned blanket policy applied per-field. For most fields the heuristic is fine (different defaults, different lifecycles, no real conflict). DataFolder is the structural exception:

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

`pkg/config.Config.DataFolder` becomes the single source of truth; the `-data-folder` CLI flag stays but is parsed *before* `pkgconfig.Load()` and passed in as a functional option (`pkgconfig.Load(pkgconfig.WithDataFolderOverride(s))`); the legacy `web/scrape.go:36` reader is deleted; `appCfg` is immutable after `Load()` returns.

### Precedence (explicit, single arbitration point)

```
explicit -data-folder flag  >  DATA_FOLDER env var  >  envDefault "./webdata"
```

Resolved **inside `pkgconfig.Load`** via a functional option, so the override is applied during config construction rather than by mutating `appCfg` post-Load. Implemented manually (no Viper) since the project does not use Viper — but the resulting precedence matches the standard Cobra+Viper convention so the rule is familiar. Keeping `appCfg` immutable post-Load aligns with the project's existing dependency-injection pattern (the prior plan's Chunk 2.1 plumbed `*config.Config` through every runner; nothing else mutates it after construction).

### Field changes

| Field | Before | After |
|---|---|---|
| `pkg/config.Config.DataFolder` | exists, `env:"DATA_FOLDER" envDefault:"./webdata"`, never read by webrunner | exists, same tag, **read by webrunner**; value can be replaced at construction time by `WithDataFolderOverride` option |
| `runner.Config.DataFolder` | exists as primary | **deleted** — webrunner reads from `appCfg.DataFolder` |
| `-data-folder` CLI flag | binds to `runner.Config.DataFolder` with default `"webdata"` | binds to `runner.FlagOverrides.DataFolder` field with default `""` (empty = "flag not set"); the typed `FlagOverrides` struct is returned alongside `*runner.Config` from `runner.ParseConfig` |
| `pkg/config.Load` signature | `Load() (*Config, error)` | `Load(opts ...LoadOption) (*Config, error)` — variadic, non-breaking for existing callers |
| `pkg/config.WithDataFolderOverride` | does not exist | new functional option: `func WithDataFolderOverride(s string) LoadOption`; if `s != ""`, replaces the parsed `cfg.DataFolder` value |
| `web/scrape.go:36` `getEnv("DATA_FOLDER", "./webdata")` | active reader for parallel scraper Config | **deleted**; parallel scraper Config reads from injected `appCfg.DataFolder` |

### Composition root (sketch, for shape only — actual code in implementation plan)

In `main.go`, the order becomes:

1. `cfg, overrides, err := runner.ParseConfig()` — CLI flags parsed first; `-data-folder` lands in `overrides.DataFolder` (empty when flag is unset). Signature change from `(*Config, error)` to `(*Config, FlagOverrides, error)`. Verified caller surface: `main.go:68` is the only caller in the repo (`grep -rn 'runner.ParseConfig'`); no test files call it directly. One-line update.
2. `appCfg, err := pkgconfig.Load(pkgconfig.WithDataFolderOverride(overrides.DataFolder))` — env-config parsed; the option is applied during construction. If `overrides.DataFolder == ""`, the option is a no-op and `cfg.DataFolder` keeps its env-or-default value. Otherwise the option replaces it. **`appCfg` is immutable after this call returns.**
3. `runner.MergeAWSDefaults(cfg, appCfg)` — unchanged
4. `runnerFactory(cfg, appCfg, logger)` — webrunner reads `appCfg.DataFolder`

The flag value lives only on the `FlagOverrides` struct returned from `ParseConfig` — it is never stored on `runner.Config`. The override is consumed exactly once when constructing `appCfg`, then discarded. There is one resolved value (`appCfg.DataFolder`), one source of truth, no post-construction mutation.

**Why a `FlagOverrides` struct rather than a bare string return.** Today there is one override; tomorrow there may be two or three (`Concurrency`, similar latent gaps from the prior plan). A struct with one field today scales without breaking the `ParseConfig` signature; a `(*Config, string, error)` return would need to grow into `(*Config, string, int, error)` and so on. The struct also documents intent: callers see `overrides.DataFolder` instead of an unnamed string.

**Why a functional option on `Load` rather than mutating `appCfg`.** `pkgconfig.Load` is the typed-config construction boundary; functional options are the idiomatic Go pattern for "construction-time configurable values" (see `golang-design-patterns`: *"Constructors SHOULD use functional options — they scale better as APIs evolve"*). Mutating `appCfg.DataFolder` in main.go after `Load` returns would introduce a precedent — *"appCfg is mutable during startup"* — that would calcify into "appCfg is mutable, period," which would silently break dependency-injection guarantees the prior plan's Chunk 2.1 established.

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
- **CWD-relative default.** The unified default is `./webdata` (from `envDefault` in `pkg/config.Config`). The legacy CLI-flag default of `"webdata"` (no leading dot) is dropped — same directory on Unix, different string. See risk register. Production explicitly sets `DATA_FOLDER`.

---

## Test plan

Existing tests that must continue passing:

- `pkg/config/config_test.go:48` — `assert.Equal(t, "./webdata", cfg.DataFolder)` (default value test)
- `web/service_test.go:72,119` — `filepath.Join(dataFolder, jobID+".csv")` paths (use `t.TempDir()`)

New tests required:

1. **Functional-option unit test** in `pkg/config/config_test.go` exercising `WithDataFolderOverride` directly. Function under test: `pkgconfig.WithDataFolderOverride(s string) LoadOption`. Cases (table-driven, named subtests per `golang-testing` skill):
   - `flag set, env unset` — `Load(WithDataFolderOverride("/custom"))` with `os.Unsetenv("DATA_FOLDER")` → `cfg.DataFolder == "/custom"`
   - `flag unset, env set` — `Load(WithDataFolderOverride(""))` with `t.Setenv("DATA_FOLDER", "/from-env")` → `cfg.DataFolder == "/from-env"`
   - `both unset` — `Load()` with `os.Unsetenv("DATA_FOLDER")` → `cfg.DataFolder == "./webdata"` (envDefault fires when var is unset)
   - `both set` — `Load(WithDataFolderOverride("/custom"))` with `t.Setenv("DATA_FOLDER", "/from-env")` → `cfg.DataFolder == "/custom"` (flag wins)
   - `flag empty, env set to default value` — `Load(WithDataFolderOverride(""))` with `t.Setenv("DATA_FOLDER", "./webdata")` → `cfg.DataFolder == "./webdata"` (sanity case; locks behavior even if envDefault later changes)
   - `flag empty, env set to empty string` — `Load(WithDataFolderOverride(""))` with `t.Setenv("DATA_FOLDER", "")` → expected `cfg.DataFolder == "./webdata"`. **Note: this contradicts an earlier draft of this spec** which claimed caarlos0/env's behavior is "set-but-empty wins, envDefault does NOT fire." Verified empirically against `caarlos0/env/v11 v11.4.0` during Chunk 1 implementation: **envDefault DOES fire on a set-but-empty env var.** The test locks this observed behavior so any future env-lib upgrade that flips the semantics breaks loudly. No post-parse fallback needed: the library already does the right thing for both "unset" and "set-but-empty" cases.

   **Note on `-data-folder ""` from the CLI side.** Because Go's `flag.StringVar` cannot distinguish "user passed `-data-folder ""`" from "user did not pass the flag," and because `WithDataFolderOverride("")` is a no-op by design, there is no semantic for "explicitly clear via CLI flag." The collapse is intentional and matches the precedence rule (flag overrides env *only* when non-empty).

   **Test must NOT use `t.Parallel()`.** The test cases set/unset `DATA_FOLDER` via `t.Setenv`, which mutates process-global state. `t.Parallel()` would race with any other test that touches env vars. The `golang-testing` skill rule *"Independent tests SHOULD use t.Parallel() when possible"* explicitly says "when possible" — this is a case where it isn't. A `// Note: tests serialize on env var; do not add t.Parallel()` comment must accompany the test function.

   Why this test lives in `pkg/config_test`: the function under test (`WithDataFolderOverride`) is exported from `pkg/config`, and the option's effect is observable on the returned `*Config`. Per `golang-testing` *"Co-locate _test.go files with the code they test"*.

2. **No `runner.ParseConfig` test for `FlagOverrides.DataFolder` required for this PR.** The flag-binding code is a single `flag.StringVar` call; testing it would test `flag.StringVar` itself. If a future change adds non-trivial logic around flag parsing, that's when a test is justified.

3. **No new webrunner integration test required.** Deleting `runner.Config.DataFolder` makes any miswiring (`cfg.DataFolder` instead of `appCfg.DataFolder`) fail at compile time — the type system is the test. Existing `webrunner_startup_test.go` cases continue to pass once they construct `*pkgconfig.Config` directly (already the prior-plan pattern).
**CI grep gate stays green**: `.github/workflows/build.yml:52-86` env-boundary check still passes. The check matches `os.Getenv`/`os.LookupEnv` (lines 63-72) and helper functions including `getEnv` (lines 74-79); both grep blocks already path-exclude `web/scrape.go` (lines 71 and 77), so the deletion at `web/scrape.go:36` is silently green either way. The new `WithDataFolderOverride` option lives in `pkg/config` (allow-listed), and contains no env access — it is a pure value-replacement function. The gate is not triggered.

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
| Default-value string changes from `"webdata"` (old CLI flag default) to `"./webdata"` (envDefault) when neither flag nor env is set. Explicit `-data-folder` still works. | Low (rare path — production sets `DATA_FOLDER`; local dev usually inherits CWD) | Low (resolves to the same on-disk directory on Unix, `os.Stat`/`os.MkdirAll` semantics identical) | Documented behavior change. The only observable difference is in log lines or error messages that quote the path string verbatim — anyone string-comparing the path would need to update to match `"./webdata"`. No behavior depends on the bare-vs-leading-dot form. |
| Contributor with a stale `webdata/` directory at repo root (created by the old `"webdata"` flag default) sees no migration of leftover files when their local Go binary now writes to `./webdata/` after this change | Low | Negligible | Same on-disk directory on Unix; `.gitignore` already lists `webdata/` (path-style irrelevant to gitignore matching). Nothing to do. |
| Test using `t.Setenv("DATA_FOLDER", ...)` in webrunner tests breaks because the read site moved | Low | Low | Update affected tests to construct `*config.Config` directly (matches Chunk 2 pattern already in use) |
| Production `DATA_FOLDER=/gmapsdata` starts being honored on next deploy; CSV staging path moves from `/webdata` (bug) to `/gmapsdata` (intended) | High (the brezel production deploy already sets `DATA_FOLDER`, so this fires on first deploy after merge) | Medium | Per `runner/webrunner/webrunner.go:1485-1510`, the `JobFile` Postgres row is created **only after** S3 upload succeeds, and `os.Remove(csvFilePath)` then deletes the local file. Any job whose CSV write straddles the deploy cutover therefore loses its partial file on the old path with no Postgres metadata trail — the new container starts a fresh write at `/gmapsdata/{job_id}.csv` and never sees the orphaned `/webdata/{job_id}.csv`. **Mitigations, all required, not optional:** (a) deploy runbook MUST include a brief downtime window. There is no job-intake pause flag in the codebase today (verified by `grep -rn 'JOB_INTAKE_PAUSED\|intake.*pause' --include='*.go'` → no matches), so the runbook MUST either: (i) `docker compose stop backend` and wait for `SELECT count(*) FROM jobs WHERE status='running' AND updated_at > now() - interval '5 minutes'` to reach 0 AND for any `JobFile` rows still in upload state to settle (running-jobs=0 is necessary but not sufficient because the worker takes a few seconds between the last row write at `runner/webrunner/webrunner.go:809-811` and the S3 upload finishing at `:1468`); or (ii) deploy an intake-pause flag in a preceding commit and use that. The simpler (i) path is acceptable given a brief planned-maintenance window; (b) deploy-time pre-check: `docker compose exec backend sh -c "ls /webdata/*.csv 2>/dev/null"` — if non-empty, abort the deploy and drain first; (c) document the path change in the PR description and link the runbook |
| Field rename leaves a fossil reference in `web/scrape.go`'s parallel `Config` struct | Medium | Low | Spec explicitly deletes `web/scrape.go:36` reader; review checklist must verify |

---

## Decisions (resolved during spec writing — listed for transparency)

1. **No Validate-time invariant on empty `DataFolder`.** `envDefault:"./webdata"` is the only safety. The prior plan kept production validation focused on secrets, and an empty `DataFolder` is recoverable: `mkdir-of-empty` is the CWD, which fails fast on read-only rootfs and on the existing `os.MkdirAll` call anyway.
2. **`web/scrape.go`'s parallel `Config` struct keeps its `DataFolder` field** (line 14); only the `getEnv("DATA_FOLDER", …)` reader at line 36 is removed. The field is populated by the constructor (whichever site builds the parallel `Config`), which now takes `appCfg.DataFolder` as a parameter. Other readers in `web/scrape.go` (e.g. `SERVER_PORT`, `CLERK_SECRET_KEY`) stay — out of scope per the prior plan.
3. **Functional option on `Load` rather than post-Load mutation.** Initial draft of this spec mutated `appCfg.DataFolder` in `main.go` after `Load()` returned. Reviewed against `golang-design-patterns` (*"Constructors SHOULD use functional options — they scale better as APIs evolve"*) and `golang-dependency-injection` (immutable injected config) and revised: `pkg/config.Load` now accepts variadic `LoadOption`s, the override is applied during construction, and `appCfg` is immutable post-`Load`. The cost is one new exported function (`WithDataFolderOverride`); the benefit is preserving the dependency-injection guarantee the prior plan's Chunk 2.1 established.
4. **Typed `FlagOverrides` struct rather than a bare-string `ParseConfig` return.** Initial draft returned `(*Config, string, error)` from `ParseConfig`. Reviewed against `golang-cli` (functional options scale better as concerns grow) and revised: a `runner.FlagOverrides` struct holds the override field. With one field today the struct is overkill, but adding a second override (`Concurrency`, the leading next candidate) becomes a non-breaking field addition rather than a `(_, _, _, _, error)` signature growth. The struct also makes the call site self-documenting (`overrides.DataFolder` instead of an unnamed string).

## Open questions for the reviewer

*(none — all decisions above are resolved; flag any disagreement during review)*

---

## Sequencing

Single PR, three logical commits. **Each commit individually compiles AND runs in isolation** — no commit leaves the binary in a runtime-broken state where `git bisect` would land on a non-startable build:

1. **`feat(config): add WithDataFolderOverride functional option to Load`** — extends `pkg/config.Load` to accept variadic `LoadOption`s; introduces `pkgconfig.WithDataFolderOverride(s string) LoadOption` (no-op when `s == ""`, replaces `cfg.DataFolder` otherwise). Existing `Load()` callers are unaffected (variadic). Includes the table-driven test from §test plan item 1. **No production-code call sites change** — the option is dormant until commit 2 wires it.

2. **`refactor: make pkg/config.DataFolder canonical; delete runner.Config.DataFolder`** — single atomic migration:
   - introduce `runner.FlagOverrides{ DataFolder string }` struct
   - change `runner.ParseConfig` signature to `(*Config, FlagOverrides, error)`
   - move `-data-folder` flag binding from `runner.Config.DataFolder` to `FlagOverrides.DataFolder` (default `""`)
   - delete `runner.Config.DataFolder` field
   - reorder `main.go` startup: parse flags first, then `pkgconfig.Load(pkgconfig.WithDataFolderOverride(overrides.DataFolder))` (see "Startup-sequence reordering" subsection below)
   - flip the four webrunner read sites from `cfg.DataFolder` to `appCfg.DataFolder`
   The compile-time tripwire ensures every stale reference is found at build time. Splitting this into separate commits would either (a) leave one commit with `runner.Config.DataFolder` deleted but webrunner still reading it (compile error), or (b) leave one commit with both fields existing and the old field stale-but-still-read at runtime (zero string defeats the `cfg.DataFolder == ""` guard at `runner/webrunner/webrunner.go:200`). Atomic commit avoids both.

3. **`refactor(web/scrape): drop legacy DATA_FOLDER getEnv reader`** — `web/scrape.go:36` reads from injected `appCfg.DataFolder` instead. The struct field at `web/scrape.go:14` stays; the constructor (whichever site builds the parallel `Config`) takes `appCfg.DataFolder` as a parameter. Independent of commit 2 — runs after to keep the migration's diff focused.

Estimated diff: ~180 lines including tests. No new dependencies.

### Startup-sequence reordering (load-bearing detail of commit 2)

The current `main.go` calls `pkgconfig.Load()` (line 46) **before** `runner.ParseConfig()` (line 68). The new design needs the flag override available **at** `Load` call time, so the order flips:

| Step | Before (`main.go` today) | After |
|---|---|---|
| 1 | `pkgconfig.Load()` (line 46) | `runner.ParseConfig()` |
| 2 | build slog logger from `appCfg.Log*` (line 55) | `pkgconfig.Load(pkgconfig.WithDataFolderOverride(overrides.DataFolder))` |
| 3 | `runner.ParseConfig()` (line 68) | build slog logger from `appCfg.Log*` |
| 4 | `runner.MergeAWSDefaults` (line 77) | `runner.MergeAWSDefaults` |
| 5 | `runnerFactory(...)` | `runnerFactory(...)` |

Consequence: if `runner.ParseConfig` fails (bad CLI flags), the custom slog handler is not yet constructed when the error is logged. This is *not* a regression: today, if `pkgconfig.Load` fails (missing required env vars), the same situation already obtains — the error at `main.go:48` uses `slog.Error` against the default handler (stderr, before `slog.SetDefault`). The new order makes both startup-failure paths use the same fallback, which is symmetric and acceptable.

---

## Execution handoff

Once this spec is approved by the user, transition to `superpowers:writing-plans` to produce the step-by-step implementation plan. Do not begin implementation until the plan is also reviewed and approved.
