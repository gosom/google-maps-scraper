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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/web/auth"
	"golang.org/x/time/rate"
)

// retryAfterSec is the value placed in the Retry-After header on 429 responses.
const retryAfterSec = "1"

// rateLimitPolicyName is the policy label used in the modern IETF
// `RateLimit` / `RateLimit-Policy` structured-field headers (draft-10,
// Sept 2025). One label per request because we expose a single
// composite "API" policy to clients regardless of which internal bucket
// served them — future work can expand this to per-route policies.
const rateLimitPolicyName = "api"

// rateLimitCtxKey is the context key under which the dispatcher stashes
// the chosen bucket's snapshot for the RateLimitHeaders middleware to
// read. Private type to prevent collisions and keep the symbol unreachable
// from outside the package (no direct context.WithValue from callers).
type rateLimitCtxKey struct{}

// snapshotWithContext returns a new context with snap stored under
// rateLimitCtxKey. Used by the dispatcher in PerAPIKeyRateLimit.
func snapshotWithContext(ctx context.Context, snap LimiterSnapshot) context.Context {
	return context.WithValue(ctx, rateLimitCtxKey{}, snap)
}

// snapshotFromContext extracts the LimiterSnapshot stored by the
// dispatcher, returning ok=false when no limiter ran on this request
// (e.g. public routes). Callers should skip header emission in that case.
func snapshotFromContext(ctx context.Context) (LimiterSnapshot, bool) {
	s, ok := ctx.Value(rateLimitCtxKey{}).(LimiterSnapshot)
	return s, ok
}

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
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' cdn.redoc.ly cdnjs.cloudflare.com 'unsafe-inline' 'unsafe-eval'; "+
				"worker-src 'self' blob:; "+
				"style-src 'self' 'unsafe-inline' fonts.googleapis.com; "+
				"img-src 'self' data: cdn.redoc.ly; "+
				"font-src 'self' fonts.gstatic.com; "+
				"connect-src 'self'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
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
			attrs := []any{slog.String("request_id", GetRequestID(r.Context()))}
			// Add user_id if authenticated (may be empty for public endpoints).
			if userID, err := auth.GetUserID(r.Context()); err == nil && userID != "" {
				attrs = append(attrs, slog.String("user_id", userID))
			}
			child := log.With(attrs...)
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
					// Prefer the request-scoped logger (carries request_id, user_id)
					// over the root logger passed to Recovery().
					reqLog := pkglogger.FromContext(r.Context())
					reqLog.Error("panic_recovered",
						slog.Any("panic", rec),
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

// AllowCIDRs restricts access to requests whose source IP falls within one of
// the configured CIDR ranges. This is intended for provider callback endpoints
// such as Stripe webhooks. Parsing is done once at startup so each request only
// pays a small linear scan over the prevalidated networks.
func AllowCIDRs(cidrs []string) (func(http.Handler) http.Handler, error) {
	networks := make([]*net.IPNet, 0, len(cidrs))
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("parse CIDR %q: %w", raw, err)
		}
		networks = append(networks, network)
	}
	if len(networks) == 0 {
		return nil, fmt.Errorf("empty CIDR allowlist")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.RemoteAddr
			if parsedHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				host = parsedHost
			}
			ip := net.ParseIP(strings.TrimSpace(host))
			if ip == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    http.StatusForbidden,
					"message": "forbidden",
				})
				return
			}
			for _, network := range networks {
				if network.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    http.StatusForbidden,
				"message": "forbidden",
			})
		})
	}, nil
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

