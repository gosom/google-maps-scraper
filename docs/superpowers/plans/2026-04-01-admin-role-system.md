# Admin Role System — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable internal admin users to run mass scraping jobs (e.g., scrape all of Berlin) without purchasing credits through Stripe, while reusing the entire existing scraping infrastructure.

**Architecture:** The system already has a `role` column (`'user'`|`'admin'`) on the `users` table and an unfinished `RequireRole` middleware. We complete the RBAC wiring by: (1) populating the role in auth context after user lookup, (2) adding dedicated admin routes at `/api/v1/admin/*` behind `RequireRole("admin")`, (3) skipping credit checks and billing for admin-sourced jobs, and (4) providing a CLI tool for admin promotion. This follows the same AuthN/AuthZ separation pattern used by AWS, Stripe, and other major SaaS platforms — same authentication provider (Clerk), different authorization path based on role stored server-side in the database (not JWT claims), so role changes take effect immediately without token refresh.

**Tech Stack:** Go 1.23+, gorilla/mux, Clerk JWT auth, PostgreSQL, existing billing/job infrastructure.

**Security Design Decisions:**
- **Role stored in DB, not JWT claims** — role changes are immediate, no token refresh lag. This is the standard for server-side RBAC.
- **Admin routes isolated in `/api/v1/admin/*`** — separate route namespace enables independent auditing, WAF rules, and rate limiting.
- **No self-promotion API** — users cannot change their own role. Promotion requires direct DB access or a CLI tool with DB credentials. This is intentional — the admin promotion path should be as narrow as possible.
- **Admin auth is Clerk JWT only (Phase 1)** — API key auth is explicitly rejected in admin handlers (defense-in-depth, in addition to route-level middleware). API keys can be leaked; Clerk sessions require interactive login. This prevents privilege escalation from a compromised API key. API key admin access can be added in Phase 2 with additional controls (IP allowlist, short TTL).
- **Admin jobs tagged with `source = 'admin'`** — enables filtering, auditing, and conditional billing bypass. The billing bypass is in the webrunner, not the billing service, so the billing service remains pure.
- **Defense in depth** — role is checked at the middleware layer (RequireRole) AND service layer (admin handlers only call non-credit-checked paths). Even if middleware is misconfigured, admin handlers don't expose credit-bypass to regular users.

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `web/auth/auth.go` | Populate `UserRoleKey` in context after user lookup |
| Modify | `models/job.go` | Add `SourceAdmin` constant |
| Create | `web/auth/role.go` | `IsAdmin(ctx)` helper + admin-specific auth utilities |
| Create | `scripts/migrations/000030_add_admin_job_source.up.sql` | Add `'admin'` to `jobs.source` CHECK constraint |
| Create | `scripts/migrations/000030_add_admin_job_source.down.sql` | Reverse the CHECK constraint change |
| Create | `web/handlers/admin.go` | Admin-specific handlers (job creation, listing, management) |
| Modify | `web/handlers/handlers.go` | Add `Admin` field to `HandlerGroup` |
| Modify | `web/web.go` | Register admin routes with `RequireRole` middleware |
| Modify | `runner/webrunner/webrunner.go` | Skip billing for admin-sourced jobs |
| Create | `scripts/promote_admin.sh` | CLI script to promote a user to admin |
| Create | `web/auth/role_test.go` | Tests for role helpers |
| Create | `web/handlers/admin_test.go` | Tests for admin handlers |

---

## Chunk 1: Foundation — Role Wiring & Model Constants

### Task 1: Add `SourceAdmin` constant and `IsAdmin` helper

**Files:**
- Modify: `models/job.go:68-71`
- Create: `web/auth/role.go`
- Create: `web/auth/role_test.go`

- [ ] **Step 1: Write the failing test for `IsAdmin`**

Create `web/auth/role_test.go`:

```go
package auth_test

import (
	"context"
	"testing"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name string
		role string
		want bool
	}{
		{"admin role returns true", models.RoleAdmin, true},
		{"user role returns false", models.RoleUser, false},
		{"empty role returns false", "", false},
		{"unknown role returns false", "superadmin", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), auth.UserRoleKey, tt.role)
			if got := auth.IsAdmin(ctx); got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go test ./web/auth/ -run TestIsAdmin -v`
