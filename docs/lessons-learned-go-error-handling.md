# Lessons Learned: Go Error Handling — To Wrap or Not to Wrap

**Source**: "Go errors: to wrap or not to wrap?" (March 7, 2026)
**Applied to**: google-maps-scraper backend (Go 1.25.4, service serving thousands of users)

---

## Core Decision Framework

The right approach depends on **where** the error is and **what** you're building:

| Context | Approach | Verb (`%w` vs `%v`) |
|---------|----------|---------------------|
| Within a package | Bare `return err` | Neither |
| At package boundaries | `fmt.Errorf("doing X: %w", err)` | `%w` |
| At system boundaries (DB, RPC, API) | Translate to domain errors | `%v` or sentinel |
| In libraries | Conservative wrapping | `%v` by default |
| In CLIs | Wrap everything | `%w` everywhere |
| In services (our case) | All of the above + `slog` at handler | Mixed |

---

## Rule 1: Bare `return err` Within a Package

If a function calls another function **in the same package**, the caller already has context. Wrapping is noise.

```go
// GOOD: same package, bare return
func (r *repository) processJob(ctx context.Context, job *Job) error {
    if err := r.validate(job); err != nil {
        return err // validate is in the same package
    }
    return r.save(ctx, job)
}

// BAD: redundant wrapping within package
func (r *repository) processJob(ctx context.Context, job *Job) error {
    if err := r.validate(job); err != nil {
        return fmt.Errorf("validating job: %w", err) // noise
    }
    return r.save(ctx, job)
}
```

**How we applied this**: The `wrapcheck` linter flags only cross-package bare returns, not same-package ones.

---

## Rule 2: Wrap at Package Boundaries with Identifying Info

When an error crosses a package boundary, the receiving code is the **last place** that knows what it was trying to do. Add the operation name and identifying data (user ID, job ID, item ID).

```go
// GOOD: cross-package call, wrap with context
user, err := users.Get(ctx, req.UserID)
if err != nil {
    return fmt.Errorf("getting user %s: %w", req.UserID, err)
}

// BAD: bare return loses context
user, err := users.Get(ctx, req.UserID)
if err != nil {
    return err // which call failed? no idea
}
```

**How we applied this**: All handler→service→repository calls include operation name and entity IDs.

---

## Rule 3: Don't Duplicate What the Inner Error Already Says

Each function is responsible for its **own** values, not re-stating what the wrapped error already provides.

```go
// BAD: path appears twice
return fmt.Errorf("opening %s: %w", path, err)
// open /etc/app.yaml: opening /etc/app.yaml: permission denied

// GOOD: add what you were doing, not what Open already said
return fmt.Errorf("reading config: %w", err)
// reading config: open /etc/app.yaml: permission denied
```

**How we applied this**: We avoid wrapping messages that restate the inner error's content.

---

## Rule 4: `%w` Creates API Contracts — Use `%v` at System Boundaries

`%w` makes the wrapped error traversable via `errors.Is` and `errors.As`. This means **callers can depend on the inner error type**. If you later swap databases, those callers break.

```go
// DANGEROUS: exposes pgx internals to callers
func (r *UserRepo) Get(ctx context.Context, id string) (*User, error) {
    if err := row.Scan(&u.Name); err != nil {
        return nil, fmt.Errorf("getting user %s: %w", id, err)
        // callers can now do errors.Is(err, pgx.ErrNoRows)
    }
}

// SAFE: translate to domain error, use %v for the rest
var ErrNotFound = errors.New("not found")

func (r *UserRepo) Get(ctx context.Context, id string) (*User, error) {
    if err := row.Scan(&u.Name); err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrNotFound // YOUR sentinel, not pgx's
        }
        return nil, fmt.Errorf("getting user %s: %v", id, err)
    }
}
```

**Key insight**: Going from `%v` to `%w` is backward-compatible (exposes more). Going from `%w` to `%v` is **breaking** (callers relying on `errors.Is` stop working). When in doubt, start with `%v`.

**How we applied this**: Our `models` package defines `ErrNotFound`, `ErrAPIKeyNotFound` etc. Repository layers translate DB-specific errors into these domain sentinels.

---

## Rule 5: Structured Logging as an Alternative to Deep Wrapping

Dave Cheney — the person who **created** `pkg/errors` and popularized error wrapping — eventually walked away from his own advice:

> "I no longer use this package, in fact I no longer wrap errors."

His reasoning: **structured logging** can carry the debugging context that wrapping was meant to provide.

```go
// WRAPPING approach: context baked into error string
err = inventory.Reserve(ctx, req.ItemID, req.Qty)
if err != nil {
    return fmt.Errorf("reserving stock for %s: %w", req.ItemID, err)
}
// Log: reserving stock for item-123: connection refused

// STRUCTURED LOGGING approach: context as searchable fields
err = inventory.Reserve(ctx, req.ItemID, req.Qty)
if err != nil {
    slog.Error("reserve_stock_failed",
        slog.String("item_id", req.ItemID),
        slog.Any("error", err))
    return err
}
// Log: level=ERROR msg="reserve_stock_failed" item_id=item-123 err="connection refused"
```

