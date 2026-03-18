package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gosom/google-maps-scraper/web/auth"
	"golang.org/x/time/rate"
)

func TestNewCORS_AllowedOrigin(t *testing.T) {
	handler := NewCORS([]string{"https://brezel.ai"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://brezel.ai")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://brezel.ai" {
		t.Errorf("expected ACAO=https://brezel.ai, got %q", got)
	}
}

func TestNewCORS_UnlistedOriginGetsNoHeader(t *testing.T) {
	handler := NewCORS([]string{"https://brezel.ai"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO header for unlisted origin, got %q", got)
	}
}

func TestNewCORS_NullOriginNotReflected(t *testing.T) {
	handler := NewCORS([]string{"null"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "null")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO header for null origin, got %q", got)
	}
}

func TestNewCORS_PreflightAllowedOrigin(t *testing.T) {
	handler := NewCORS([]string{"https://brezel.ai"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://brezel.ai")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://brezel.ai" {
		t.Errorf("expected ACAO=https://brezel.ai on preflight, got %q", got)
	}
}

func TestNewCORS_EmptyAllowedOriginsDefaultsToLocalhost(t *testing.T) {
	handler := NewCORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("expected ACAO=http://localhost:3000, got %q", got)
	}
}

func TestRecovery_PanicReturns500(t *testing.T) {
	logger := slog.Default()
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := Recovery(logger)(panicHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	// Must not crash the test process.
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if code, ok := body["code"].(float64); !ok || int(code) != 500 {
		t.Errorf("expected body.code=500, got %v", body["code"])
	}
	if msg, ok := body["message"].(string); !ok || msg != "internal server error" {
		t.Errorf("expected body.message='internal server error', got %v", body["message"])
	}
}

func TestRecovery_ErrAbortHandlerRepanics(t *testing.T) {
	logger := slog.Default()
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})

	handler := Recovery(logger)(panicHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Errorf("expected ErrAbortHandler to be re-panicked, got %v", rec)
		}
	}()

	handler.ServeHTTP(rr, req)
	t.Error("expected panic, but ServeHTTP returned normally")
}

func TestRecovery_NoPanicPassesThrough(t *testing.T) {
	logger := slog.Default()
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := Recovery(logger)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// PerIPRateLimit tests
// ---------------------------------------------------------------------------

func TestPerIPRateLimit_AllowsUnderLimit(t *testing.T) {
	// Generous limit so the single request is always allowed.
	handler := PerIPRateLimit(rate.Limit(100), 100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestPerIPRateLimit_Returns429WhenExceeded(t *testing.T) {
	// Zero-rate limiter: every request is denied.
	handler := PerIPRateLimit(rate.Limit(0), 0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode 429 body: %v", err)
	}
	if body["message"] != "too many requests" {
		t.Errorf("unexpected message: %v", body["message"])
	}
}

// ---------------------------------------------------------------------------
// PerAPIKeyRateLimit tests
// ---------------------------------------------------------------------------

// injectAPIKey adds API key context values so PerAPIKeyRateLimit treats the
// request as API-key-authenticated.
func injectAPIKey(r *http.Request, keyID, tier string) *http.Request {
	ctx := r.Context()
	ctx = context.WithValue(ctx, auth.APIKeyIDKey, keyID)
	ctx = context.WithValue(ctx, auth.APIKeyPlanTierKey, tier)
	ctx = context.WithValue(ctx, auth.UserIDKey, "user-123")
	return r.WithContext(ctx)
}

// injectSessionUser adds only a user ID (simulates browser/session auth).
func injectSessionUser(r *http.Request, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserIDKey, userID)
	return r.WithContext(ctx)
}

func TestPerAPIKeyRateLimit_FreeKeyAllowsUnderLimit(t *testing.T) {
	handler := PerAPIKeyRateLimit(
		rate.Limit(100), 100, // free: generous
		rate.Limit(100), 100, // paid: generous
		rate.Limit(100), 100, // fallback: generous
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	req = injectAPIKey(req, "key-uuid-free", "free")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestPerAPIKeyRateLimit_FreeKeyReturns429WhenExceeded(t *testing.T) {
	handler := PerAPIKeyRateLimit(
		rate.Limit(0), 0, // free: zero rate — always denied
		rate.Limit(100), 100,
		rate.Limit(100), 100,
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	req = injectAPIKey(req, "key-uuid-free", "free")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for free key, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
}

func TestPerAPIKeyRateLimit_PaidKeyUsesOwnBucket(t *testing.T) {
	handler := PerAPIKeyRateLimit(
		rate.Limit(0), 0,    // free: always denied
		rate.Limit(100), 100, // paid: generous
		rate.Limit(100), 100,
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	req = injectAPIKey(req, "key-uuid-paid", "paid")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for paid key (free rate should not affect paid bucket), got %d", rr.Code)
	}
}

func TestPerAPIKeyRateLimit_SessionAuthUseFallback_Allowed(t *testing.T) {
	handler := PerAPIKeyRateLimit(
		rate.Limit(0), 0,    // free: always denied (should not be used)
		rate.Limit(0), 0,    // paid: always denied (should not be used)
		rate.Limit(100), 100, // fallback: generous
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	req = injectSessionUser(req, "session-user-abc")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for session user via fallback, got %d", rr.Code)
	}
}

func TestPerAPIKeyRateLimit_SessionAuthUseFallback_Denied(t *testing.T) {
	handler := PerAPIKeyRateLimit(
		rate.Limit(100), 100,
		rate.Limit(100), 100,
		rate.Limit(0), 0, // fallback: always denied
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	req = injectSessionUser(req, "session-user-abc")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for session user via denied fallback, got %d", rr.Code)
	}
}
