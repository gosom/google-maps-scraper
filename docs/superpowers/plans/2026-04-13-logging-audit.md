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

### Fix 1: SC-M8 — Logger from context.Background() (Medium) — DONE `b25fb2b`

- [x] ~~processExtractedImages now accepts `ctx context.Context`~~ — passes scrapemate's context for trace/request correlation
- [x] ~~Process method passes its `ctx` parameter~~ — was discarding as `_`, now used for logger and image processing

### Fix 2: IW-M9 — Log file rotation unbounded counter (Medium) — DONE `b25fb2b`

- [x] ~~Added `maxLogParts = 1000` constant~~
- [x] ~~Guard returns error when limit exceeded~~

### Fix 3: IW-M8 — Logger file writer Close() (Low) — DONE `b25fb2b`

- [x] ~~Added `Close() error` method to `rotatingFileWriter`~~ — mutex-protected, nil-safe

### Fix 4: IW-L1 — Health check logs at INFO (Low) — DONE `b25fb2b`

- [x] ~~Removed redundant Info log~~ — RequestLogger middleware already covers it
- [x] ~~Changed DB probe failure to Warn level~~ — degraded state, not application error

### Fix 5 (NEW): InjectLogger missing user_id — DONE `b25fb2b`

- [x] ~~InjectLogger now enriches child logger with user_id~~ — every downstream log line in the request chain has both request_id and user_id for Grafana/Loki correlation

---

## Verification — DONE

- [x] `go build ./...` passes
- [x] `go test ./web/... ./pkg/logger/... -count=1 -timeout 60s` — all 7 packages pass
- [x] `context.Background()` removed from gmaps/place.go logger calls

---

## Grafana/Loki Log Flow (after fixes)

```
Request → RequestID middleware (generates uuid)
       → Auth middleware (extracts user_id from JWT)
       → InjectLogger (creates child logger with request_id + user_id)
       → Handler (logs with context-aware logger)
       → Service (gets logger from context via FromContext(ctx))
       → All log lines have: request_id, user_id, msg, level, timestamp
```

Every log line in the chain now has both `request_id` and `user_id`, enabling queries like:
- `{app="brezelscraper"} | json | user_id="user_123"` — all logs for a user
- `{app="brezelscraper"} | json | request_id="uuid-xyz"` — full request trace
- `{app="brezelscraper"} | json | level="ERROR" | user_id="user_123"` — errors for a specific user
