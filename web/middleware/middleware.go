package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/web/auth"
	"golang.org/x/time/rate"
)

// retryAfterSec is the value placed in the Retry-After header on 429 responses.
const retryAfterSec = "1"

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

// Chain applies middlewares in order to a handler.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// NewCORS returns a CORS middleware that only reflects origins present in
// allowedOrigins. If allowedOrigins is empty, it defaults to localhost only.
// The "null" origin (used by sandboxed iframes) is never allowed.
func NewCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"http://localhost:3000", "http://localhost:3001"}
	}
	allowSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowSet[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// Never reflect "null" origin (sandboxed iframes attack vector).
			if origin != "" && origin != "null" {
				if _, ok := allowSet[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					// Vary must be set so caches don't serve the wrong origin's response.
					w.Header().Add("Vary", "Origin")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets the same headers as the monolith.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' cdn.redoc.ly cdnjs.cloudflare.com 'unsafe-inline' 'unsafe-eval'; "+
				"worker-src 'self' blob:; "+
				"style-src 'self' 'unsafe-inline' fonts.googleapis.com; "+
				"img-src 'self' data: cdn.redoc.ly; "+
				"font-src 'self' fonts.gstatic.com; "+
				"connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Request ID
// ---------------------------------------------------------------------------

type requestIDKey struct{}

// GetRequestID extracts the request ID from the context.
// Returns an empty string if no request ID is present.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// RequestID generates a UUID, stores it in context, and sets the
// X-Request-ID response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.New().String()
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---------------------------------------------------------------------------
// Request Logger
// ---------------------------------------------------------------------------

// RequestLogger logs method, path, status, duration, userID, and request_id.
// It is a factory that returns a middleware so callers can inject their logger.
type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}

func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			lrw := &loggingResponseWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(lrw, r)
			dur := time.Since(start)

			userID, _ := auth.GetUserID(r.Context())
			requestID := GetRequestID(r.Context())

			log.Info("http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", lrw.status),
				slog.String("duration", dur.String()),
				slog.String("user_id", userID),
				slog.String("request_id", requestID),
			)
		})
	}
}

// ---------------------------------------------------------------------------
// Inject Logger
// ---------------------------------------------------------------------------

// InjectLogger creates a child logger with request_id and stores it in context
// via pkg/logger.WithContext, so downstream handlers can retrieve it with
// pkglogger.FromContext(ctx).
func InjectLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			child := log.With(slog.String("request_id", GetRequestID(r.Context())))
			ctx := pkglogger.WithContext(r.Context(), child)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---------------------------------------------------------------------------
// Recovery
// ---------------------------------------------------------------------------

// Recovery catches panics from downstream handlers, logs the panic value and
// stack trace at ERROR level, and writes a 500 JSON response. It re-panics
// for http.ErrAbortHandler so that the net/http server can perform proper
// connection hijack cleanup.
func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// Re-panic for ErrAbortHandler — the stdlib uses this to
					// signal that the connection should be closed without writing
					// a response.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}

					stack := debug.Stack()
					log.Error("panic recovered",
						slog.String("panic", fmt.Sprintf("%v", rec)),
						slog.String("stack", string(stack)),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"code":    500,
						"message": "internal server error",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// Body Size Limiting (CWE-400)
// ---------------------------------------------------------------------------

// MaxBodySize wraps r.Body with http.MaxBytesReader so requests with bodies
// exceeding maxBytes are rejected before handlers read the body. If the limit
// is exceeded, http.MaxBytesReader causes json.Decoder / io.ReadAll to return
// an error annotated with http.MaxBytesError, which handlers should surface as
// a 413 response. Using this middleware prevents unbounded memory allocation
// from oversized request bodies.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// Rate Limiting (CWE-307)
// ---------------------------------------------------------------------------

// rateLimiterEntry holds a per-key rate limiter and a last-seen timestamp
// for eviction of idle entries.
type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// keyRateLimiter maintains a map of per-key token-bucket limiters. Idle
// entries (not seen within ttl) are cleaned up periodically to prevent
// unbounded map growth.
const maxRateLimitEntries = 50_000

type keyRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rateLimiterEntry
	r        rate.Limit // tokens per second
	b        int        // burst size
	ttl      time.Duration
}

func newKeyRateLimiter(r rate.Limit, b int, ttl time.Duration) *keyRateLimiter {
	krl := &keyRateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		r:        r,
		b:        b,
		ttl:      ttl,
	}
	go krl.cleanup()
	return krl
}

func (krl *keyRateLimiter) get(key string) *rate.Limiter {
	krl.mu.Lock()
	defer krl.mu.Unlock()
	e, ok := krl.limiters[key]
	if !ok {
		if len(krl.limiters) >= maxRateLimitEntries {
			krl.evictOldest()
		}
		e = &rateLimiterEntry{limiter: rate.NewLimiter(krl.r, krl.b)}
		krl.limiters[key] = e
	}
	e.lastSeen = time.Now()
	return e.limiter
}

