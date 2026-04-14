# Support Email Endpoint — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `POST /api/v1/support` endpoint to the Go backend that receives support requests from the dashboard, enriches them with server-side user data (email, userId, creditBalance), and sends an email to `support@brezel.ai` via Resend.

**Architecture:** New `SupportHandlers` handler following the existing pattern (`Dependencies` struct, `decodeStrict`, `validate.Struct`, `renderJSON`). A thin `notify` package provides a `Sender` interface with a Resend HTTP implementation and a log-only fallback for dev. The handler enriches the request with Clerk user data (fetched from the `users` DB table, never trusted from the client), validates and sanitizes all input, then delegates to the sender. Per-user rate limiting prevents abuse.

**Tech Stack:** Go, Gorilla Mux, `go-playground/validator`, Resend REST API (plain `net/http` — no SDK), `log/slog`

---

## Security Analysis (golang-security)

### Trust Boundaries

| Boundary | What crosses it | Risk |
|----------|----------------|------|
| HTTP request body | `category`, `subject`, `message` from authenticated user | Injection, abuse |
| Email system (Resend API) | Constructed email with user-controlled subject/body | Email header injection, spam relay |
| Logs | User-provided message content | Log injection |

### Threat Model (STRIDE)

| Threat | Attack | Defense | DREAD |
|--------|--------|---------|-------|
| **Tampering** — Email header injection | Newlines in `subject` field → inject CC/BCC headers | Strip `\r`, `\n`, `\x00` from subject; Resend API uses JSON (not raw SMTP), so header injection is structurally impossible, but sanitize anyway for defense in depth | 5 (Medium) |
| **Denial of Service** — Support spam flood | Authenticated user submits thousands of requests | Per-user rate limit: 1 req/min, burst 2 | 6 (High) |
| **Information Disclosure** — PII in logs | Message contains passwords, API keys, personal data | Log only category + userId + request_id, NEVER log message content | 5 (Medium) |
| **Spoofing** — Forge userId/email | Attacker sends fake userId in request body | All user data fetched server-side from DB using auth context; request body only carries category/subject/message | 7 (High) |
| **Elevation of Privilege** — Unauthenticated access | Anonymous user hits endpoint | Endpoint behind `authMiddleware.Authenticate` on `apiRouter` | 2 (Low) |

### Input Validation Rules

| Field | Type | Validation | Sanitization |
|-------|------|------------|--------------|
| `category` | string | `required,oneof=bug feature billing account other` | Enum allowlist — no free text |
| `subject` | string | `max=200` (optional) | Strip control chars (`\r\n\t\x00`) |
| `message` | string | `required,min=10,max=5000` | Trim whitespace; log only length, not content |

### Email Safety

The Resend API accepts JSON payloads and constructs SMTP internally — the attacker never touches raw SMTP headers. This structurally prevents header injection (unlike `net/smtp` where subject goes into raw headers). We still sanitize subject for defense in depth and to prevent log injection.

The email body is sent as **plain text only** (no HTML). This eliminates any XSS-in-email risk.

---

## File Structure

### New files:
- `pkg/notify/sender.go` — `Sender` interface + `SupportRequest` model
- `pkg/notify/resend.go` — Resend REST API implementation
- `pkg/notify/log.go` — Log-only fallback for dev (no RESEND_API_KEY)
- `pkg/notify/resend_test.go` — Unit tests (HTTP round-tripper mock)
- `web/handlers/support.go` — `SupportHandlers` + `SubmitSupportRequest` handler
- `web/handlers/support_test.go` — Handler unit tests

### Modified files:
- `web/handlers/handlers.go` — Add `Support *SupportHandlers` to `HandlerGroup`
- `web/web.go` — Register `POST /api/v1/support` route with per-user rate limit
- `web/web.go` — Add `ResendAPIKey` to `ServerConfig`, construct Sender in `New()`
- `runner/webrunner/webrunner.go` — Pass `RESEND_API_KEY` env var into `ServerConfig`

---

## Chunk 1: Notification Sender Package

### Task 1: Create the Sender interface and SupportRequest model

**Files:**
- Create: `pkg/notify/sender.go`

- [ ] **Step 1: Create the interface and model**