Expected: FAIL — `IsAdmin` not defined.

- [ ] **Step 3: Add `SourceAdmin` constant to models**

In `models/job.go`, add to the job source constants block:

```go
const (
	SourceWeb   = "web"
	SourceAPI   = "api"
	SourceAdmin = "admin"
)
```

- [ ] **Step 4: Create `web/auth/role.go` with `IsAdmin` helper**

```go
package auth

import (
	"context"

	"github.com/gosom/google-maps-scraper/models"
)

// IsAdmin returns true if the authenticated user has the admin role.
func IsAdmin(ctx context.Context) bool {
	return GetUserRole(ctx) == models.RoleAdmin
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go test ./web/auth/ -run TestIsAdmin -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add models/job.go web/auth/role.go web/auth/role_test.go
git commit -m "feat: add SourceAdmin constant and IsAdmin auth helper"
```

---

### Task 2: Wire `UserRoleKey` into auth context

**Files:**
- Modify: `web/auth/auth.go:126-176` (Clerk JWT path)
- Modify: `web/auth/auth.go:194-219` (API key path)
- Modify: `web/auth/auth.go:101-118` (dev bypass path)

**Context:** The auth middleware currently sets `UserIDKey` in context but never sets `UserRoleKey`. The `GetUserRole` function defaults to `"user"` when unset. We need to look up the role from the DB user record (which already contains the `role` column) and inject it into context.

- [ ] **Step 1: Modify the Clerk JWT auth path to set role in context**

In `web/auth/auth.go`, the Clerk handler path (inside `authenticateRequest`), after the user is looked up or created, add role injection. Find the section where the user is fetched from the DB and the context is created:

Replace in `auth.go` — the Clerk JWT handler section. After the user provisioning block, change the context creation to also fetch and set the role:

```go
// In the clerkHandler section, after user provisioning, replace:
//     ctx := context.WithValue(r.Context(), UserIDKey, userID)
//     next.ServeHTTP(w, r.WithContext(ctx))
// With:

// Fetch the user record to get the role (for existing users, this was
// already done above; for newly-created users, the default is 'user').
dbUser, err := m.userRepo.GetByID(r.Context(), userID)
if err != nil {
	m.logger.Error("failed_to_fetch_user_role", slog.String("user_id", userID), slog.Any("error", err))
	http.Error(w, "Failed to retrieve user information", http.StatusInternalServerError)
	return
}

ctx := r.Context()
ctx = context.WithValue(ctx, UserIDKey, userID)
ctx = context.WithValue(ctx, UserRoleKey, dbUser.Role)
next.ServeHTTP(w, r.WithContext(ctx))
```

**Important refactor note:** The existing code already calls `m.userRepo.GetByID(r.Context(), userID)` at line 134 to check if the user exists. We should capture the user from that call and reuse it, avoiding a second DB query. The refactored flow:

```go
clerkHandler := clerkAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	claims, ok := clerk.SessionClaimsFromContext(r.Context())
	if !ok || claims == nil || claims.Subject == "" {
		http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
		return
	}
	userID := claims.Subject

	dbUser, err := m.userRepo.GetByID(r.Context(), userID)
	if err != nil {
		// User not found — auto-provision from Clerk
		clerkUser, err := m.userAPI.Get(r.Context(), userID)
		if err != nil {
			m.logger.Error("failed_to_retrieve_user_from_clerk", slog.String("user_id", userID), slog.Any("error", err))
			http.Error(w, "Failed to retrieve user information", http.StatusInternalServerError)
			return
		}

		var email string
		if clerkUser.PrimaryEmailAddressID != nil {
			primaryID := *clerkUser.PrimaryEmailAddressID
			for _, emailAddr := range clerkUser.EmailAddresses {
				if emailAddr.ID == primaryID {
					email = emailAddr.EmailAddress
					break
				}
			}
		} else if len(clerkUser.EmailAddresses) > 0 {
			email = clerkUser.EmailAddresses[0].EmailAddress
		}
		if email == "" {
			m.logger.Error("user_has_no_email", slog.String("user_id", userID))
			http.Error(w, "User has no email address", http.StatusBadRequest)
			return
		}

		newUser := postgres.User{ID: userID, Email: email}
		if err := m.userRepo.Create(r.Context(), &newUser); err != nil {
			slog.Error("failed to create user record",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
			http.Error(w, "Failed to create user record", http.StatusInternalServerError)
			return
		}
		if err := m.grantSignupBonus(r.Context(), userID); err != nil {
			m.logger.Error("failed_to_grant_signup_bonus", slog.String("user_id", userID), slog.Any("error", err))
		} else {
			m.logger.Info("signup_bonus_granted", slog.Float64("amount", SignupBonusAmount), slog.String("user_id", userID))
		}

		// Newly created users always have the default role ("user").
		// Set explicitly so the in-memory struct matches the DB default,
		// rather than relying on GetUserRole()'s empty-string fallback.
		newUser.Role = models.RoleUser
		dbUser = newUser
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, UserIDKey, userID)
	ctx = context.WithValue(ctx, UserRoleKey, dbUser.Role)
	next.ServeHTTP(w, r.WithContext(ctx))
}))
```

