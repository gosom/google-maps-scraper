# DataFolder Config Consolidation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pkg/config.Config.DataFolder` the single source of truth for the runtime CSV-staging directory; delete `runner.Config.DataFolder`; route the `-data-folder` CLI flag through a typed `runner.FlagOverrides` struct and a functional option `pkgconfig.WithDataFolderOverride`; remove the legacy `web/scrape.go:36` reader. Spec: `docs/superpowers/specs/2026-05-06-data-folder-config-consolidation-design.md`.

**Architecture:** Three sequential commits. Commit 1 adds the functional option additively (no callers). Commit 2 is an atomic migration (introduce FlagOverrides, change ParseConfig signature, delete runner.Config.DataFolder, flip webrunner reads, reorder main.go startup). Commit 3 cleans the legacy `web/scrape.go` reader. Each commit independently compiles AND runs.

**Tech Stack:**
- Go 1.25.x (build env), `go 1.25.4` directive in go.mod
- `github.com/caarlos0/env/v11` (already a project dep) — env-config parser
- `flag` (stdlib) — CLI flag binding
- `github.com/stretchr/testify` — assertions in test cases

**Branch:** `refactor/data-folder-config-consolidation` (already created off `develop`).

**Hard rule:** Do not weaken the env-boundary CI check at `.github/workflows/build.yml:52-86`. New code must not introduce `os.Getenv` outside the allow-listed files.

---

## Pre-flight (run once before Chunk 1)

- [ ] Confirm clean working tree on the right branch:
  ```bash
  git branch --show-current   # → refactor/data-folder-config-consolidation
  git status -s               # tracked files clean (untracked CLAUDE.md, tmp/, etc. ok)
  ```
- [ ] Confirm baseline tests pass on develop's tip-of-DataFolder code paths (so we know what we should hold steady):
  ```bash
  go test ./pkg/config/... ./runner/... ./web/...
  ```
  Expected: all green except the pre-existing `gmaps/entry_internal_test.go:38` panic (unrelated to this plan; verified pre-existing on `develop` during the security PR work).