Same information, but with structured logging:
- Fields are **indexable** in Grafana/Loki
- Fields are **filterable** (show me all errors for user X)
- Fields are **aggregatable** (how many reserve_stock failures this hour?)
- Error string doesn't change when code is refactored

**How we applied this**: All error paths in our handlers use `slog.Error()` with `user_id`, `job_id`, `path`, `method` fields. This is our primary debugging tool in Grafana — not error string matching.

---

## Rule 6: The Service Pattern (What We Use)

For our backend service, we combine all approaches:

```go
// HANDLER LAYER: slog for request context, %v to external callers
func (h *APIHandlers) GetJob(w http.ResponseWriter, r *http.Request) {
    userID, err := auth.GetUserID(r.Context())
    if err != nil {
        renderJSON(w, http.StatusUnauthorized, ...)
        return
    }
    job, err := h.Deps.App.Get(r.Context(), id, userID)
    if err != nil {
        slog.Error("get_job_failed",
            slog.String("user_id", userID),
            slog.String("job_id", id),
            slog.String("path", r.URL.Path),
            slog.Any("error", err))
        renderJSON(w, http.StatusNotFound, genericError)
        return
    }
}

// SERVICE LAYER: wrap at package boundaries with %w
func (s *Service) Get(ctx context.Context, id, userID string) (*Job, error) {
    job, err := s.repo.Get(ctx, id, userID) // cross-package
    if err != nil {
        return nil, fmt.Errorf("getting job %s: %w", id, err)
    }
    return job, nil
}

// REPOSITORY LAYER: translate DB errors to domain errors
func (r *repository) Get(ctx context.Context, id, userID string) (*Job, error) {
    err := r.db.QueryRowContext(ctx, query, id, userID).Scan(...)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, models.ErrNotFound // domain sentinel
        }
        return nil, fmt.Errorf("querying job: %v", id, err) // %v, not %w
    }
}
```

---

## Rule 7: Never Leak `err.Error()` to HTTP Clients

Internal errors can contain database table names, SQL queries, file paths, stack traces, and third-party API details. These are security risks and useless to the end user.

```go
// NEVER DO THIS
http.Error(w, err.Error(), http.StatusInternalServerError)
// Client sees: "pq: relation \"users\" does not exist"

// ALWAYS DO THIS
slog.Error("operation_failed",
    slog.String("user_id", userID),
    slog.Any("error", err))
http.Error(w, "Internal server error", http.StatusInternalServerError)
```

**How we applied this**: Phase 1 and Phase 5 eliminated all `err.Error()` leaks across billing, integration, auth, and web handlers. Every error path now logs the full error server-side with structured fields and returns a generic message to the client.

---

## Quick Reference: Decision Tree

```
Is the error crossing a package boundary?
├── No (same package) → bare return err
└── Yes
    ├── Is it crossing a system boundary (DB, RPC, external API)?
    │   ├── Can the caller meaningfully act on the specific error?
    │   │   ├── Yes → translate to domain sentinel (ErrNotFound)
    │   │   └── No → fmt.Errorf("doing X: %v", err)
    │   └── Always log with slog at the handler level
    └── Is it crossing an internal package boundary?
        ├── Are you adding useful context (IDs, names)?
        │   ├── Yes → fmt.Errorf("doing X for %s: %w", id, err)
        │   └── No → bare return err (wrapping adds noise)
        └── Should callers inspect the inner error?
            ├── Yes → %w (and document this contract)
            └── No → %v (safer default)
```

---

## Mistakes We Fixed in This Codebase

| Mistake | Where | Fix |
|---------|-------|-----|
| Bare `return err` across package boundaries | 11+ handlers passing `""` userID | Added `fmt.Errorf` wrapping with user/job context |
| `err.Error()` leaked to HTTP clients | 10+ handlers in billing, integration, auth | Generic message to client + `slog.Error` server-side |
| No structured fields on error logs | All handler files | Added `user_id`, `job_id`, `path`, `method` to every `slog.Error` |
| `fmt.Printf` instead of `slog` | 43 calls in optimized_extractor.go | Replaced with structured `slog.Debug`/`slog.Warn` |
| No domain error translation | Repository returning raw DB errors | Added `ErrNotFound` sentinel translation in repos |
| Overwrapping with redundant context | Multiple layers adding the same info | Stripped redundant wrappers, let inner error speak |

---

## Key Takeaways for This Project

1. **Our handlers are the slog boundary** — every error that reaches a handler gets logged with `user_id`, `job_id`, `path`, `method` before returning a generic message to the client.

2. **Our repositories translate, not wrap** — `pgx.ErrNoRows` becomes `models.ErrNotFound`. Callers never depend on our database driver.

3. **Our service layer wraps with `%w`** — cross-package calls include operation name and entity IDs for the error chain.

4. **Our logging is Grafana/Loki-native** — JSON output via `slog.NewJSONHandler()`, searchable by any structured field. When a user reports a bug: search `user_id="usr_xxx"` and see every error they triggered.

5. **`%v` is our default at system boundaries** — we don't expose internal error types to callers. Domain sentinels handle the "which error" question.

6. **Wrapping is not a substitute for structured logging** — both serve different purposes. Wrapping builds the error chain for intermediate code. Logging provides the request context for operators in Grafana.