- [ ] **Step 2: Modify the API key auth path to set role in context**

In the API key auth section of `authenticateRequest` (~line 194-219), after validating the API key and getting the userID, fetch the user role:

```go
// After: ctx = context.WithValue(ctx, APIKeyIDKey, keyID)
// Add role lookup:
if apiUser, err := m.userRepo.GetByID(r.Context(), userID); err == nil {
	ctx = context.WithValue(ctx, UserRoleKey, apiUser.Role)
}
// If role lookup fails, GetUserRole() defaults to "user" — safe fallback.
```

- [ ] **Step 3: Modify the dev bypass path to support role testing**

In the dev bypass section (~line 101-115), add role from a dev header:

```go
if devUserID := strings.TrimSpace(r.Header.Get(DevUserHeaderName)); devUserID != "" {
	if m.logger != nil {
		m.logger.Warn("dev_auth_bypass", slog.String("user_id", devUserID))
	}
	ctx := context.WithValue(r.Context(), UserIDKey, devUserID)
	// Allow dev testing of role-based behavior
	if devRole := strings.TrimSpace(r.Header.Get("X-Braza-Dev-Role")); devRole != "" {
		ctx = context.WithValue(ctx, UserRoleKey, devRole)
	}
	next.ServeHTTP(w, r.WithContext(ctx))
	return
}
```

- [ ] **Step 4: Fix `RequireRole` middleware — remove TODO and use `GetUserRole` for consistency**

In `web/middleware/middleware.go`, the current implementation reads the role via raw context value assertion: `role, _ := r.Context().Value(auth.UserRoleKey).(string)`. If the role is never set, this yields `""` which happens to be safe (empty != "admin"), but it's fragile — if someone later refactors this to call `GetUserRole()` (which defaults to "user"), the behavior changes silently. Fix by using `GetUserRole` now so the behavior is explicitly consistent:

```go
// RequireRole returns middleware that rejects requests unless the authenticated
// user has the specified role. The role is read from the request context
// (set by the auth middleware after looking up the user).
func RequireRole(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := auth.GetUserRole(r.Context())
			if role != requiredRole {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code":403,"message":"forbidden"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 5: Run existing tests to verify nothing is broken**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go build ./...`
