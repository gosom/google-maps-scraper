package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// Chain applies middlewares in order to a handler.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// CORS replicates the existing CORS behavior.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Allow localhost origins for development
		if origin == "http://localhost:3000" || origin == "http://localhost:3001" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			// For production, you should set specific allowed origins
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
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