// evictOldest removes the oldest 20% of entries by lastSeen time.
// Must be called with krl.mu held.
func (krl *keyRateLimiter) evictOldest() {
	n := len(krl.limiters)
	if n == 0 {
		return
	}
	type keyed struct {
		key      string
		lastSeen time.Time
	}
	entries := make([]keyed, 0, n)
	for k, e := range krl.limiters {
		entries = append(entries, keyed{key: k, lastSeen: e.lastSeen})
	}
	slices.SortFunc(entries, func(a, b keyed) int {
		return a.lastSeen.Compare(b.lastSeen)
	})
	evictCount := max(n/5, 1)
	for i := 0; i < evictCount && i < len(entries); i++ {
		delete(krl.limiters, entries[i].key)
	}
}

func (krl *keyRateLimiter) cleanup() {
	ticker := time.NewTicker(krl.ttl)
	defer ticker.Stop()
	for range ticker.C {
		krl.mu.Lock()
		cutoff := time.Now().Add(-krl.ttl)
		for key, e := range krl.limiters {
			if e.lastSeen.Before(cutoff) {
				delete(krl.limiters, key)
			}
		}
		krl.mu.Unlock()
	}
}

// rateLimitJSON writes a 429 JSON response with a Retry-After header.
func rateLimitJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", retryAfterSec)
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    http.StatusTooManyRequests,
		"message": "too many requests",
	})
}

// concurrentLimitJSON writes a 429 JSON response for concurrent job limit exceeded.
func concurrentLimitJSON(w http.ResponseWriter, limit int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", retryAfterSec)
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    429,
		"message": "concurrent job limit reached",
		"limit":   limit,
	})
}

// PerIPRateLimit returns a middleware that limits requests per client IP.
// r is tokens per second; b is the burst size.
// Use for public/unauthenticated routes to slow down credential stuffing and
// sign-up farming.
func PerIPRateLimit(r rate.Limit, b int) func(http.Handler) http.Handler {
	krl := newKeyRateLimiter(r, b, 10*time.Minute)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			if !krl.get(ip).Allow() {
				rateLimitJSON(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PerUserRateLimit returns a middleware that limits requests per authenticated
// user ID. Falls back to per-IP if no user ID is in the context.
// r is tokens per second; b is the burst size.
// Use for authenticated routes to prevent job flooding and credit farming.
func PerUserRateLimit(r rate.Limit, b int) func(http.Handler) http.Handler {
	krl := newKeyRateLimiter(r, b, 10*time.Minute)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			key, _ := auth.GetUserID(req.Context())
			if key == "" {
				ip, _, err := net.SplitHostPort(req.RemoteAddr)
				if err != nil {
					ip = req.RemoteAddr
				}
				key = "ip:" + ip
			}
			if !krl.get(key).Allow() {
				rateLimitJSON(w)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// PerAPIKeyRateLimit returns a middleware that applies per-key rate limits for
// API key authenticated requests and falls back to per-user rate limiting for
// browser/session authenticated requests.
//
// Rate parameters:
//   - freeRate/freeBurst: free-tier API key limits (2 req/s, burst 5)
//   - paidRate/paidBurst: paid-tier API key limits (10 req/s, burst 30)
//   - fallbackRate/fallbackBurst: session-auth fallback (same as PerUserRateLimit)
//
// The bucket key is the API key UUID, ensuring each key has its own independent
// limit regardless of how many keys a user has.
func PerAPIKeyRateLimit(
	freeRate rate.Limit, freeBurst int,
	paidRate rate.Limit, paidBurst int,
	fallbackRate rate.Limit, fallbackBurst int,
) func(http.Handler) http.Handler {
	freeKRL := newKeyRateLimiter(freeRate, freeBurst, 10*time.Minute)
	paidKRL := newKeyRateLimiter(paidRate, paidBurst, 10*time.Minute)
	fallbackKRL := newKeyRateLimiter(fallbackRate, fallbackBurst, 10*time.Minute)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			apiKeyID := auth.GetAPIKeyID(req.Context())

			if apiKeyID != "" {
				// API key authenticated: apply tier-specific limit keyed on key UUID.
				tier := auth.GetAPIKeyPlanTier(req.Context())
				var allowed bool
				switch tier {
				case "paid":
					allowed = paidKRL.get(apiKeyID).Allow()
				default: // "free" or unset
					allowed = freeKRL.get(apiKeyID).Allow()
				}
				if !allowed {
					rateLimitJSON(w)
					return
				}
				next.ServeHTTP(w, req)
				return
			}

			// Session / cookie authenticated: fall back to per-user limiting.
			key, _ := auth.GetUserID(req.Context())
			if key == "" {
				ip, _, err := net.SplitHostPort(req.RemoteAddr)
				if err != nil {
					ip = req.RemoteAddr
				}
				key = "ip:" + ip
			}
			if !fallbackKRL.get(key).Allow() {
				rateLimitJSON(w)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}