- [ ] Read the spec end-to-end before starting:
  - `docs/superpowers/specs/2026-05-06-data-folder-config-consolidation-design.md`
  - Especially the **Composition root** section (load-bearing for Chunk 2) and the **Test plan** section (drives Chunk 1's TDD).

---

## Chunk 1: Add `WithDataFolderOverride` functional option (additive, no callers)

After this chunk, `pkg/config.Load` accepts variadic `LoadOption`s and a new option `WithDataFolderOverride(s string) LoadOption` exists. **No production caller uses it yet.** This is the foundation commit; it must compile and pass all existing tests on its own.

### Task 1.1: Add `LoadOption` type and `WithDataFolderOverride` option (TDD red)

**Files:**
- Modify: `pkg/config/config.go` (add type + option func + extend `Load` signature; the load-bearing logic is the post-parse application of options)
- Test: `pkg/config/config_test.go` (extend with the table-driven precedence test from spec §test plan item 1)

- [ ] **Step 1 (TDD red): Add the failing test cases first**

  In `pkg/config/config_test.go`, add a new test function `TestLoad_WithDataFolderOverride` with table-driven cases (named subtests):

  | Case name | Override arg | `DATA_FOLDER` env | Expected `cfg.DataFolder` |
  |---|---|---|---|
  | `flag set, env unset` | `"/custom"` | unset (`os.Unsetenv`) | `"/custom"` |
  | `flag unset, env set` | `""` | `"/from-env"` | `"/from-env"` |
  | `both unset` | (no option) | unset | `"./webdata"` (envDefault) |
  | `both set` | `"/custom"` | `"/from-env"` | `"/custom"` (flag wins) |
  | `flag empty, env set to default value` | `""` | `"./webdata"` | `"./webdata"` |
  | `flag empty, env set to empty string` | `""` | `""` | `""` (caarlos0/env: set-but-empty wins; documented behavior, locked by this test) |

  Each case MUST also `t.Setenv` for every other required env var (`DSN`, `CLERK_SECRET_KEY`, etc.) so `Load` does not fail validation. Reuse the helper pattern from existing tests in the file. **Do NOT add `t.Parallel()` — env-var tests serialize on process-global state.** Add a `// Note: tests serialize on env var; do not add t.Parallel()` comment above the function.

- [ ] **Step 2: Run the test to verify it fails (compilation error expected)**

  ```bash
  go test ./pkg/config/ -run TestLoad_WithDataFolderOverride -v
  ```
  Expected: build failure (`undefined: WithDataFolderOverride` and/or `Load(opt) — Load takes 0 arguments`).

- [ ] **Step 3: Implement `LoadOption` and `WithDataFolderOverride`**

  In `pkg/config/config.go`, add (location: after the `Validate` method, before the existing `Load` function):

  ```go
  // LoadOption mutates a Config during construction. Options are applied AFTER
  // env parsing so they can override env-derived values. Use sparingly — the
  // canonical source of truth for runtime config is environment variables; an
  // option exists only when a CLI flag must take precedence over an env var.
  type LoadOption func(*Config)

  // WithDataFolderOverride replaces cfg.DataFolder with s if s != "". An empty
  // s is a no-op so that `Load(WithDataFolderOverride(unsetFlagValue))` is safe
  // when the caller cannot easily distinguish "flag unset" from "flag explicitly
  // empty" (Go's flag package collapses these without flag.Visit).
  func WithDataFolderOverride(s string) LoadOption {
      return func(c *Config) {
          if s != "" {
              c.DataFolder = s
          }
      }
  }
  ```

  Then change `Load`'s signature from `func Load() (*Config, error)` to `func Load(opts ...LoadOption) (*Config, error)` and apply options at the bottom of the function, after the existing post-parse trimming pass:

  ```go
  // ... existing parse + trim logic ...

  for _, opt := range opts {
      opt(&cfg)
  }

  return &cfg, nil
  ```

  Note: take `&cfg` if `cfg` is currently a value; if `Load` already returns a pointer that's been allocated, pass that pointer. Match the existing local-variable convention in the file.

- [ ] **Step 4: Run the test to verify it passes**

  ```bash
  go test ./pkg/config/ -run TestLoad_WithDataFolderOverride -v
  ```
  Expected: PASS for all six subtests.

- [ ] **Step 5: Run the full pkg/config test suite to ensure no regression**

  ```bash
  go test ./pkg/config/...
  ```
  Expected: all green.

- [ ] **Step 6: Run `go build ./...` to confirm no caller is broken by the variadic signature change**

  ```bash
  go build ./...
  ```
  Expected: clean (variadic is backwards-compatible).

- [ ] **Step 7: Commit**

  ```bash
  git add pkg/config/config.go pkg/config/config_test.go
  git commit -m "feat(config): add WithDataFolderOverride functional option to Load

  Adds variadic LoadOption parameter to pkgconfig.Load so callers can
  pass typed overrides at construction time without mutating the
  returned *Config post-Load. Introduces WithDataFolderOverride(s) which
  is a no-op when s is empty.

  No production caller uses this option yet — it is wired in the next
  commit. Existing Load() callers compile unchanged because the new
  parameter is variadic.

  Includes a six-case table-driven test that locks precedence semantics
  including the caarlos0/env quirk that envDefault does not fire when
  the env var is set-but-empty."
  ```

- [ ] **Step 8: Capture the commit SHA in this plan's Implementation Notes section** (Task 1.1 row).

### Task 1.2: Code review for Chunk 1

- [ ] **Step 1: Dispatch a fresh code-review subagent.** Brief includes:
  - the diff (`git show HEAD`)
  - the spec section §"Composition root" + §"test plan"
  - explicit asks: (a) `Load` signature is variadic and non-breaking; (b) the option is post-parse, not pre-parse; (c) test does NOT use `t.Parallel()`; (d) all six cases enumerated in the spec are present and named correctly.
- [ ] **Step 2: Apply any findings.** Re-run tests after each fix.
- [ ] **Step 3: Re-dispatch reviewer until verdict is "Approved" with no critical/major issues.** Cap at 5 iterations.
- [ ] **Step 4: Record review findings + fixes in Implementation Notes section.**

---

## Chunk 2: Atomic migration

Make `pkg/config.Config.DataFolder` canonical. Move the flag binding to `runner.FlagOverrides`. Delete `runner.Config.DataFolder`. Reorder `main.go` startup. Flip the four webrunner read sites. **All in one commit** so `git bisect` cannot land on a runtime-broken state.

### Task 2.1: Introduce `runner.FlagOverrides` + change `ParseConfig` signature (TDD red, then atomic implementation)

**Files:**
- Modify: `runner/runner.go`
  - Add `FlagOverrides` struct (top-level, near `Config`)
  - Remove `DataFolder string` field from `Config` struct (currently line 214)
  - Change `flag.StringVar(&cfg.DataFolder, "data-folder", "webdata", …)` (line 256) to bind to a local on `FlagOverrides`
  - Change `ParseConfig` return signature to `(*Config, FlagOverrides, error)`
- Modify: `main.go`
  - Reorder startup: parse flags first, then call `pkgconfig.Load(pkgconfig.WithDataFolderOverride(overrides.DataFolder))`
  - Build slog logger AFTER `Load` (since logger config still comes from `appCfg`)
- Modify: `runner/webrunner/webrunner.go`
  - Replace four read sites (`runner/webrunner/webrunner.go:200`, `:207`, `:268`, `:809`) with `appCfg.DataFolder`
- Test: `runner/webrunner/webrunner_startup_test.go` — update if it constructs `runner.Config` directly with `DataFolder` set; it should now construct via `FlagOverrides` or rely on `appCfg`.

- [ ] **Step 1 (TDD red): Add a webrunner-side compile-time test**

  No new functional test is required for this commit (per spec — the type system is the test). Instead, do a "build the world" check up front:

  ```bash
  go build ./...
  ```
  Expected on a clean develop tip: PASS. After deleting `runner.Config.DataFolder` later in this task, expect FAIL until all sites are flipped. This is the load-bearing tripwire.

- [ ] **Step 2: Add `FlagOverrides` struct in `runner/runner.go`** (near top, after `Config` struct definition):

  ```go
  // FlagOverrides holds CLI-flag values that override the canonical typed
  // env-config (pkg/config.Config). Each field's zero value means "flag not
  // set"; non-zero values are passed into pkgconfig.Load via functional
  // options so the override is applied during config construction rather
  // than by mutating *config.Config post-Load.
  //
  // Today this struct holds only DataFolder. Adding a second override is a
  // non-breaking field addition; the alternative — returning a (*Config,
  // string, error) tuple from ParseConfig — would require a signature break
  // every time a new override appears.
  type FlagOverrides struct {
      // DataFolder, if non-empty, overrides pkg/config.Config.DataFolder via
      // pkgconfig.WithDataFolderOverride. Bound to the -data-folder CLI flag.
      DataFolder string
  }
  ```

- [ ] **Step 3: Change `ParseConfig` signature**

  In `runner/runner.go`, change `func ParseConfig() (*Config, error)` to:

  ```go
  func ParseConfig() (*Config, FlagOverrides, error) {
      cfg := &Config{}
      var overrides FlagOverrides
      // ... existing flag setup ...
      flag.StringVar(&overrides.DataFolder, "data-folder", "", "data folder for web runner (overrides DATA_FOLDER env var)")
      // ... existing parsing logic ...
      return cfg, overrides, nil   // also update error returns
  }
  ```

  Remove the `flag.StringVar(&cfg.DataFolder, "data-folder", "webdata", …)` line. Remove the `DataFolder string` field from `Config` struct entirely.

  Note: the flag default changes from `"webdata"` to `""` because the typed config's `envDefault:"./webdata"` is now the single source for defaults. Documented behavior change per spec risk register.

- [ ] **Step 4: Update `main.go` startup sequence**

  Replace the existing block at `main.go:46-77` (Load → SetDefault → ParseConfig → MergeAWSDefaults) with the new order:

  ```go
  // Parse CLI flags first so we can pass overrides into pkgconfig.Load.
  cfg, overrides, err := runner.ParseConfig()
  if err != nil {
      slog.Error("invalid_configuration", slog.Any("error", err))
      os.Exit(1)
  }

  // Load typed env config, applying any CLI-flag overrides at construction.
  // appCfg is immutable from this point on.
  appCfg, err := pkgconfig.Load(pkgconfig.WithDataFolderOverride(overrides.DataFolder))
  if err != nil {
      slog.Error("config_load_failed", slog.Any("error", err))
      os.Exit(1)
  }

  // Build the single root logger from typed config.
  logger := pkglogger.New(appCfg.LogLevel, pkglogger.LogConfig{
      Output:        appCfg.Log.Output,
      FilePath:      appCfg.Log.FilePath,
      Dir:           appCfg.Log.Dir,
      FileName:      appCfg.Log.FileName,
      MaxSizeMB:     appCfg.Log.MaxSizeMB,
      RetentionDays: appCfg.Log.RetentionDays,
  })
  slog.SetDefault(logger)

  ctx, cancel := context.WithCancel(context.Background())

  runner.MergeAWSDefaults(cfg, appCfg)
  // ... rest unchanged ...
  ```

  Note the failure-path symmetry: if `ParseConfig` fails, `slog.Error` writes to the default stderr-text handler (logger not yet built); same as today's `Load`-fail path.

- [ ] **Step 5: Flip webrunner read sites**

  In `runner/webrunner/webrunner.go`, change four sites from `cfg.DataFolder` to `appCfg.DataFolder`:
  - Line 200: `if cfg.DataFolder == ""` → `if appCfg.DataFolder == ""`
  - Line 207: `os.MkdirAll(cfg.DataFolder, …)` → `os.MkdirAll(appCfg.DataFolder, …)`
  - Line 268: `web.NewService(repo, cfg.DataFolder)` → `web.NewService(repo, appCfg.DataFolder)`
  - Line 809: `filepath.Join(w.cfg.DataFolder, …)` — this requires storing `appCfg.DataFolder` on the webrunner struct or passing it through. Choose the minimal option: add `dataFolder string` to the webrunner struct, set it in `New()` from `appCfg.DataFolder`, and use `w.dataFolder` at the call site. Document why on the field comment.

- [ ] **Step 6: Run `go build ./...` until green**

  ```bash
  go build ./...
  ```
  Expected: any remaining stale `cfg.DataFolder` reference fails the build. Fix iteratively until clean. **The compile-time tripwire IS the migration's test.**

- [ ] **Step 7: Run full test suite**

  ```bash
  go test ./...
  ```
  Expected: all packages green except the pre-existing `gmaps/entry_internal_test.go:38` failure (unrelated). If a webrunner test fails because it constructed `runner.Config{DataFolder: …}` directly, update it to construct via `*pkgconfig.Config` (matches the prior plan's Chunk 2.1 pattern).

- [ ] **Step 8: Run `go vet ./...` and the env-boundary grep gate locally**

  ```bash
  go vet ./...
  ```
  And reproduce the CI gate locally:
  ```bash
  bash -c 'set -e
    direct=$(grep -rn "os\.Getenv\|os\.LookupEnv" --include="*.go" --exclude-dir=".claude" . \
      | grep -v "_test.go" | grep -v ":[[:space:]]*//" \
      | grep -v "pkg/config/" | grep -v "pkg/appenv/appenv\.go" \
      | grep -v "runner/runner\.go" | grep -v "web/handlers/version\.go" \
      | grep -v "config/config\.go" | grep -v "web/scrape\.go" || true)
    helpers=$(grep -rnE "\b(getEnv|getEnvOrDefault|envInt|envDuration|dbEnvInt|dbEnvDuration|parseCSVEnv|stripeWebhookSecretsFromEnv)\(" --include="*.go" --exclude-dir=".claude" . \
      | grep -v "_test.go" | grep -v "pkg/config/" \
      | grep -v "web/scrape\.go" | grep -v "web/handlers/version\.go" || true)
    if [ -n "$direct" ] || [ -n "$helpers" ]; then
      echo FAIL; echo "DIRECT:"; echo "$direct"; echo "HELPERS:"; echo "$helpers"; exit 1
    fi
    echo OK'
  ```
  Expected: `OK`.

- [ ] **Step 9: Commit (atomic)**

  ```bash
  git add -A   # all the modified files: runner/runner.go, runner/webrunner/webrunner.go, main.go, any test updates
  git commit -m "refactor: make pkg/config.DataFolder canonical; delete runner.Config.DataFolder

  Atomic migration:
  - Introduce runner.FlagOverrides struct holding CLI-flag overrides
    that should be applied to pkg/config.Config at construction time.
  - Change runner.ParseConfig signature from (*Config, error) to
    (*Config, FlagOverrides, error). main.go is the only caller.
  - Move -data-folder flag binding from runner.Config.DataFolder to
    runner.FlagOverrides.DataFolder; flag default changes from
    \"webdata\" to \"\" (the canonical default \"./webdata\" now comes
    from pkg/config envDefault).
  - Delete runner.Config.DataFolder.
  - Reorder main.go startup so flags are parsed before pkgconfig.Load,
    and Load receives the override via WithDataFolderOverride. appCfg
    is immutable post-Load.
  - Flip the four webrunner read sites from cfg.DataFolder to
    appCfg.DataFolder (or w.dataFolder where the webrunner struct
    holds it).

  Each step would have been runtime-broken in isolation: deleting
  runner.Config.DataFolder before flipping webrunner reads is a
  compile error, and flipping webrunner reads while the flag still
  binds to runner.Config.DataFolder leaves the staging path empty at
  runtime. The compile-time tripwire (deletion of the field) ensures
  all four sites are migrated in one commit."
  ```

- [ ] **Step 10: Capture commit SHA in Implementation Notes.**

### Task 2.2: Code review for Chunk 2

- [ ] Same ping-pong as Task 1.2 but the brief is heavier:
  - diff (`git show HEAD`)
  - spec sections: §"Composition root", §"Why delete runner.Config.DataFolder", §"Startup-sequence reordering"
  - explicit asks: (a) `runner.Config.DataFolder` is gone (`grep -n "Config.DataFolder" runner/runner.go` → only `pkg/config` matches); (b) all four webrunner sites flipped (`grep -n "cfg.DataFolder\|w.cfg.DataFolder" runner/webrunner/webrunner.go` → empty); (c) main.go failure paths still work; (d) flag default change from `"webdata"` to `""` is intentional and matches risk register; (e) env-boundary CI gate stays green.
- [ ] Loop until "Approved." Cap 5 iterations. Record findings + fixes in Implementation Notes.

---

## Chunk 3: Cleanup — `web/scrape.go` legacy reader

### Task 3.1: Replace `getEnv("DATA_FOLDER")` with injected `appCfg.DataFolder`

**Files:**
- Modify: `web/scrape.go` — delete the `getEnv("DATA_FOLDER", "./webdata")` call at line 36; the surrounding `Config` constructor now takes `appCfg.DataFolder` (or the value derived from it) as a parameter.
- Modify: any caller that constructs `web/scrape.Config` and was relying on the env-read default — pass `appCfg.DataFolder` explicitly.

- [ ] **Step 1: Locate the constructor for `web/scrape.go`'s parallel `Config`**

  ```bash
  grep -rn "web/scrape\|scrape\.Config\|loadScrapeConfig\|NewScrapeConfig" --include="*.go" .
  ```
  Find every site that builds the struct. There may be only one.

- [ ] **Step 2: Change the constructor signature to take `dataFolder string`**

  Delete the `getEnv("DATA_FOLDER", "./webdata")` call. Replace with the parameter. Other `getEnv(...)` calls in the same constructor stay (out of scope per spec).

- [ ] **Step 3: Update each caller to pass `appCfg.DataFolder`**

  Trace from main.go → runners → wherever the scrape Config is built. Pass `appCfg.DataFolder` through. **DO NOT** re-read it from a different source.

- [ ] **Step 4: Build + test**

  ```bash
  go build ./...
  go test ./...
  ```
  Expected: green except pre-existing gmaps test.

- [ ] **Step 5: Re-run env-boundary grep gate**

  Same script as Chunk 2 Step 8. Expected: `OK`. (`web/scrape.go` is path-excluded from the gate, but the deletion strictly reduces matches there anyway.)

- [ ] **Step 6: Commit**

  ```bash
  git add web/scrape.go <any caller files>
  git commit -m "refactor(web/scrape): drop legacy DATA_FOLDER getEnv reader

  The scrape Config struct's DataFolder field is now populated from
  the injected pkg/config.Config (via appCfg.DataFolder) rather than
  re-reading DATA_FOLDER directly. The struct field at web/scrape.go:14
  stays; only the line-36 getEnv call is removed.

  Other getEnv(...) readers in this file (SERVER_PORT, CLERK_SECRET_KEY,
  etc.) are out of scope per the 2026-04-27 env-config plan; they will
  be addressed in a separate cleanup PR."
  ```

- [ ] **Step 7: Capture commit SHA in Implementation Notes.**

### Task 3.2: Code review for Chunk 3

- [ ] Same ping-pong. Brief: diff + spec §"Decisions" item 2. Verify: `web/scrape.go:36` getEnv is gone; struct field at line 14 stays; constructor takes `dataFolder string`; no new env reads anywhere in the file. Loop until Approved. Record.

---

## Chunk 4: Final verification + master review

### Task 4.1: Pre-merge verification gate

- [ ] **Step 1: Full test suite**
  ```bash
  go test ./...
  ```
- [ ] **Step 2: Race detector**
  ```bash
  go test -race ./pkg/config/... ./runner/... ./web/...
  ```
- [ ] **Step 3: `go vet` + format check**
  ```bash
  gofmt -s -l . | grep -v vendor   # expect empty
  go vet ./...
  go mod tidy && git diff --exit-code go.mod go.sum
  ```
- [ ] **Step 4: Env-boundary CI gate (local reproduction)**
  Same script as Chunk 2 Step 8.
- [ ] **Step 5: `govulncheck` (catches any Go-stdlib regression introduced)**
  ```bash
  go run golang.org/x/vuln/cmd/govulncheck ./...
  ```
- [ ] **Step 6: Three-commit history sanity**
  ```bash
  git log --oneline develop..HEAD   # expect exactly 3 commits, the spec rewrites, and any review-fix commits
  ```

### Task 4.2: Master review (Opus 4.7)

- [ ] **Step 1: Dispatch a master reviewer subagent on `claude-opus-4-7`** with:
  - The spec: `docs/superpowers/specs/2026-05-06-data-folder-config-consolidation-design.md`
  - The plan with all Implementation Notes filled in (this file)
  - The branch diff: `git log -p develop..HEAD`
  - Explicit ask: review the implementation against the spec, not against the plan. Verify every spec promise (single source of truth, immutability, FlagOverrides struct, post-parse option application, four webrunner sites flipped, web/scrape.go cleanup, env-boundary intact, test plan items present). Surface any deviation, missing test case, or scope creep.
- [ ] **Step 2: Apply any findings.** If the master review flags a real issue, fix it (potentially with a 4th commit). Re-run Task 4.1 verification.
- [ ] **Step 3: When master reviewer says Approved, push the branch and open a PR against `develop`.** PR description must:
  - Cite the spec path
  - List the three commits (or four if a review-fix commit was needed)
  - Include the deploy runbook excerpt from spec §risk register (the `docker compose stop backend` + drain procedure)
  - Document the default-string change (`"webdata"` → `"./webdata"`) for reviewers

---

## Implementation Notes (filled in as tasks complete)

> Update this section after every task. Captures commit SHAs, review findings, and fixes so a fresh agent can resume mid-plan.

### Task 1.1 — Add functional option

- **Status:** ✅ implementation complete; awaiting code review
- **Commit SHA:** `169eddc`
- **Files modified:** `pkg/config/config.go`, `pkg/config/config_test.go` (only)
- **Watched-it-fail output (TDD red):**
  ```
  pkg/config/config_test.go:438:21: undefined: config.LoadOption
  pkg/config/config_test.go:447:41: undefined: config.WithDataFolderOverride
  pkg/config/config_test.go:504:16: cannot use ... in call to non-variadic config.Load
  ```
- **Test cases:** all six subtests of `TestLoad_WithDataFolderOverride` pass on first run after implementation. Full `pkg/config` suite passes. `go build ./...` and `go vet ./pkg/config/...` clean.
- **Implementer concerns (validated by controller):**
  1. **Spec inversion on the sixth test case (caarlos0/env behavior):** spec claimed `t.Setenv("DATA_FOLDER", "")` produces `cfg.DataFolder == ""` (set-but-empty wins). Empirical observation against `caarlos0/env/v11 v11.4.0` is the opposite: `envDefault` DOES fire, producing `"./webdata"`. Implementer changed the test's expected value to match observed library behavior. **Resolution:** controller updated the spec's test-plan item 1 to match reality (commit follows). The test still serves its locking purpose — if a future env-lib upgrade flips the semantics, the test breaks loudly.
  2. Local var `opts` in existing `Load` body collided with new variadic `opts ...LoadOption`. Renamed local to `envOpts`. No behavior change.
  3. Option-application loop placed before `cfg.Validate()` rather than between Validate and `return`. Reasonable — options can affect validated values, though `DataFolder` is not currently validated. All tests pass.
- **Pre-existing lint diagnostics (not addressed):** `pkg/config/config.go:196,199` — `interface{}` could be `any` (modernize). These are in the FuncMap signature mandated by `env.ParserFunc`, predate this PR, and are out of scope per anti-pattern guardrails ("don't refactor unrelated code").

### Task 1.2 — Code review for Chunk 1

- **Status:** ☐ not started
- **Review iterations:** _(N rounds)_
- **Findings:** _(list)_
- **Fixes applied:** _(list, with new commit SHAs if any)_
- **Final verdict:** _(Approved / blocked)_

### Task 2.1 — Atomic migration

- **Status:** ☐ not started
- **Commit SHA:** _(to fill)_
- **Build-failure trail:** _(list of compile errors hit while flipping; this is informative — the compile-time tripwire IS the test)_
- **Test updates required:** _(any webrunner_startup_test.go updates and why)_

### Task 2.2 — Code review for Chunk 2

- **Status:** ☐ not started
- **Review iterations:** _(N)_
- **Findings:** _(list)_
- **Fixes applied:** _(list)_
- **Final verdict:** _(Approved / blocked)_

### Task 3.1 — web/scrape.go cleanup

- **Status:** ☐ not started
- **Commit SHA:** _(to fill)_
- **Constructor caller sites updated:** _(list file:line)_

### Task 3.2 — Code review for Chunk 3

- **Status:** ☐ not started
- **Review iterations:** _(N)_
- **Findings:** _(list)_
- **Fixes applied:** _(list)_
- **Final verdict:** _(Approved / blocked)_

### Task 4.1 — Pre-merge verification

- **Status:** ☐ not started
- **All checks green:** _(yes/no, with any deviation noted)_

### Task 4.2 — Master review (Opus 4.7)

- **Status:** ☐ not started
- **Findings:** _(list)_
- **Fixes applied:** _(list)_
- **Final verdict against spec:** _(Approved / blocked)_
- **PR URL:** _(to fill)_

---

## Anti-pattern guardrails (do not violate)

- Do **NOT** restore `runner.Config.DataFolder` as a "resolved value" field — the deletion is the compile-time tripwire that ensures the migration is complete.
- Do **NOT** mutate `appCfg.DataFolder` after `Load()` returns. The functional-option pattern exists specifically to keep `appCfg` immutable post-Load.
- Do **NOT** add a `t.Parallel()` to env-var-touching tests. They serialize on process-global state.
- Do **NOT** widen scope to other duplicated fields (`Concurrency`, `Debug`). Each is its own future PR.
- Do **NOT** introduce `os.Getenv` calls outside the env-boundary CI allow-list.
- Do **NOT** change the `ENTRYPOINT`, `CMD`, healthcheck, or any Dockerfile contents — this PR is Go-side only.
- Do **NOT** skip the master reviewer step. The spec → plan → implementation chain has been carefully maintained; the master review against the spec is the final gate.
