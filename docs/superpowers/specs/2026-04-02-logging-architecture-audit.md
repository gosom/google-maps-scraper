# Logging Architecture Audit

**Date**: 2026-04-02
**Triggered by**: Debugging session where critical logs went to `logs/brezel-api-YYYY-MM-DD.log` instead of the expected `/tmp/logfile.log`, causing hours of wasted time.

---

## Current State

### Three Independent Logging Systems

The codebase has **three separate logging systems** that write to **different destinations** with **different formats**:

#### 1. `slog.Default()` -- the Go structured logger (JSON)

**Set up in** `main.go:44-45`:
```go
logger := pkglogger.New(os.Getenv("LOG_LEVEL"))
slog.SetDefault(logger)
```

**Writer**: `pkg/logger/logger.go` builds an `io.Writer` controlled by `LOG_OUTPUT` env var (default: `"both"`):
- `"stdout"` -- writes to `os.Stdout` only
- `"file"` -- writes to `logs/brezel-api-YYYY-MM-DD.log` only
- `"both"` (default) -- writes to **both** `os.Stdout` AND `logs/brezel-api-YYYY-MM-DD.log`

**Format**: JSON via `slog.NewJSONHandler`

**Used by** (direct `slog.Info/Warn/Error/Debug` calls, no component tag):
| Package | Approx. call sites |
|---|---|
| `exiter/` | ~25 |
| `gmaps/` (entry.go, job.go, cookies.go, place.go, reviews.go) | ~25 |
| `gmaps/images/` | ~45 |
| `runner/runner.go` | ~15 |
| `runner/webrunner/webrunner.go` | ~23 (startup config, proxy init) |
| `runner/webrunner/writers/` | ~15 |
| `postgres/resultwriter.go` | ~20 |
| `postgres/fallback_resultwriter.go` | ~5 |
| `web/web.go` | ~3 |
| `web/auth/` | ~3 |
| `web/handlers/billing.go` | ~5 |
| `web/handlers/integration.go` | ~1 |
| `webshare/client.go` | ~2 |

**Key problem**: These logs have **no `component` field**, making it impossible to distinguish which subsystem emitted them. When redirecting stdout with `> /tmp/logfile.log 2>&1`, these logs go to BOTH the temp file (via stdout) AND `logs/brezel-api-YYYY-MM-DD.log` (via the file writer). This means log lines are **duplicated** across two files with no indication of which is "primary".

#### 2. Component-scoped `slog.Logger` instances (JSON, with `component` attribute)

**Created in** `main.go:106-123` via `runnerFactory`:
```go
logger.With(slog.String("component", "webrunner"))
logger.With(slog.String("component", "filerunner"))
// etc.
```

Also created independently in `web/web.go:75`:
```go
logger: pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "api"),
```

**Writer**: Same `outputWriter()` singleton as `slog.Default()` (both go through `pkg/logger/logger.go`).

**Used by** (via `w.logger.`, `s.logger.`, `l.logger.`, etc.):
| Package | Receiver | Component tag | Approx. call sites |
|---|---|---|---|
| `runner/webrunner/webrunner.go` | `w.logger` | `"webrunner"` | ~107 |
| `billing/service.go` | `s.logger` | `"billing"` | ~50 |
| `runner/lambdaaws/` | `l.logger`, `i.logger` | `"lambdaaws"`, `"invoker"` | ~6 |
| `runner/databaserunner/` | `d.logger` | `"databaserunner"` | ~1 |
| `web/web.go` (Server) | `s.logger` | `"api"` | ~10 |
| `proxy/pool.go`, `proxy/proxy.go` | `p.logger`, `ps.logger` | none (receives `slog.Default()`) | ~15 |
| `postgres/migration.go` | `m.logger` | passed in | ~10 |
| `postgres/stuck_jobs.go` | `log` param | passed in | ~10 |
| `web/middleware/` | `log` param | inherits from `"api"` | ~6 |

**Key insight**: These use the **same writer** as `slog.Default()`, so they go to the same destinations. The `component` field makes them filterable, which is good. However, code in the same package inconsistently uses `w.logger.Info(...)` (component-scoped) for some things and `slog.Info(...)` (global, no component) for others. Example: `runner/webrunner/webrunner.go` has **107** component-scoped calls AND **23** bare `slog.*` calls.