```go
package notify

import "context"

// SupportRequest is the enriched payload sent to the notification backend.
// All fields are validated and sanitized before reaching this struct.
type SupportRequest struct {
	// User-provided (validated)
	Category string // one of: bug, feature, billing, account, other
	Subject  string // optional, max 200 chars, control chars stripped
	Message  string // required, 10-5000 chars

	// Server-enriched (never from client)
	UserID        string
	UserEmail     string
	CreditBalance string
	UserAgent     string
}

// Sender delivers support requests to an external system (email, ticket, Slack).
// Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, req SupportRequest) error
}
```

- [ ] **Step 2: Commit**

```bash
git add pkg/notify/sender.go
git commit -m "feat(support): add notify.Sender interface and SupportRequest model"
```

### Task 2: Implement Resend email sender

**Files:**
- Create: `pkg/notify/resend.go`
- Create: `pkg/notify/resend_test.go`

- [ ] **Step 1: Write the Resend implementation**

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResendSender sends support emails via the Resend REST API.
// It does NOT use the Resend Go SDK — just net/http + JSON,
// consistent with how the codebase calls Stripe's API.
type ResendSender struct {
	apiKey     string
	from       string
	to         string
	baseURL    string       // default "https://api.resend.com"; override in tests
	httpClient *http.Client
}

// NewResendSender creates a Resend email sender.
// apiKey: Resend API key (re_...)
// from: sender address (e.g., "BrezelScraper Support <noreply@brezel.ai>")
// to: recipient address (e.g., "support@brezel.ai")
func NewResendSender(apiKey, from, to string) *ResendSender {
	return &ResendSender{
		apiKey:  apiKey,
		from:    from,
		to:      to,
		baseURL: "https://api.resend.com",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	ReplyTo string   `json:"reply_to,omitempty"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

func (s *ResendSender) Send(ctx context.Context, req SupportRequest) error {
	body := fmt.Sprintf(
		"Category: %s\nSubject: %s\n\n%s\n\n---\nUser ID: %s\nEmail: %s\nCredit Balance: %s\nUser Agent: %s",
		req.Category, req.Subject, req.Message,
		req.UserID, req.UserEmail, req.CreditBalance, req.UserAgent,
	)

	payload := resendPayload{
		From:    s.from,
		To:      []string{s.to},
		ReplyTo: req.UserEmail,
		Subject: fmt.Sprintf("[Support: %s] %s", req.Category, req.Subject),
		Text:    body,
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/emails", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("notify: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("notify: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("notify: resend returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
```

**Security notes:**
- `io.LimitReader(resp.Body, 1024)` prevents unbounded read from Resend error responses (CWE-400)
- `http.Client{Timeout: 10s}` prevents hanging on unresponsive Resend API
- `http.NewRequestWithContext` ensures cancellation propagates from handler context
- Plain text only (no HTML) — eliminates XSS-in-email risk
- API key passed via `Authorization` header, never logged

- [ ] **Step 2: Write tests**

```go
package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResendSender_Send_Success(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected auth header, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected json content type")
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"email_123"}`))
	}))
	defer srv.Close()

	sender := NewResendSender("test-key", "from@test.com", "to@test.com")
	sender.httpClient = srv.Client()
	sender.baseURL = srv.URL

	err := sender.Send(context.Background(), SupportRequest{
		Category:  "bug",
		Subject:   "Test",
		Message:   "Something broke",
		UserID:    "user_123",
		UserEmail: "user@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = gotBody // verify payload structure if desired
}

func TestResendSender_Send_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_api_key"}`))
	}))
	defer srv.Close()

	sender := NewResendSender("bad-key", "from@test.com", "to@test.com")
	sender.baseURL = srv.URL

	err := sender.Send(context.Background(), SupportRequest{
		Category: "bug",
		Message:  "test message content",
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}
```

- [ ] **Step 3: Commit**

```bash
git add pkg/notify/
git commit -m "feat(support): implement Resend email sender with tests"
```

### Task 3: Implement log-only fallback sender

**Files:**
- Create: `pkg/notify/log.go`

- [ ] **Step 1: Create the dev-mode fallback**

```go
package notify

import (
	"context"
	"log/slog"
)

// LogSender writes support requests to the logger instead of sending email.
// Used when RESEND_API_KEY is not configured (local development).
type LogSender struct {
	Logger *slog.Logger
}

func (s *LogSender) Send(_ context.Context, req SupportRequest) error {
	s.Logger.Info("support_request_received",
		slog.String("category", req.Category),
		slog.String("subject", req.Subject),
		slog.String("user_id", req.UserID),
		slog.String("user_email", req.UserEmail),
		// security: NEVER log req.Message — may contain PII, passwords, API keys
		slog.Int("message_length", len(req.Message)),
	)
	return nil
}
```

**Security note:** `req.Message` is intentionally NOT logged — users may paste API keys, passwords, or personal data into support messages. Only log the length for debugging.

- [ ] **Step 2: Commit**

```bash
git add pkg/notify/log.go
git commit -m "feat(support): add log-only fallback sender for development"
```

---

## Chunk 2: Support Handler

### Task 4: Create the support handler

**Files:**
- Create: `web/handlers/support.go`

- [ ] **Step 1: Write the handler**

```go
package handlers

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/notify"
	"github.com/gosom/google-maps-scraper/web/auth"
	webservices "github.com/gosom/google-maps-scraper/web/services"
)