Expected: builds cleanly.

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go test ./web/... -v -count=1 -timeout 60s`
Expected: all existing tests pass.

- [ ] **Step 6: Commit**

```bash
git add web/auth/auth.go web/middleware/middleware.go
git commit -m "feat: populate UserRoleKey in auth context from DB user record"
```

---

### Task 3: Database migration — add 'admin' to jobs.source CHECK constraint

**Files:**
- Create: `scripts/migrations/000030_add_admin_job_source.up.sql`
- Create: `scripts/migrations/000030_add_admin_job_source.down.sql`

**Deployment ordering:** This migration MUST run BEFORE the code that creates admin jobs is deployed. The existing CHECK constraint `source IN ('web', 'api')` will reject `source='admin'` and cause 500 errors if the new code runs against the old schema. Since migrations run on startup (`postgres.NewMigrationRunner()`), this is automatically handled as long as the migration file is included in the same deployment as the code.

- [ ] **Step 1: Create the up migration**

```sql
-- Allow admin-sourced jobs (created by admin users, bypassing credit system).
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_source_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_source_check CHECK (source IN ('web', 'api', 'admin'));
```

- [ ] **Step 2: Create the down migration**

```sql
-- Revert: remove 'admin' from allowed sources.
-- WARNING: This will fail if any jobs with source='admin' exist.
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_source_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_source_check CHECK (source IN ('web', 'api'));
```

- [ ] **Step 3: Verify migration files exist**

Run: `ls -la /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend/scripts/migrations/000030_*`
Expected: both .up.sql and .down.sql present.

- [ ] **Step 4: Commit**

```bash
git add scripts/migrations/000030_add_admin_job_source.up.sql scripts/migrations/000030_add_admin_job_source.down.sql
git commit -m "feat: add 'admin' to jobs.source CHECK constraint"
```

---

## Chunk 2: Admin API Routes & Handlers

### Task 4: Create admin handlers

**Files:**
- Create: `web/handlers/admin.go`
- Create: `web/handlers/admin_test.go`

**Design:** Admin handlers are intentionally separate from regular handlers. This follows the security principle of isolation — admin code paths can be audited independently, and there's no risk of a logic error in a role check accidentally giving admin privileges to regular users. The admin job creation handler calls `App.Create()` directly, completely bypassing the credit system and concurrent limit enforcement.

- [ ] **Step 1: Write failing test for admin job creation**

Create `web/handlers/admin_test.go`:

```go
package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	"github.com/gosom/google-maps-scraper/web/handlers"
)

// mockAdminJobService implements handlers.JobService for testing.
type mockAdminJobService struct {
	createCalled bool
	lastJob      *models.Job
}

func (m *mockAdminJobService) Create(_ context.Context, job *models.Job) error {
	m.createCalled = true
	m.lastJob = job
	return nil
}
func (m *mockAdminJobService) All(_ context.Context, _ string) ([]models.Job, error) {
	return nil, nil
}
func (m *mockAdminJobService) Get(_ context.Context, _ string, _ string) (models.Job, error) {
	return models.Job{}, nil
}
func (m *mockAdminJobService) Delete(_ context.Context, _ string, _ string) error { return nil }
func (m *mockAdminJobService) Cancel(_ context.Context, _ string, _ string) error { return nil }
func (m *mockAdminJobService) GetCSV(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *mockAdminJobService) GetCSVReader(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func TestAdminCreateJob_SetsAdminSource(t *testing.T) {
	mock := &mockAdminJobService{}
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: mock},
	}

	body := map[string]interface{}{
		"name":     "Berlin mass scrape",
		"keywords": []string{"restaurants in Berlin"},
		"lang":     "de",
		"depth":    10,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs", bytes.NewReader(b))
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_admin_123")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleAdmin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.CreateJob(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	if !mock.createCalled {
		t.Fatal("expected App.Create to be called")
	}
	if mock.lastJob.Source != models.SourceAdmin {
		t.Errorf("expected source %q, got %q", models.SourceAdmin, mock.lastJob.Source)
	}
	if mock.lastJob.UserID != "user_admin_123" {
		t.Errorf("expected user_id %q, got %q", "user_admin_123", mock.lastJob.UserID)
	}
}