#### 3. `scrapemate` library's internal logger (zerolog, JSON to stderr)

**Set up in** `scrapemate.go:46`:
```go
s.log = logging.Get().With("component", "scrapemate")
```

**Writer**: `gosom/kit/logging` creates a **zerolog** logger writing to **`os.Stderr`** (hardcoded in `logging.go:77`):
```go
var std = New("zerolog", INFO, os.Stderr)
```

**Format**: zerolog JSON (different field names than slog JSON -- e.g., `"level":"info"` vs `"level":"INFO"`, different timestamp format)

**What it logs**: Job start/finish, stats every minute, retries, errors, inactivity timeouts. These are critical operational logs.

**Key problem**: `scrapemate` logs go to `os.Stderr`, completely bypassing the application's `pkg/logger` system. When running `./brezel-api -web > /tmp/logfile.log 2>&1`, stderr IS redirected to the file, so these DO appear. But when running without redirection, they go to a different stream than the `slog` logs. The JSON format is also subtly different (zerolog vs slog), breaking unified parsing in Grafana/Loki.

### Summary: Where Logs Go

| Logger | stdout | stderr | `logs/brezel-api-*.log` | Format |
|---|---|---|---|---|
| `slog.*` (default) | Yes (if LOG_OUTPUT=both/stdout) | No | Yes (if LOG_OUTPUT=both/file) | slog JSON |
| `w.logger.*` (component) | Yes (same writer) | No | Yes (same writer) | slog JSON with `component` |
| `scrapemate` internal | No | Yes | No | zerolog JSON |

When running `./brezel-api > /tmp/logfile.log 2>&1`:
- stdout-targeted slog logs go to temp file AND `logs/brezel-api-*.log` (duplicated!)
- scrapemate logs go to temp file only (via stderr redirect)
- `logs/brezel-api-*.log` contains slog logs but NOT scrapemate logs

When running `./brezel-api > /tmp/logfile.log` (no 2>&1):
- slog logs go to temp file AND `logs/brezel-api-*.log`
- scrapemate logs go to terminal (stderr), completely invisible in the file

---

## Problems Found

### P1: Default LOG_OUTPUT="both" causes silent log duplication

With the default `LOG_OUTPUT=both`, every slog line is written to **both** stdout and the rotating file. When stdout is redirected to a file (`> /tmp/logfile.log`), you get:
- `/tmp/logfile.log` -- contains slog logs + scrapemate stderr logs
- `logs/brezel-api-YYYY-MM-DD.log` -- contains slog logs only

This means debugging requires checking **two files**, and the rotating log file is missing scrapemate's operational data (job stats, retries, errors).

### P2: scrapemate uses a completely separate logging library

The `scrapemate` library uses `gosom/kit/logging` which wraps `zerolog` and writes to `os.Stderr`. This is a third-party dependency that:
- Cannot be configured to use the application's slog logger
- Has no `WithLogger` option that accepts a `*slog.Logger` (only `logging.Logger` interface)
- Produces differently-formatted JSON than slog
- Contains critical operational data (job completion, retries, timing)

### P3: Inconsistent component tagging within the same file

`runner/webrunner/webrunner.go` has both `w.logger.Info(...)` (107 calls, with `component=webrunner`) and `slog.Info(...)` (23 calls, no component). The bare `slog.*` calls happen during startup initialization (before `w.logger` is available) and in static/helper functions. This means filtering by `component=webrunner` misses 18% of that package's logs.

Similarly, `runner/webrunner/writers/` uses only bare `slog.*` with no component tag.

### P4: No job_id in most log lines

The `exiter/`, `gmaps/`, and `postgres/resultwriter.go` packages use bare `slog.*` calls with no `job_id` field. When multiple jobs run concurrently, it is impossible to correlate these logs to a specific job. Only `webrunner`'s `w.logger.*` calls consistently include `job_id`.

### P5: Port-binding failures are not surfaced loudly enough

`web/web.go:365-367` returns the error from `ListenAndServe`, and the webrunner's `errgroup` propagates it, but:
- The error message is a generic OS error ("bind: address already in use")
- There is no pre-flight port check or explicit log explaining "port X is already in use by another process"
- The `Start` function logs `server_started` BEFORE `ListenAndServe`, so the log says "started" even when binding fails immediately after
- No PID file or process detection to warn about stale processes