// RateLimitHeaders writes the IETF `RateLimit` / `RateLimit-Policy`
// structured-field headers (draft-ietf-httpapi-ratelimit-headers-10) and
// the legacy `X-RateLimit-Limit/-Remaining/-Reset` triplet on every
// response that travelled through a rate limiter in this package.
//
// Must be installed AFTER the limiter middleware in the chain so the
// snapshot is already in the request context by the time we run. Writes
// headers BEFORE calling next — Go buffers them until the downstream
// handler calls WriteHeader, so the limiter info goes out with the body.
//
// On the 429 path the dispatcher writes the same headers inline (see
// rateLimitJSON); this middleware exists only for the success path.
func RateLimitHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if snap, ok := snapshotFromContext(r.Context()); ok {
				writeRateLimitHeaders(w, snap)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// applyAndSnapshot is the standard "consume a token, then report state"
// shape used by every rate-limit middleware in this file. Centralising the
// pair means every limiter pathway emits a snapshot suitable for the
// RateLimit response headers, and removes the risk of "we counted but
// didn't snapshot" drift between sites. Locks the bucket twice (Allow,
// Tokens) — at the per-request frequencies we operate at the contention
// is irrelevant; benchmark before optimising.
func applyAndSnapshot(krl *keyRateLimiter, key string) (bool, LimiterSnapshot) {
	allowed := krl.get(key).Allow()
	return allowed, krl.Snapshot(key)
}

// LimiterSnapshot is the rate-limit state at a point in time, suitable for
// rendering as RateLimit / X-RateLimit-* response headers. Limit and Burst
// describe the policy (constants for the lifetime of the limiter); Remaining
// is the integer floor of tokens currently in the bucket; ResetSeconds is
// the ceiling of seconds until a fully-drained bucket would refill to Burst.
// Zero value is fine and is treated by RateLimitHeaders as "no policy"
// (skips emission) — that's the right behaviour for routes without a
// limiter in the chain.
type LimiterSnapshot struct {
	Limit        rate.Limit
	Burst        int
	Remaining    int
	ResetSeconds int
}

// Snapshot returns the current state of the bucket for `key` WITHOUT
// consuming a token. Safe to call from a response-header writer that runs
// after the request has been admitted (or rejected) by Allow().
//
// rate.Limiter.Tokens() is the source of truth; we floor it for "remaining"
// because tokens can be fractional during refill, but clients want a
// whole-number budget.
func (krl *keyRateLimiter) Snapshot(key string) LimiterSnapshot {
	lim := krl.get(key)
	tokens := lim.Tokens()
	remaining := int(tokens) // truncation toward zero
	if remaining < 0 {
		remaining = 0
	}
	resetSeconds := 0
	if krl.r > 0 {
		deficit := float64(krl.b) - tokens
		if deficit > 0 {
			// Ceil(deficit/rate) without importing math: integer-divide
			// after a +near-1 nudge that keeps exact integers stable.
			resetSeconds = int(deficit/float64(krl.r) + 0.9999)
			if resetSeconds < 1 {
				resetSeconds = 1
			}
		}
	}
	return LimiterSnapshot{
		Limit:        krl.r,
		Burst:        krl.b,
		Remaining:    remaining,
		ResetSeconds: resetSeconds,
	}
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

// writeRateLimitHeaders emits the IETF `RateLimit` / `RateLimit-Policy`
// structured-field headers (draft-ietf-httpapi-ratelimit-headers-10) plus
// the legacy `X-RateLimit-Limit` / `-Remaining` / `-Reset` triplet that
// most HTTP client libraries still parse natively. Called both from the
// 429 path (inline, before WriteHeader) and from the success path via the
// RateLimitHeaders middleware. Zero-snapshot means "no policy ran" — skip.
func writeRateLimitHeaders(w http.ResponseWriter, s LimiterSnapshot) {
	if s.Limit == 0 && s.Burst == 0 {
		return
	}
	// Window = the time it takes a fully-drained bucket to refill to Burst.
	// Ceiling division; minimum 1 so we never emit "w=0" (clients would
	// divide by it). `win` rather than `w` to avoid shadowing the
	// http.ResponseWriter parameter — easy to introduce when the
	// header-write lines below get moved or wrapped.
	window := 1
	if s.Limit > 0 {
		win := int(float64(s.Burst)/float64(s.Limit) + 0.9999)
		if win > window {
			window = win
		}
	}
	// The policy name is emitted as a bare structured-field key/token, not
	// as a quoted string. RFC 8941 §3.2 says a Dictionary member-name is a
	// `key` (lcalpha-leading), and `sf-item`s in a List use the same token
	// shape by convention. The IETF rate-limit draft's own examples are
	// `default;q=100;w=10` and `default;r=50;t=30` — no quotes.
	policy := fmt.Sprintf("%s;q=%d;w=%d", rateLimitPolicyName, s.Burst, window)
	current := fmt.Sprintf("%s;r=%d;t=%d", rateLimitPolicyName, s.Remaining, s.ResetSeconds)
	w.Header().Set("RateLimit-Policy", policy)
	w.Header().Set("RateLimit", current)
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(s.Burst))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(s.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Duration(s.ResetSeconds)*time.Second).Unix(), 10))
}