func TestAdminCreateJob_RejectsNonAdmin(t *testing.T) {
	h := &handlers.AdminHandlers{
		Deps: handlers.Dependencies{App: &mockAdminJobService{}},
	}

	body := map[string]interface{}{
		"name":     "test",
		"keywords": []string{"test"},
		"lang":     "en",
		"depth":    1,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs", bytes.NewReader(b))
	ctx := context.WithValue(req.Context(), auth.UserIDKey, "user_regular_456")
	ctx = context.WithValue(ctx, auth.UserRoleKey, models.RoleUser)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.CreateJob(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d: %s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go test ./web/handlers/ -run TestAdminCreate -v`
Expected: FAIL — `AdminHandlers` not defined.

- [ ] **Step 3: Create `web/handlers/admin.go`**

```go
package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
)

// AdminHandlers contains routes for admin-only operations.
// Admin handlers bypass credit checks and concurrent job limits.
// They are protected by RequireRole("admin") middleware at the router level,
// but also perform a defense-in-depth role check in each handler.
type AdminHandlers struct{ Deps Dependencies }

// CreateJob creates a scraping job without credit checks or concurrent limits.
// The job is tagged with source="admin" so the billing system skips charging.
func (h *AdminHandlers) CreateJob(w http.ResponseWriter, r *http.Request) {
	// Defense-in-depth: verify admin role even though middleware already checks.
	if !auth.IsAdmin(r.Context()) {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin access required"})
		return
	}

	// Block API key access to admin routes — admin operations require Clerk JWT.
	// This prevents privilege escalation from a compromised API key.
	if auth.GetAPIKeyID(r.Context()) != "" {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin routes require session authentication, not API keys"})
		return
	}

	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	var req apiScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Invalid request body"})
		return
	}
	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	newJob := models.Job{
		ID:     uuid.New().String(),
		UserID: userID,
		Name:   req.Name,
		Date:   time.Now().UTC(),
		Status: models.StatusPending,
		Data:   req.JobData,
		Source: models.SourceAdmin,
	}
	newJob.Data.MaxTime *= time.Second

	if err := webutils.ValidateJob(&newJob); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: err.Error()})
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Warn("admin_job_created",
			slog.String("admin_user_id", userID),
			slog.String("job_id", newJob.ID),
			slog.String("job_name", newJob.Name),
			slog.Int("keywords", len(req.JobData.Keywords)),
			slog.Int("depth", req.JobData.Depth),
		)
	}

	// Bypass ConcurrentLimitService — use App.Create directly.
	// No credit check, no concurrent job limit enforcement.
	if err := h.Deps.App.Create(r.Context(), &newJob); err != nil {
		internalError(w, h.Deps.Logger, err, "admin job creation failed",
			slog.String("user_id", userID), slog.String("job_id", newJob.ID))
		return
	}

	renderJSON(w, http.StatusCreated, models.ApiScrapeResponse{ID: newJob.ID})
}

// GetJobs lists all jobs for the admin user.
func (h *AdminHandlers) GetJobs(w http.ResponseWriter, r *http.Request) {
	if !auth.IsAdmin(r.Context()) {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin access required"})
		return
	}
	if auth.GetAPIKeyID(r.Context()) != "" {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin routes require session authentication, not API keys"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}

	jobs, err := h.Deps.App.All(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to list admin jobs",
			slog.String("user_id", userID))
		return
	}
	renderJSON(w, http.StatusOK, jobs)
}

// CancelJob cancels an admin job.
func (h *AdminHandlers) CancelJob(w http.ResponseWriter, r *http.Request) {
	if !auth.IsAdmin(r.Context()) {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin access required"})
		return
	}
	if auth.GetAPIKeyID(r.Context()) != "" {
		renderJSON(w, http.StatusForbidden, models.APIError{Code: http.StatusForbidden, Message: "admin routes require session authentication, not API keys"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	jobID := mux.Vars(r)["id"]
	if jobID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "Missing job ID"})
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Warn("admin_job_cancelled",
			slog.String("admin_user_id", userID),
			slog.String("job_id", jobID),
		)
	}

	if err := h.Deps.App.Cancel(r.Context(), jobID, userID); err != nil {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "Job not found"})
		return
	}
	renderJSON(w, http.StatusOK, map[string]any{"message": "Admin job cancellation initiated", "job_id": jobID})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go test ./web/handlers/ -run TestAdminCreate -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add web/handlers/admin.go web/handlers/admin_test.go
git commit -m "feat: add admin job handlers with credit bypass"
```

---

### Task 5: Add `AdminHandlers` to `HandlerGroup` and register admin routes

**Files:**
- Modify: `web/handlers/handlers.go:43-64`
- Modify: `web/web.go:191-207`

- [ ] **Step 1: Add `Admin` field to `HandlerGroup`**

In `web/handlers/handlers.go`, add the `Admin` field to the struct and constructor:

```go
// HandlerGroup groups all handler categories for routing setup.
type HandlerGroup struct {
	Web         *WebHandlers
	API         *APIHandlers
	APIKey      *APIKeyHandlers
	Webhook     *WebhookHandlers
	Billing     *BillingHandlers
	Integration *IntegrationHandler
	Version     *VersionHandler
	Admin       *AdminHandlers
}

