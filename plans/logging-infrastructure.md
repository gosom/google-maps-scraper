# Logging Infrastructure

## Architecture

```
Go App (JSON stdout) --> Docker Loki Driver --> Loki --> Grafana
```

- **Go app** outputs structured JSON logs to stdout via `log/slog`
- **Docker Loki logging driver** captures container stdout and ships logs directly to Loki
- **Loki** stores and indexes logs (port 3100)
- **Grafana** provides the search/query UI (port 3001)

## Technology Choices

| Component | Technology | Version | Why |
|-----------|-----------|---------|-----|
| Structured logging | `log/slog` (Go stdlib) | Go 1.21+ | Zero dependencies, built into Go |
| Log storage | Grafana Loki | 3.4.2 | Lightweight, designed for logs, low resource usage |
| Log UI | Grafana | 11.6.0 | Industry standard, free, powerful query language |
| Log shipping | Loki Docker driver | 3.4.2 | Simplest option for single-host Docker deployments |

## LOG_LEVEL Configuration

Set via `LOG_LEVEL` environment variable (already in Docker Compose files):

| Value | Behavior |
|-------|----------|
| `debug` | All logs (default for dev) |
| `info` | Info, warn, error (default for staging/production) |
| `warn` | Warn and error only |
| `error` | Errors only |

## Log Output Configuration

The logger supports writing JSON logs to stdout, file, or both:

| Variable | Values | Default | Purpose |
|----------|--------|---------|---------|
| `LOG_OUTPUT` | `stdout`, `file`, `both` | `both` | Controls where logs are written |
| `LOG_FILE_PATH` | absolute/relative path | _(unset)_ | Explicit file path (overrides directory/name) |
| `LOG_DIR` | directory path | `logs` | Log directory when `LOG_FILE_PATH` is not set |
| `LOG_FILE_NAME` | filename | `brezel-api.log` | Log filename when `LOG_FILE_PATH` is not set |
| `LOG_MAX_SIZE_MB` | integer MB | `100` | Max size per dated file before rolling to next part |
| `LOG_RETENTION_DAYS` | integer days | `14` | Number of days of dated files to keep |

### Local terminal default

Without extra env vars, terminal runs now write to both:
- stdout (for real-time viewing)
- daily files in `logs/`, e.g. `logs/brezel-api-2026-02-12.log`
- if a daily file exceeds `LOG_MAX_SIZE_MB`, rollover continues as:
  `logs/brezel-api-2026-02-12.1.log`, `logs/brezel-api-2026-02-12.2.log`, etc.

At midnight (or next process start on a new day), logging switches to a new dated file automatically.

### Container recommendation

For Docker + Loki environments, keep:

```bash
LOG_OUTPUT=stdout
```

This avoids duplicate file logging inside containers and keeps Loki as the source of truth.

## Structured Log Format

Every log line is a JSON object written to stdout:

```json
{
  "time": "2026-02-12T10:30:00.123Z",
  "level": "INFO",
  "msg": "http_request",
  "component": "api",
  "method": "GET",
  "path": "/api/v1/jobs",
  "status": 200,
  "duration": "12.3ms",
  "user_id": "user_abc123",
  "request_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

### Standard Fields

| Field | Type | Description |
|-------|------|-------------|
| `time` | string | ISO 8601 timestamp |
| `level` | string | DEBUG, INFO, WARN, ERROR |
| `msg` | string | Snake_case event description |
| `component` | string | Which subsystem: api, webrunner, billing, migration, proxy, webshare, auth |
| `request_id` | string | UUID assigned per HTTP request (via X-Request-ID header) |
| `user_id` | string | Authenticated user ID (when available) |
| `error` | string | Error message (on error logs) |

## Querying Logs in Grafana

Access Grafana at `http://<server>:3001` (default: admin/admin for staging, admin/changeme for production — change immediately).

Go to **Explore** > Select **Loki** datasource > Use LogQL queries:

### Common Queries

```logql
# All errors in last 24h
{service="backend"} | json | level="ERROR"

# Errors in billing component
{service="backend"} | json | component="billing" | level="ERROR"

# All logs for a specific user
{service="backend"} | json | user_id="user_abc123"

# Trace a specific request
{service="backend"} | json | request_id="550e8400-e29b-41d4-a716-446655440000"

# Slow requests (over 1 second)
{service="backend"} | json | msg="http_request" | duration > 1s

# Webhook events
{service="backend"} | json | component="billing" | msg=~"webhook.*"

# Job scraping errors
{service="backend"} | json | component="webrunner" | level="ERROR" | msg=~"job.*"
```

## Host Setup (Required Once)

Install the Loki Docker logging driver on the host before starting containers:

```bash
docker plugin install grafana/loki-docker-driver:3.4.2 --alias loki --grant-all-permissions
```

Verify it's installed:

```bash
docker plugin ls
```

### Fallback

If the Loki driver is not installed, Docker will refuse to start containers. To temporarily fall back to file-based logging, change the logging driver in docker-compose:

```yaml
logging:
  driver: "json-file"
  options:
    max-size: "100m"
    max-file: "3"
```

## Adding Logging to New Code

### In a struct (preferred for components)

```go
import (
    "log/slog"
    pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
)

type MyService struct {
    logger *slog.Logger
}

func NewMyService() *MyService {
    return &MyService{
        logger: pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "myservice"),
    }
}

func (s *MyService) DoWork() {
    s.logger.Info("work_started", slog.String("task", "example"))
    s.logger.Error("work_failed", slog.Any("error", err))
}
```

### In HTTP handlers (use request-scoped logger)

```go
import pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"

func (h *MyHandler) Handle(w http.ResponseWriter, r *http.Request) {
    log := pkglogger.FromContext(r.Context())
    // log already has request_id attached
    log.Info("handling_request", slog.String("user_id", userID))
}
```

### In standalone functions

```go
import "log/slog"

func helperFunc() {
    slog.Info("helper_called")  // uses slog.Default() which outputs JSON
}
```

## Files Modified

- `pkg/logger/logger.go` — centralized slog factory
- `main.go` — sets slog.SetDefault for JSON output
- `web/middleware/middleware.go` — RequestID + structured RequestLogger
- `web/web.go` — Server struct uses *slog.Logger
- `web/handlers/*.go` — all handler logging migrated
- `billing/service.go` — billing logging migrated
- `runner/webrunner/webrunner.go` — webrunner logging migrated
- `postgres/migration.go` — migration logging migrated
- `proxy/*.go` — proxy logging migrated
- `webshare/client.go` — webshare logging migrated
- `web/auth/auth.go` — auth logging migrated
- `docker-compose.staging.yaml` — Loki + Grafana services added
- `docker-compose.production.yaml` — Loki + Grafana services added
- `grafana/provisioning/datasources/loki.yaml` — auto-configures Loki in Grafana
