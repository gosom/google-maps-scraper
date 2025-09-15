package middleware

import "net/http"

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
		w.Header().Set("Access-Control-Allow-Origin", "*")
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