### P6: No `log.Printf` usage (good news)

The standard library `log` package is **not imported** by any application code (only `log/slog`). The `scripts/debug_maps_reviews/main.go` uses `fmt.Printf` but that is a debug tool, not production code.

### P7: `fmt.Fprintln(os.Stderr)` used for critical errors

`main.go:38` and `runner/runner.go:507,520` write directly to stderr using `fmt.Fprintln`. These bypass all logging infrastructure and are not structured JSON.

---

## Root Causes

### RC1: "Both" default was a transitional compromise

The `LOG_OUTPUT=both` default was likely added during a migration from file-only logging to support stdout-based log collection. It was never switched to stdout-only, leaving the file writer as a vestigial destination.

### RC2: scrapemate's logging is architecturally incompatible

The `scrapemate` library's `ScrapeMate` struct accepts a `logging.Logger` (from `gosom/kit`), not a `*slog.Logger`. The library's `New()` function falls back to `logging.Get()` which is a zerolog-based logger writing to stderr. Even though `scrapemate.WithLogger()` exists, it requires a `logging.Logger` interface, not slog.

### RC3: No logging convention enforced

There is no linter rule or convention document requiring that all log calls:
- Use a component-scoped logger (not bare `slog.*`)
- Include `job_id` when operating in a job context
- Avoid direct stdout/stderr writes

### RC4: Server lifecycle logs are optimistic

The "server_started" log is emitted before the blocking `ListenAndServe` call. If binding fails, the log record is misleading.

---

## Recommended Fix

### Target architecture

All logs from all sources (application code, scrapemate library, HTTP middleware) go to a **single structured JSON stream on stdout**. The rotating file writer is eliminated. Log collection is handled by the infrastructure (Docker/systemd -> Grafana Agent -> Loki).

Every log line contains at minimum:
```json
{
  "time": "2026-04-02T12:00:00.000Z",
  "level": "INFO",
  "msg": "job_finished",
  "component": "webrunner",
  "job_id": "abc-123",
  "user_id": "user_456",
  ...structured fields...
}
```

### Specific changes

#### 1. Set `LOG_OUTPUT=stdout` as default

Change `pkg/logger/logger.go:21`:
```go
const defaultLogOutputMode = "stdout"
```

Keep the file writer code for environments that explicitly need it, but stop defaulting to dual-write.

#### 2. Bridge scrapemate's logger to slog

Create an adapter that implements `gosom/kit/logging.Logger` but delegates to `*slog.Logger`:

```go
// pkg/logger/scrapemate_bridge.go
type scrapemateBridge struct {
    slogger *slog.Logger
}

func (b *scrapemateBridge) Info(msg string, args ...any)  { b.slogger.Info(msg, args...) }
func (b *scrapemateBridge) Warn(msg string, args ...any)  { b.slogger.Warn(msg, args...) }
// ... etc for all logging.Logger methods
```

Then in the scrapemate initialization:
```go
bridge := pkglogger.NewScrapeMateBridge(logger.With("component", "scrapemate"))
mate, err := scrapemate.New(
    scrapemate.WithLogger(bridge),
    // ...
)
```

This routes all scrapemate logs through the same slog handler.

#### 3. Eliminate bare `slog.*` calls

Every package that currently calls `slog.Info(...)` directly should instead receive a `*slog.Logger` via dependency injection. At minimum:
- `exiter.Exiter` should accept a `*slog.Logger` parameter
- `gmaps` job/entry/place functions should use the logger from context (`logger.FromContext(ctx)`)
- `postgres/resultwriter.go` should accept a `*slog.Logger` parameter
- `runner/webrunner/writers/` should accept a `*slog.Logger` parameter

#### 4. Propagate job_id through context

Use `pkg/logger.WithContext()` to store a job-scoped logger in the context at the start of each job:
```go
jobLogger := w.logger.With("job_id", job.ID, "user_id", job.UserID)
ctx = pkglogger.WithContext(ctx, jobLogger)
```

Then all downstream code uses `pkglogger.FromContext(ctx)` instead of `slog.Default()`.

#### 5. Fix server startup logging