// SupportHandlers handles support-related API requests.
type SupportHandlers struct {
	Deps   Dependencies
	Sender notify.Sender
}

// supportRequest is the JSON body from the frontend.
// All user-identifying fields are fetched server-side — NEVER trust client-provided userId/email.
type supportRequest struct {
	Category string `json:"category" validate:"required,oneof=bug feature billing account other"`
	Subject  string `json:"subject"  validate:"max=200"`
	Message  string `json:"message"  validate:"required,min=10,max=5000"`
}

// sanitizeSubject strips control characters that could cause log injection
// or (in non-API email transports) header injection.
// Defense-in-depth: Resend's JSON API is structurally immune to header injection,
// but we sanitize anyway so the value is safe for any downstream use (logs, DB, future transports).
func sanitizeSubject(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\x00' || unicode.IsControl(r) {
			return -1 // strip
		}
		return r
	}, strings.TrimSpace(s))
}

// SubmitSupportRequest handles POST /api/v1/support.
func (h *SupportHandlers) SubmitSupportRequest(w http.ResponseWriter, r *http.Request) {
	// 1. Decode request body (strict: unknown fields rejected, trailing data rejected)
	var req supportRequest
	if err := decodeStrict(r, &req); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("support_decode_failed", slog.Any("error", err))
		}
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{
			Code:    http.StatusUnprocessableEntity,
			Message: "Invalid request body",
		})
		return
	}

	// 2. Validate with struct tags (enum allowlist on category, length limits)
	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{
			Code:    http.StatusBadRequest,
			Message: formatValidationErrors(err),
		})
		return
	}

	// 3. Extract authenticated user ID from context (set by auth middleware)
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{
			Code:    http.StatusUnauthorized,
			Message: "Authentication required",
		})
		return
	}

	// 4. Fetch user data server-side (NEVER trust client-provided email/balance)
	var userEmail, creditBalance string
	if h.Deps.UserRepo != nil {
		user, err := h.Deps.UserRepo.GetByID(r.Context(), userID)
		if err != nil {
			if h.Deps.Logger != nil {
				h.Deps.Logger.Warn("support_user_lookup_failed",
					slog.String("user_id", userID),
					slog.Any("error", err),
				)
			}
			// Non-fatal: send the ticket anyway, just without enrichment
			userEmail = "unknown"
		} else {
			userEmail = user.Email
		}
	}

	if h.Deps.DB != nil && h.Deps.BillingSvc != nil {
		cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc)
		resp, err := cs.GetBalance(r.Context(), userID)
		if err == nil {
			creditBalance = resp.CreditBalance
		}
	}

	// 5. Sanitize inputs
	req.Subject = sanitizeSubject(req.Subject)
	req.Message = strings.TrimSpace(req.Message)

	// 6. Build enriched support request
	supportReq := notify.SupportRequest{
		Category:      req.Category,
		Subject:       req.Subject,
		Message:       req.Message,
		UserID:        userID,
		UserEmail:     userEmail,
		CreditBalance: creditBalance,
		UserAgent:     r.UserAgent(),
	}

	// 7. Send via configured sender (Resend or log fallback)
	if err := h.Sender.Send(r.Context(), supportReq); err != nil {
		internalError(w, h.Deps.Logger, err, "Failed to send support request",
			slog.String("user_id", userID),
			slog.String("category", req.Category),
		)
		return
	}

	// 8. Log success (no message content — PII risk)
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("support_request_sent",
			slog.String("user_id", userID),
			slog.String("category", req.Category),
		)
	}

	renderJSON(w, http.StatusOK, map[string]bool{"success": true})
}
```

**Security checklist:**
- `decodeStrict` — rejects unknown fields, trailing data (parser-divergence defense)
- `validate.Struct` — enum allowlist on category (no arbitrary strings), length limits on all fields
- `auth.GetUserID(r.Context())` — user identity from auth middleware, not request body
- `sanitizeSubject` — strips control chars (log injection, header injection defense-in-depth)
- `internalError` — raw errors logged server-side, generic message to client
- Message content NEVER logged — PII risk
- `r.UserAgent()` — read from request headers, enrichment only

- [ ] **Step 2: Verify `BillingSvc.GetBalance` signature**

Before implementing, confirm the billing service's balance method signature. Run:
```bash
grep -n "GetBalance\|func.*BillingService\|func.*Service.*Balance" web/services/credit.go billing/*.go
```
Adapt the handler to match the actual method name and return type.

- [ ] **Step 3: Commit**

```bash
git add web/handlers/support.go
git commit -m "feat(support): add SubmitSupportRequest handler with input validation and sanitization"
```

### Task 5: Write handler tests

**Files:**
- Create: `web/handlers/support_test.go`

- [ ] **Step 1: Write tests covering security-critical paths**

Test cases to cover:

| Test | What it verifies |
|------|-----------------|
| Valid request → 200 | Happy path, sender called with enriched data |
| Missing category → 400 | Enum validation rejects empty |
| Invalid category → 400 | `oneof` rejects "hacking" |
| Empty message → 400 | `min=10` rejects short messages |
| Message > 5000 chars → 400 | `max=5000` rejects oversized |
| Subject with newlines → sanitized | Control chars stripped before send |
| Unknown JSON field → 422 | `decodeStrict` rejects extra fields |
| No auth → 401 | Handler rejects unauthenticated requests |
| Sender failure → 500 | Internal error, generic message to client |

Use a mock `Sender` (simple struct implementing the interface) to verify the handler passes correct data without actually sending email.

- [ ] **Step 2: Run tests**

```bash
go test ./web/handlers/ -run TestSupport -v
```

- [ ] **Step 3: Commit**

```bash
git add web/handlers/support_test.go
git commit -m "test(support): add handler tests covering validation, auth, sanitization"
```

---

## Chunk 3: Wiring — Config, Dependencies, Route Registration

### Task 6: Add config and wire dependencies

**Files:**
- Modify: `web/web.go` — Add `ResendAPIKey` to `ServerConfig`, construct Sender in `New()`, pass to Dependencies
- Modify: `web/handlers/handlers.go` — Add `Sender notify.Sender` to Dependencies, `Support` to HandlerGroup
- Modify: `runner/webrunner/webrunner.go` — Pass `RESEND_API_KEY` env var to `ServerConfig`

- [ ] **Step 1: Add ResendAPIKey to ServerConfig**

In `web/web.go`, add to the `ServerConfig` struct (around line 66):

```go
ResendAPIKey string // optional; if empty, support requests are logged instead of emailed
```

In `runner/webrunner/webrunner.go` where `ServerConfig` is assembled, add:

```go
ResendAPIKey: os.Getenv("RESEND_API_KEY"),
```

- [ ] **Step 2: Add Sender to Dependencies**

In `web/handlers/handlers.go`, add to `Dependencies`:

```go
Sender notify.Sender // nil-safe; handler checks before use
```

- [ ] **Step 3: Add Support to HandlerGroup**

In `web/handlers/handlers.go`, add to `HandlerGroup`:

```go
Support *SupportHandlers
```

In `NewHandlerGroup`, add:

```go
Support: &SupportHandlers{Deps: deps, Sender: deps.Sender},
```

- [ ] **Step 4: Construct Sender in web.go New()**

In `web/web.go` inside the `New()` function (around line 130, before `NewHandlerGroup` is called), add:

```go
// Support email sender: Resend if configured, log fallback otherwise
var supportSender notify.Sender
if cfg.ResendAPIKey != "" {
    supportSender = notify.NewResendSender(
        cfg.ResendAPIKey,
        "BrezelScraper Support <noreply@brezel.ai>",
        "support@brezel.ai",
    )
} else {
    supportSender = &notify.LogSender{Logger: ans.logger}
}
deps.Sender = supportSender
```

- [ ] **Step 5: Register route in web.go**

In `web/web.go`, after the existing `apiRouter` routes (around line 276), add:

```go
// Support endpoint: per-user rate limit of 1 req/min, burst 2
// Prevents support-form spam while allowing a quick retry after a typo fix.
supportLimiter := webmiddleware.PerUserRateLimit(rate.Limit(1.0/60.0), 2)
apiRouter.Handle("/support",
    supportLimiter(http.HandlerFunc(hg.Support.SubmitSupportRequest)),
).Methods(http.MethodPost)
```

**Rate limit rationale:** `rate.Limit(1.0/60.0)` = 1 request per 60 seconds. Burst 2 allows a quick correction (submit, realize typo, resubmit). This is tighter than the job creation limit (1/s burst 3) because support messages have no idempotency key and directly generate email.

- [ ] **Step 6: Run full test suite**

```bash
go test ./... -race -count=1
```

- [ ] **Step 7: Commit**

```bash
git add web/web.go web/handlers/handlers.go runner/webrunner/webrunner.go
git commit -m "feat(support): wire support endpoint with config, rate limiting, and sender injection"
```

---

## Chunk 4: Frontend API Route Cleanup

### Task 7: Update frontend to call backend instead of Next.js API route

**Files:**
- Modify: `brezelscraper-frontend/src/app/dashboard/support/ContactForm.tsx`
- Delete: `brezelscraper-frontend/src/app/api/support/route.ts`

- [ ] **Step 1: Update ContactForm to use the backend API client**

The frontend currently POSTs to `/api/support` (a Next.js API route). This should instead use the existing `useAPI()` hook to call the Go backend at `/api/v1/support`, which handles auth token injection automatically.

Replace the `fetch("/api/support", ...)` call with:

```tsx
const api = useAPI();

async function handleSubmit() {
  setSubmitting(true);
  try {
    await api.post("/api/v1/support", { category, subject, message });
    toast.success("Message sent", {
      description: "We'll get back to you within 24 hours.",
    });
    setCategory("");
    setSubject("");
    setMessage("");
  } catch {
    toast.error("Failed to send", {
      description: "Please try again or email support@brezel.ai directly.",
    });
  } finally {
    setSubmitting(false);
  }
}
```

This change:
- Automatically includes the Clerk JWT `Authorization: Bearer` header (via `apiClient`)
- Gets automatic retry on 401 (token refresh)
- Routes through the Go backend (not a Next.js API route)
- Removes the need for the Next.js `/api/support` route entirely

- [ ] **Step 2: Delete the Next.js API route**

Remove `brezelscraper-frontend/src/app/api/support/route.ts` — all logic is now in the Go backend.

- [ ] **Step 3: Commit**

```bash
git add brezelscraper-frontend/src/app/dashboard/support/ContactForm.tsx
git rm brezelscraper-frontend/src/app/api/support/route.ts
git commit -m "refactor(support): route support form through Go backend instead of Next.js API route"
```

---

## Summary

### Commits (7 total)

| # | Message | Scope |
|---|---------|-------|
| 1 | `feat(support): add notify.Sender interface and SupportRequest model` | `pkg/notify/sender.go` |
| 2 | `feat(support): implement Resend email sender with tests` | `pkg/notify/resend.go`, `resend_test.go` |
| 3 | `feat(support): add log-only fallback sender for development` | `pkg/notify/log.go` |
| 4 | `feat(support): add SubmitSupportRequest handler` | `web/handlers/support.go` |
| 5 | `test(support): add handler tests` | `web/handlers/support_test.go` |
| 6 | `feat(support): wire support endpoint with config and rate limiting` | `web/web.go`, `handlers.go`, `runner/webrunner/` |
| 7 | `refactor(support): route support form through Go backend` | Frontend `ContactForm.tsx`, delete API route |

### Environment Variables

| Variable | Required | Example | Purpose |
|----------|----------|---------|---------|
| `RESEND_API_KEY` | No | `re_123abc...` | Resend API key. If absent, support requests log to console (dev mode). |

### Future Expansion Points

The `notify.Sender` interface is the extension point. To add new notification channels:

| Channel | What to build | Effort |
|---------|--------------|--------|
| Slack | `pkg/notify/slack.go` implementing `Sender` | ~30 lines |
| Linear tickets | `pkg/notify/linear.go` implementing `Sender` | ~50 lines |
| Multi-channel fan-out | `pkg/notify/multi.go` that wraps `[]Sender` and calls all | ~15 lines |
| DB persistence | `pkg/notify/db.go` that stores requests in a `support_tickets` table | ~40 lines + migration |

Each is a new `Sender` implementation — no handler changes needed.