// NewHandlerGroup constructs a HandlerGroup with initialized handlers.
func NewHandlerGroup(deps Dependencies) *HandlerGroup {
	return &HandlerGroup{
		Web:         &WebHandlers{Deps: deps},
		API:         &APIHandlers{Deps: deps},
		APIKey:      &APIKeyHandlers{Deps: deps},
		Webhook:     &WebhookHandlers{Deps: deps},
		Billing:     &BillingHandlers{Deps: deps},
		Integration: NewIntegrationHandler(deps.IntegrationRepo, deps.Encryptor, deps.App, deps.GoogleSheetsSvc),
		Version:     NewVersionHandler(),
		Admin:       &AdminHandlers{Deps: deps},
	}
}
```

- [ ] **Step 2: Register admin routes in `web.go`**

In `web/web.go`, after the authenticated API router setup (~line 235, after the webhook routes block), add the admin subrouter:

```go
	// ─── Admin routes ────────────────────────────────────────────────────
	// Admin routes are isolated in their own namespace and protected by
	// RequireRole middleware. API key auth is implicitly blocked because
	// admin users should only access these via Clerk JWT sessions.
	adminRouter := apiRouter.PathPrefix("/admin").Subrouter()
	adminRouter.Use(webmiddleware.RequireRole(models.RoleAdmin))

	adminRouter.HandleFunc("/jobs", hg.Admin.CreateJob).Methods(http.MethodPost)
	adminRouter.HandleFunc("/jobs", hg.Admin.GetJobs).Methods(http.MethodGet)
	adminRouter.HandleFunc("/jobs/{id}/cancel", hg.Admin.CancelJob).Methods(http.MethodPost)
```

Also add the `models` import if not already present (it should be already imported via other usages, but verify).

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go build ./...`
Expected: builds cleanly.

- [ ] **Step 4: Run all tests**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go test ./web/... -v -count=1 -timeout 60s`
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add web/handlers/handlers.go web/web.go
git commit -m "feat: register admin routes at /api/v1/admin/* with RequireRole middleware"
```

---

## Chunk 3: Billing Bypass & Admin Promotion

### Task 6: Skip ALL billing for admin-sourced jobs

**Files:**
- Modify: `runner/webrunner/webrunner.go:593-606` (ChargeActorStart at job start)
- Modify: `runner/webrunner/webrunner.go:988-1015` (ChargeAllJobEvents at job completion)

**Context:** There are TWO billing charge points in the webrunner:
1. **`ChargeActorStart`** (~line 593) — charges a flat fee when a job begins execution. If the user has zero credits, the job **fails immediately** with "insufficient credit balance to start job". This MUST be skipped for admin jobs or they will never run.
2. **`ChargeAllJobEvents`** (~line 990) — charges for places, reviews, images, and contacts after job completion.

Both must be bypassed for admin-sourced jobs.

- [ ] **Step 1a: Skip `ChargeActorStart` for admin jobs**

In `runner/webrunner/webrunner.go`, find the `ChargeActorStart` block (~line 592-606). Change:

```go
	// Charge actor_start at job start (requires sufficient balance)
	if w.billingSvc != nil {
```

To:

```go
	// Charge actor_start at job start (requires sufficient balance).
	// Admin jobs bypass billing entirely — they are internal operations.
	if w.billingSvc != nil && job.Source != models.SourceAdmin {
```

And after the closing `}` of the else block (~line 606), add:

```go
	if job.Source == models.SourceAdmin {
		w.logger.Info("actor_start_charge_skipped_admin_job",
			slog.String("job_id", job.ID),
			slog.String("user_id", job.UserID),
		)
	}
```

- [ ] **Step 1b: Modify the completion billing charge section**

In `runner/webrunner/webrunner.go`, find the billing section (~line 988-1015). The current code checks `w.billingSvc != nil && resultCount > 0`. Add a check for admin source:

Replace the billing block:

```go
			if w.billingSvc != nil && resultCount > 0 {
```

With:

```go
			if w.billingSvc != nil && resultCount > 0 && job.Source != models.SourceAdmin {
```

And add a log line for admin jobs right after:

