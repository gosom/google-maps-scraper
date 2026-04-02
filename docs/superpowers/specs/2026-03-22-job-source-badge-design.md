# Job Source Badge — Design Spec

**Date:** 2026-03-22
**Status:** Approved

## Problem

Jobs created via API key and jobs created via the web UI are indistinguishable in the frontend. Users need a visual indicator to know how each job was submitted.

## Solution

Add a `source` column to the `jobs` table and display a badge next to the status pill in the job table.

## Design

### Database

- Add `source TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('web', 'api'))` directly to the existing `000005_web_jobs_table.up.sql` migration (no new migration file — we are pre-production with no real user data, so we can drop and recreate)
- `source` is write-once — set at INSERT time, never updated

### Backend — Model

Add `Source string` field to the `Job` struct in `models/job.go` with `json:"source"` tag.

### Backend — Job Creation Handler

In `APIHandlers.Scrape()` (`web/handlers/api.go`):
- Check if `auth.APIKeyIDKey` is present in the request context
- If present: set `job.Source = "api"`
- If absent: set `job.Source = "web"`

Note: Both web UI (HTMX form) and API requests flow through the same `Scrape()` handler. The detection works because Clerk JWT auth does not set `APIKeyIDKey` while API key auth does.

### Backend — Repository: Postgres

In `postgres/repository.go`:
- Add `Source string` to internal `job` struct (line 272)
- Update `rowToJob()` (line 221): scan `source` column, map to `ans.Source`
- Update `jobToRow()` (line 254): include `Source` in returned struct
- Update all SELECT queries to include `source` column
- Update INSERT query to include `source` column

### Backend — Repository: SQLite

In `web/sqlite/sqlite.go`:
- Update INSERT query (line 42) to include `source`
- Update `rowToJob()` to scan `source`
- Update `jobToRow()` to include `Source`

### Backend — Concurrent Limit Service

In `web/services/concurrent_limit.go` (line 152-158):
- Update the INSERT query to include `source` column
- Pass `job.Source` as an additional parameter

This is the **primary production INSERT path** when billing is enabled. Without this change, all jobs would silently default to `'web'`.

### Frontend — Template

In `job_row.html`, add a source badge inside the Status `<td>`, after the status indicator:

```html
<span class="source-badge source-{{.Source}}">{{if eq .Source "api"}}API{{else}}Web{{end}}</span>
```

No changes to table headers — the badge lives inside the existing Status cell.

### Frontend — CSS

Add badge styles to `main.css`:

```css
.source-badge {
    display: inline-block;
    padding: 3px 8px;
    border-radius: 10px;
    font-size: 10px;
    font-weight: 600;
    margin-left: 6px;
    vertical-align: middle;
}

.source-api {
    background-color: #e3f2fd;
    color: #1565c0;
}

.source-web {
    background-color: #f3e5f5;
    color: #7b1fa2;
}
```

## Files Changed

| File | Change |
|------|--------|
| `scripts/migrations/000005_web_jobs_table.up.sql` | Add `source` column inline to existing CREATE TABLE |
| `models/job.go` | Add `Source string` field with json tag |
| `web/handlers/api.go` | Set `Source` based on auth context |
| `postgres/repository.go` | Add `Source` to internal struct, `rowToJob`, `jobToRow`, SELECT/INSERT queries |
| `web/sqlite/sqlite.go` | Add `source` to INSERT, `rowToJob`, `jobToRow` |
| `web/services/concurrent_limit.go` | Add `source` to INSERT query |
| `web/static/templates/job_row.html` | Add source badge next to status |
| `web/static/css/main.css` | Add `.source-badge` styles |

## Out of Scope

- Storing which specific API key created the job (future enhancement)
- Filtering/sorting by source in the UI
