package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// Idempotency-Key support for billable POST endpoints (initially POST
// /api/v1/jobs). Implements the Stripe two-phase pattern so that
// concurrent network retries cannot double-bill a user:
//
//  1. Phase 1 — atomic reservation. INSERT a row with status='started'
//     using the UNIQUE (user_id, key) constraint as the lock. Only the
//     request that wins the insert is allowed to run the inner handler.
//  2. Phase 2 — replay or wait. Concurrent retries hit the conflict
//     branch, look up the existing row, and either replay the cached
//     response (if the first request has completed) or return 409
//     "in use" (if it's still in flight) so the client retries with
//     backoff.
//
// See web/middleware/idempotency_test.go for the concurrency test that
// pins this design — fire 20 goroutines at the same key, assert the
// inner handler runs exactly once and every caller observes either the
// cached response or a 409.

const (
	// IdempotencyHeader is the request header name. Matches Stripe.
	IdempotencyHeader = "Idempotency-Key"

	// idempotencyTTL is how long a completed row is replayable. 24h
	// matches Stripe and bounds the storage cost. The cleanup goroutine
	// reaps expired rows.
	idempotencyTTL = 24 * time.Hour

	// maxBodyForHash mirrors MaxBodySize so the request-hash computation
	// cannot be exploited as a CWE-400 amplifier. The +1 in the
	// io.LimitReader call below detects oversized bodies that the
	// MaxBodySize middleware will reject anyway.
	maxBodyForHash = 1 << 20

	// maxKeyLen caps the client-supplied key. 255 bytes is the Stripe
	// limit and the longest VARCHAR our schema considers reasonable.
	maxKeyLen = 255
)

// The models.IdempotencyRecord type, models.IdempotencyRepo interface, and
// models.ErrIdempotencyConflict sentinel live in the models package
// (models/idempotency.go) so both this middleware and the postgres
// repo can reference them without creating a postgres → middleware →
// auth → postgres import cycle. See models/idempotency.go for the
// type definitions.

// responseCapture is an http.ResponseWriter that mirrors every write to
// an inner buffer so the middleware can persist the exact bytes the
// client saw. We delegate Header() through to the wrapped writer so
// Content-Type and other headers set by the inner handler are preserved
// in the live response. The buffer is only used to populate
// response_body for the cached replay.
type responseCapture struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	buf         bytes.Buffer
}

func (r *responseCapture) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseCapture) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		// net/http promotes the first Write to a 200 OK if the handler
		// never called WriteHeader explicitly — mirror that here so the
		// captured status reflects what the client actually receives.
		r.WriteHeader(http.StatusOK)
	}
	// Best-effort capture: ignore the buf write result. If the buffer
	// fails (won't, with bytes.Buffer, but defensive), the live response
	// to the client must still succeed.
	r.buf.Write(p)
	return r.ResponseWriter.Write(p)
}