```go
			if job.Source == models.SourceAdmin {
				w.logger.Info("billing_skipped_admin_job",
					slog.String("job_id", job.ID),
					slog.String("user_id", job.UserID),
					slog.Int("result_count", resultCount),
				)
			}
```

The full modified block should look like:

```go
			if w.billingSvc != nil && resultCount > 0 && job.Source != models.SourceAdmin {
				// Charge ALL events in a single atomic transaction
				// ... (existing charging code unchanged)
			} else {
				if job.Source == models.SourceAdmin {
					w.logger.Info("billing_skipped_admin_job",
						slog.String("job_id", job.ID),
						slog.String("user_id", job.UserID),
						slog.Int("result_count", resultCount),
					)
				}
				if w.billingSvc == nil {
					w.logger.Warn("billing_service_nil_skipping_charges", slog.String("job_id", job.ID))
				}
				if resultCount == 0 {
					w.logger.Warn("result_count_zero_skipping_charges", slog.String("job_id", job.ID))
				}
			}
```

Note: The webrunner imports both `models` (directly) and `web` (the web package). Status constants like `StatusFailed` are re-exported via `web/job.go`, but Source constants are NOT re-exported — they only exist in `models/job.go`. Since `models` is already imported directly in the webrunner (`import "github.com/gosom/google-maps-scraper/models"` at line 24), use `models.SourceAdmin`. Do NOT use `web.SourceAdmin` — it doesn't exist and will cause a compile error.

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go build ./...`
Expected: builds cleanly.

- [ ] **Step 3: Commit**

```bash
git add runner/webrunner/webrunner.go
git commit -m "feat: skip billing for admin-sourced jobs"
```

---

### Task 7: Create admin promotion script

**Files:**
- Create: `scripts/promote_admin.sh`

**Context:** There is no API to change user roles — this is intentional. Admin promotion requires direct database access, keeping the promotion path narrow and auditable. This script is meant to be run by an operator with DB credentials.

- [ ] **Step 1: Create the promotion script**

```bash
#!/usr/bin/env bash
#
# promote_admin.sh — Promote a user to admin role.
#
# Usage:
#   ./scripts/promote_admin.sh <user_id_or_email>
#
# Requires:
#   - DSN environment variable (PostgreSQL connection string)
#   - psql client
#
# Security:
#   - This script requires direct database access. There is no API
#     endpoint to change roles — this is intentional (CWE-269).
#   - Always verify the user identity before promoting.

set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 <user_id_or_email>"
    echo ""
    echo "Examples:"
    echo "  $0 user_2abc123def456    # by Clerk user ID"
    echo "  $0 admin@brezel.ai       # by email address"
    exit 1
fi

if [ -z "${DSN:-}" ]; then
    echo "Error: DSN environment variable is required."
    echo "Example: DSN='postgres://user:pass@host:5432/dbname?sslmode=require'"
    exit 1
fi

IDENTIFIER="$1"

# Input validation — ALLOWLIST approach (not denylist).
# Only permit characters that are valid in Clerk user IDs and email addresses.
# This prevents SQL injection regardless of PostgreSQL quoting tricks.
if ! echo "$IDENTIFIER" | grep -qE '^[a-zA-Z0-9@._+\-]+$'; then
    echo "Error: Invalid characters in identifier."
    echo "Only alphanumeric characters, @, ., _, +, and - are allowed."
    exit 1
fi

# Determine if input is an email or user ID and use psql variables
# for safe parameterization (no string interpolation into SQL).
if echo "$IDENTIFIER" | grep -q '@'; then
    LOOKUP_COLUMN="email"
    DISPLAY="email=$IDENTIFIER"
else
    LOOKUP_COLUMN="id"
    DISPLAY="id=$IDENTIFIER"
fi

echo "Looking up user ($DISPLAY)..."

# Show current user info before making changes.
# Uses psql \gset to bind the identifier safely via a psql variable.
CURRENT=$(psql "$DSN" -t -A -c "SELECT id, email, role FROM users WHERE $LOOKUP_COLUMN = \$\$${IDENTIFIER}\$\$" 2>/dev/null)
if [ -z "$CURRENT" ]; then
    echo "Error: No user found with $DISPLAY"
    exit 1