// rateLimitJSON writes a 429 JSON response with a Retry-After header
// and the RateLimit-* family so the client knows when to retry without
// guessing. snap is the post-Allow snapshot of the bucket that just
// denied the request — pass the zero value on the rare paths where a
// snapshot is unavailable; the headers will be skipped.
func rateLimitJSON(w http.ResponseWriter, snap LimiterSnapshot) {
	writeRateLimitHeaders(w, snap)
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
		"message": fmt.Sprintf("concurrent job limit reached (%d active jobs)", limit),
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
			allowed, snap := applyAndSnapshot(krl, ip)
			if !allowed {
				slog.Warn("rate_limit_exceeded",
					slog.String("key", ip),
					slog.String("path", r.URL.Path),
					slog.String("method", r.Method),
				)
				rateLimitJSON(w, snap)
				return
			}
			next.ServeHTTP(w, r.WithContext(snapshotWithContext(r.Context(), snap)))
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
			allowed, snap := applyAndSnapshot(krl, key)
			if !allowed {
				slog.Warn("rate_limit_exceeded",
					slog.String("key", key),
					slog.String("path", req.URL.Path),
					slog.String("method", req.Method),
				)
				rateLimitJSON(w, snap)
				return
			}
			next.ServeHTTP(w, req.WithContext(snapshotWithContext(req.Context(), snap)))
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
				// Tier comes from the auth middleware, which loads the user row and
				// stashes models.UserTierFree / models.UserTierPaid in the context
				// (see web/auth/auth.go withUserContext). A missing or empty value
				// falls back to free — the safe default for an authorisation
				// decision: a malformed tier must never grant the looser bucket.
				tier := auth.GetUserTier(req.Context())
				krl := freeKRL
				if tier == models.UserTierPaid {
					krl = paidKRL
				}
				allowed, snap := applyAndSnapshot(krl, apiKeyID)
				if !allowed {
					// tier is included so ops can debug "is paid user X actually
					// getting paid quotas?" from log aggregation alone, and so a
					// future "paid users hitting the free bucket" alert is easy
					// to wire up — the lack of this signal is what let the
					// "tier never propagated" bug live for as long as it did.
					slog.Warn("rate_limit_exceeded",
						slog.String("key", apiKeyID),
						slog.String("tier", tier),
						slog.String("path", req.URL.Path),
						slog.String("method", req.Method),
					)
					rateLimitJSON(w, snap)
					return
				}
				next.ServeHTTP(w, req.WithContext(snapshotWithContext(req.Context(), snap)))
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
			allowed, snap := applyAndSnapshot(fallbackKRL, key)
			if !allowed {
				slog.Warn("rate_limit_exceeded",
					slog.String("key", key),
					slog.String("path", req.URL.Path),
					slog.String("method", req.Method),
				)
				rateLimitJSON(w, snap)
				return
			}
			next.ServeHTTP(w, req.WithContext(snapshotWithContext(req.Context(), snap)))
		})
	}
}