Move the "server_started" log to after successful bind:
```go
ln, err := net.Listen("tcp", s.srv.Addr)
if err != nil {
    return fmt.Errorf("failed to bind %s: %w", s.srv.Addr, err)
}
s.logger.Info("server_started", slog.String("addr", s.srv.Addr))
return s.srv.Serve(ln)
```

#### 6. Add process/port conflict detection

Before starting the HTTP server, check if the port is already in use and log the PID of the conflicting process:
```go
func checkPortAvailable(addr string) error {
    ln, err := net.Listen("tcp", addr)
    if err != nil {
        return fmt.Errorf("port %s already in use (check for stale processes): %w", addr, err)
    }
    ln.Close()
    return nil
}
```

#### 7. Replace `fmt.Fprintln(os.Stderr, ...)` with slog

`main.go:38` and `runner/runner.go:507,520` should use `slog.Error()` instead of raw stderr writes, with one exception: the security guard in main.go that fires before the logger is initialized should stay as stderr.

#### 8. Add a linter rule

Add a `golangci-lint` custom rule (or `revive` rule) that flags:
- Direct `slog.Info/Warn/Error/Debug` calls outside of `main.go` and `pkg/logger/`
- Imports of `"log"` (standard library) in non-test files
- `fmt.Fprintf(os.Stderr, ...)` or `fmt.Fprintln(os.Stderr, ...)` in non-main files

---

## Migration Plan

### Phase 1: Stop the bleeding (low risk, immediate)

1. **Change default `LOG_OUTPUT` to `"stdout"`** in `pkg/logger/logger.go`. This is a one-line change and eliminates the dual-write problem. Production deployments that rely on the file can set `LOG_OUTPUT=file` explicitly.

2. **Fix server startup log ordering** in `web/web.go` -- bind first, then log.

3. **Add port conflict detection** -- pre-flight check before `ListenAndServe`.

### Phase 2: Bridge scrapemate (medium risk, 1-2 days)

4. **Create the `scrapemateBridge` adapter** in `pkg/logger/`.

5. **Wire it into all scrapemate initialization sites**: `webrunner.setupMate()`, `filerunner`, `databaserunner`, `lambdaaws`. The `scrapemate.WithLogger()` option already exists; we just need to pass the bridge.

6. **Test**: Run a job and verify scrapemate's "starting scrapemate", "scrapemate stats", and "job finished" messages appear in the same stream as application logs, with consistent JSON format.

### Phase 3: Unify component loggers (medium risk, 2-3 days)

7. **Add `*slog.Logger` parameters** to `exiter.New()`, result writer constructors, and writer types. Thread the component-scoped logger through.

8. **Convert bare `slog.*` calls** in `gmaps/`, `postgres/`, `runner/webrunner/writers/`, `webshare/` to use injected loggers. Prioritize by call count:
   - `gmaps/images/optimized_extractor.go` (~45 calls)
   - `exiter/exiter.go` (~25 calls)
   - `postgres/resultwriter.go` (~20 calls)

9. **Propagate `job_id` via context logger** in `webrunner.scrapeJob()` using `pkglogger.WithContext()`.

### Phase 4: Enforce conventions (low risk, ongoing)

10. **Add linter rules** to prevent regression.

11. **Document the logging convention** in a short section of the project README or a CONTRIBUTING.md.

---

## Files Referenced

| File | Role |
|---|---|
| `pkg/logger/logger.go` | Logger factory, output writer, rotating file writer |
| `main.go` | `slog.SetDefault()`, runner factory with component loggers |
| `web/web.go` | HTTP server startup, `s.logger` (component="api") |
| `runner/webrunner/webrunner.go` | Largest consumer (107 `w.logger` + 23 `slog.*` calls) |
| `exiter/exiter.go` | Exit monitor, bare `slog.*` only |
| `gmaps/images/optimized_extractor.go` | Image extraction, bare `slog.*` only |
| `postgres/resultwriter.go` | Result persistence, bare `slog.*` only |
| `billing/service.go` | Billing, `s.logger` (component="billing") |
| `proxy/pool.go` | Proxy pool, `p.logger` (receives `slog.Default()`) |
| `gosom/kit/logging/logging.go` | scrapemate's logger interface, zerolog to stderr |
| `gosom/scrapemate/scrapemate.go` | scrapemate core, `s.log` (zerolog, component="scrapemate") |