fi

echo "Current record: $CURRENT"
echo ""

# Check if already admin
CURRENT_ROLE=$(echo "$CURRENT" | cut -d'|' -f3)
if [ "$CURRENT_ROLE" = "admin" ]; then
    echo "User is already an admin. No changes made."
    exit 0
fi

read -p "Promote this user to admin? [y/N] " -n 1 -r
echo ""
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 0
fi

# Promote and set higher concurrent job limit.
# Uses dollar-quoting for the identifier (safe after allowlist validation).
psql "$DSN" -c "
    UPDATE users
    SET role = 'admin',
        max_concurrent_jobs = 50,
        updated_at = NOW()
    WHERE $LOOKUP_COLUMN = \$\$${IDENTIFIER}\$\$
    RETURNING id, email, role, max_concurrent_jobs;
"

echo ""
echo "Done. User promoted to admin with concurrent job limit of 50."
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend/scripts/promote_admin.sh`

- [ ] **Step 3: Commit**

```bash
git add scripts/promote_admin.sh
git commit -m "feat: add admin promotion script"
```

---

### Task 8: Verify complete integration

- [ ] **Step 1: Full build check**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go build ./...`
Expected: builds cleanly.

- [ ] **Step 2: Run all tests**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && go test ./... -count=1 -timeout 120s`
Expected: all tests pass.

- [ ] **Step 3: Verify route registration with a quick grep**

Run: `cd /Users/yasseen/Documents/brezel.ai/BrezelScraper/brezelscraper-backend && grep -n "admin" web/web.go`
Expected: shows the admin router and route registrations.

- [ ] **Step 4: Final commit (if any fixes needed)**

```bash
git add -A
git commit -m "feat: complete admin role system integration"
```

---

## Summary of Changes

| Component | What Changes | Security Impact |
|-----------|-------------|-----------------|
| `web/auth/auth.go` | Populates `UserRoleKey` in context after Clerk/API key auth | Enables role-based authorization |
| `web/auth/role.go` | `IsAdmin()` helper | Defense-in-depth role checks |
| `web/middleware/middleware.go` | TODO comment removed | Cleanup |
| `models/job.go` | `SourceAdmin = "admin"` constant | Enables admin job tagging |
| `web/handlers/admin.go` | Admin job create/list/cancel handlers | Bypasses credit system, defense-in-depth |
| `web/handlers/handlers.go` | `Admin` field in `HandlerGroup` | Wiring |
| `web/web.go` | `/api/v1/admin/*` routes with `RequireRole` | Route-level access control |
| `runner/webrunner/webrunner.go` | Skip `ChargeActorStart` AND `ChargeAllJobEvents` for admin source | Complete billing bypass (both job-start and job-completion charges) |
| `scripts/migrations/000030_*` | Add `'admin'` to `jobs.source` CHECK | DB constraint |
| `scripts/promote_admin.sh` | CLI tool for admin promotion | Narrow promotion path |

## What This Does NOT Include (Intentional Scope Limits)

1. **Admin user management API** — Admins cannot manage other users via API. This keeps the attack surface minimal.
2. **Admin API key access** — Admin routes require Clerk JWT only. Add in Phase 2 with IP allowlist.
3. **Relaxed job validation for admins** — Admin jobs use the same `max_results`, `depth`, and `keywords` limits. Admin users can create more concurrent jobs instead.
4. **Internal cost tracking for admin jobs** — Billing events are skipped entirely. Add tracking-without-charging in Phase 2 for internal accounting.
5. **Admin dashboard/UI** — Out of scope. Admin operations use the API directly (curl, Postman, scripts).

## Testing Checklist

- [ ] Unit tests pass: `go test ./web/auth/ -run TestIsAdmin`
- [ ] Unit tests pass: `go test ./web/handlers/ -run TestAdminCreate`
- [ ] Full test suite: `go test ./... -count=1`
- [ ] Build succeeds: `go build ./...`
- [ ] Manual test: promote a user to admin using the script
- [ ] Manual test: create a job via `POST /api/v1/admin/jobs` as admin user
- [ ] Manual test: verify non-admin gets 403 on admin routes
- [ ] Manual test: verify admin job completes without billing charges
