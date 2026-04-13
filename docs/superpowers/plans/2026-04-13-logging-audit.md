# Logging Audit — Production Readiness

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all remaining logging issues from the production readiness report before launch. Ensure structured logging, no PII leakage, proper log levels, and clean shutdown.

**Source:** Findings from `docs/production-readiness-report.md`, re-audited 2026-04-13 against current code.

---

## Audit Results

| Finding | Original ID | Status | Severity |
|---------|-------------|--------|----------|
| Proxy credential logging | MP-H3/MP-H4 | Fixed | -- |
| fmt.Printf in production code | IE-H4 | Fixed | -- |
| PII/credentials in log messages | NEW | Clean | -- |
| Refund log wrong value | BL-M1 | Clean | -- |
| Logger from context.Background() | SC-M8 | **Still present** | Medium |
| Log file rotation unbounded counter | IW-M9 | **Still present** | Medium |
| Logger file writer never closed | IW-M8 | **Partial** | Low |
| Health check logs at INFO every request | IW-L1 | **Still present** | Low |

4 issues remain. 4 already fixed.

---

## Golang Skills Applied

| Skill | What it taught us | How it applies |
|-------|-------------------|----------------|
| **golang-observability** | "Use `slog.InfoContext(ctx, ...)` to correlate logs with traces. Context is the vehicle that carries trace_id and span_id." | SC-M8 fix: `gmaps/place.go` must use the request context, not `context.Background()`, or trace correlation is broken. |
| **golang-observability** | "Choose the right log level: Debug for development, Info for normal operations." | IW-L1 fix: health check is infrastructure polling, not a business event. Should be Debug, not Info. |
| **golang-observability** | "Errors MUST be either logged OR returned (NEVER both)." | General principle: verify no double-logging in error paths. |
| **golang-security** | "PII in logs: sanitize. Never log secrets, tokens, passwords, or user-controlled data that could contain PII." | Verified clean: proxy URLs sanitized, no API keys/secrets/passwords in slog calls. |
| **golang-security** | "Log injection: untrusted user input in log messages can break log parsers or inject fake log entries." | Structured slog prevents injection (values are escaped), but verify no raw string concatenation in log messages. |
| **golang-design-patterns** | "defer Close() immediately after opening. Later code changes can accidentally skip cleanup." | IW-M8 fix: rotatingFileWriter needs a Close() method called via defer in the shutdown path. |
| **golang-design-patterns** | "Limit everything (pool sizes, queue depths, buffers). Unbounded resources grow until they crash." | IW-M9 fix: unbounded part counter in log rotation can create infinite files. Add a max limit. |
| **golang-concurrency** | "Every goroutine must have a clear exit." | Related: the cleanup goroutine in rate limiter middleware (MP-M3) also has no shutdown. Not in scope for this plan but noted. |
| **golang-data-structures** | "Preallocate slices with make(T, 0, n) when size is known." | Not directly applicable to logging, but relevant for log buffer sizing. |

---

## Implementation Steps

### Fix 1: SC-M8 — Logger from context.Background() (Medium)

`gmaps/place.go` lines 113 and 207 use `scrapemate.GetLoggerFromContext(context.Background())` which returns a bare logger with no request context. This breaks trace correlation and any context-enriched log fields.

- [ ] **1.1** In `gmaps/place.go`, find `processExtractedImages` (line ~113). The function receives data from a response but has no `ctx` parameter. Check if the calling function has a context available. If yes, pass it through. If the method signature can be changed, add `ctx context.Context` as the first parameter and use it for the logger.

- [ ] **1.2** Same for line ~207 (second occurrence). Trace the call site to find the nearest available context.

- [ ] **1.3** If changing the function signatures is not possible (e.g., interface constraint from scrapemate), use the response's context if available: `scrapemate.GetLoggerFromContext(resp.Context)` or similar. Check the scrapemate library API for how to get context from a response.

- [ ] **1.4** Search for any other `context.Background()` uses in `gmaps/` that should use a request context instead. Fix if found.

### Fix 2: IW-M9 — Log file rotation unbounded counter (Medium)

`pkg/logger/logger.go` `openNextWritablePart()` increments `w.currentPart` in an infinite loop with no max limit. If the disk is full or files keep rolling, this loops forever.

- [ ] **2.1** Add a constant `maxLogParts = 1000` (or similar reasonable limit).

- [ ] **2.2** In the loop, check `w.currentPart > maxLogParts` and return an error if exceeded:
```go
if w.currentPart > maxLogParts {
    return fmt.Errorf("log rotation exceeded %d parts for date %s", maxLogParts, w.currentDate)
}
```

### Fix 3: IW-M8 — Logger file writer Close() (Low)

`pkg/logger/logger.go` `rotatingFileWriter` has no `Close()` method. On shutdown, buffered data may be lost.

- [ ] **3.1** Add a `Close() error` method to `rotatingFileWriter` that flushes and closes the underlying `*os.File`:
```go
func (w *rotatingFileWriter) Close() error {
    w.mu.Lock()
    defer w.mu.Unlock()
    if w.file != nil {
        return w.file.Close()
    }
    return nil
}
```

- [ ] **3.2** Check if there is a graceful shutdown path in `main.go` or `web/web.go` where this Close() should be called via defer. If the logger is a package-level singleton, add a `CloseLogger()` function and call it in the shutdown sequence.

### Fix 4: IW-L1 — Health check logs at INFO (Low)

The health endpoint logs at INFO level on every request. With load balancer health checks every 10-30 seconds, this creates noise.

- [ ] **4.1** In `web/handlers/web.go`, find the health check handler's logger call (line ~55). Either:
  - Remove the log line entirely (the request logger middleware already logs all requests), or
  - Change from `Info` to `Debug`:
    ```go
    h.Deps.Logger.Debug("health_check", slog.String("path", r.URL.Path))
    ```

- [ ] **4.2** Consider excluding `/health` from the `RequestLogger` middleware entirely. Health checks are infrastructure, not business events. Check if the middleware has a path-exclusion mechanism, or add one:
```go
if r.URL.Path == "/health" {
    next.ServeHTTP(w, r)
    return
}
```

---

## Out of Scope (noted for future)

| Issue | ID | Why deferred |
|-------|-----|-------------|
| Rate limiter cleanup goroutine leaks | MP-M3 | Requires middleware refactor, not a logging issue |
| loggingResponseWriter breaks Flusher/Hijacker | MP-M1 | Middleware architecture, not logging content |
| Recovery middleware writes after headers sent | MP-M2 | Error recovery, not logging |

---

## Verification

After all fixes:
- [ ] `go build ./...` passes
- [ ] `go test ./pkg/logger/... -count=1` passes
- [ ] `go test ./web/... -count=1 -timeout 60s` passes
- [ ] `grep -rn "context.Background()" gmaps/place.go` returns zero results
- [ ] `grep -rn "fmt.Printf\|fmt.Println" --include="*.go" . | grep -v _test.go | grep -v scripts/` returns zero results
