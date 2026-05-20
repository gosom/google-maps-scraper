package webrunner

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gosom/google-maps-scraper/proxypool"
)

// newProxyStatsHandler returns an http.Handler that serves a credential-
// stripped JSON snapshot of the supplied proxy pool. Wired into the
// internal listener (127.0.0.1:9090) via web.ServerConfig.InternalHandlers
// — never exposed publicly.
//
// Returns 503 when called with a nil pool; the webrunner only registers
// the handler when len(cfg.Proxy.Proxies) > 0, so this guard is
// defensive (the handler should never be called with nil in production).
func newProxyStatsHandler(pool *proxypool.Pool, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		if pool == nil {
			http.Error(rw, "proxy pool not configured", http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(rw).Encode(pool.Stats()); err != nil {
			// Encode errors are extremely rare (would mean the client
			// closed the connection mid-write). Log at error so on-call
			// notices a recurring pattern but don't try to write an
			// error response — the response is already partially flushed.
			logger.Error("proxy_stats_encode_failed", slog.Any("error", err))
		}
	})
}