// Idempotency returns middleware that enforces Stripe-style two-phase
// idempotency on requests carrying the Idempotency-Key header. Requests
// without the header pass through unchanged — the middleware is opt-in
// per-request, not per-route.
//
// The middleware fails OPEN on storage errors that are not conflicts:
// if the database is unreachable, requests still reach the inner
// handler (without idempotency protection) rather than hard-failing
// legitimate traffic on a transient outage. Conflict and lookup errors
// are still returned to the client because they indicate an actual
// in-progress or duplicate request.
func Idempotency(repo models.IdempotencyRepo, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get(IdempotencyHeader))
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > maxKeyLen {
				writeIdempotencyError(w, http.StatusBadRequest, "Idempotency-Key exceeds maximum length")
				return
			}
			userID, err := auth.GetUserID(r.Context())
			if err != nil || userID == "" {
				// No authenticated user — idempotency is moot. Pass
				// through and let the route's auth middleware return
				// 401 if it requires auth.
				next.ServeHTTP(w, r)
				return
			}

			// Buffer and hash the body so we can both compare on replay
			// AND hand a fresh reader to the inner handler. The
			// LimitReader uses maxBodyForHash+1 so a body that exceeds
			// the cap is detectable here — the MaxBodySize middleware
			// will reject it before the handler runs anyway, so we
			// fall through to let it produce the canonical 413/422.
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyForHash+1))
			if err != nil || len(body) > maxBodyForHash {
				next.ServeHTTP(w, r)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			sum := sha256.Sum256(body)
			hash := hex.EncodeToString(sum[:])

			// Phase 1 — atomic reservation.
			now := time.Now().UTC()
			rec := models.IdempotencyRecord{
				ID:          uuid.Must(uuid.NewV7()).String(),
				UserID:      userID,
				Key:         key,
				Method:      r.Method,
				Path:        r.URL.Path,
				RequestHash: hash,
				Status:      "started",
				CreatedAt:   now,
				ExpiresAt:   now.Add(idempotencyTTL),
			}
			err = repo.InsertStarted(r.Context(), rec)
			if err == nil {
				// We own the key. Run the handler, capture the response,
				// then mark it completed. Use r.Context() (not Background)
				// so a client disconnect cancels the Complete call too —
				// the cleanup job will reap the stuck 'started' row,
				// which is safer than persisting a row out of sync with
				// what the client actually saw.
				rw := &responseCapture{ResponseWriter: w, status: http.StatusOK}
				next.ServeHTTP(rw, r)
				if completeErr := repo.Complete(r.Context(), rec.ID, rw.status, rw.buf.Bytes()); completeErr != nil {
					logger.Warn("idempotency_complete_failed",
						slog.String("user_id", userID),
						slog.String("key", key),
						slog.String("record_id", rec.ID),
						slog.Any("error", completeErr),
					)
				}
				return
			}
			if !errors.Is(err, models.ErrIdempotencyConflict) {
				// Real storage error — fail open. Log it and let the
				// inner handler run unidempotent rather than block
				// legitimate traffic on a transient repo outage.
				logger.Warn("idempotency_insert_failed_failing_open",
					slog.String("user_id", userID),
					slog.String("key", key),
					slog.Any("error", err),
				)
				next.ServeHTTP(w, r)
				return
			}

			// Phase 2 — conflict branch. Someone else owns the key.
			existing, err := repo.Get(r.Context(), userID, key)
			if err != nil {
				logger.Error("idempotency_lookup_failed",
					slog.String("user_id", userID),
					slog.String("key", key),
					slog.Any("error", err),
				)
				writeIdempotencyError(w, http.StatusInternalServerError, "idempotency lookup failed")
				return
			}
			if existing == nil {
				// Race: the conflicting row was deleted between the
				// failed insert and the lookup (cleanup goroutine, or
				// ON DELETE CASCADE from a user delete). Retry as a
				// fresh request.
				next.ServeHTTP(w, r)
				return
			}
			if existing.RequestHash != hash {
				// Same key, different body — client programming error.
				// Stripe responds with 409 here too. Don't leak the
				// hash itself.
				writeIdempotencyError(w, http.StatusConflict, "idempotency_key_in_use_with_different_body")
				return
			}
			if existing.Status == "started" {
				// First request still in flight. Tell the client to
				// back off; their next retry will likely see the
				// completed response.
				w.Header().Set("Retry-After", retryAfterSec)
				writeIdempotencyError(w, http.StatusConflict, "idempotency_key_in_use")
				return
			}
			// Completed — replay the cached response. The original
			// Content-Type is not preserved cross-process, so set it
			// from the JSON convention used everywhere in this API.
			// (If the original handler returned a different
			// Content-Type, the cached body still goes through; only
			// the header value differs from the original.)
			if existing.StatusCode == 0 {
				existing.StatusCode = http.StatusOK
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(existing.StatusCode)
			_, _ = w.Write(existing.ResponseBody)
		})
	}
}

// writeIdempotencyError writes a JSON error in the same shape the rest
// of the API uses (`{"code":N,"message":"..."}`), keeping the surface
// consistent.
func writeIdempotencyError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body := `{"code":` + itoa(code) + `,"message":` + quoteJSON(msg) + `}`
	_, _ = w.Write([]byte(body))
}

// itoa avoids pulling in strconv just for the 3-digit status code
// formatting in the JSON error path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// quoteJSON does the minimum JSON-string escaping needed for our static
// error messages (no user input flows into msg). Avoids dragging in
// encoding/json for a fixed set of literals.
func quoteJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RunIdempotencyCleanup runs a periodic cleanup loop in a goroutine.
// Cancel ctx to stop it. The interval is the period between sweeps;
// stuckGrace is how long a 'started' row can sit before being treated
// as abandoned (recommended: 15 * time.Minute).
//
// The function is intentionally a goroutine driver, not a cron entry —
// the web server already runs in a single process and a time.Ticker
// in a goroutine is the simplest scheduling primitive that doesn't
// add a new dependency. If we ever scale to multiple replicas, the
// cleanup work is idempotent (DELETE WHERE conditions) so concurrent
// runs are safe.
func RunIdempotencyCleanup(ctx context.Context, repo models.IdempotencyRepo, interval, stuckGrace time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			completed, started, err := repo.CleanupExpired(ctx, stuckGrace)
			if err != nil {
				logger.Warn("idempotency_cleanup_failed", slog.Any("error", err))
				continue
			}
			if completed > 0 || started > 0 {
				logger.Info("idempotency_cleanup_swept",
					slog.Int64("completed_deleted", completed),
					slog.Int64("started_deleted", started),
				)
			}
		}
	}
}
