package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

type contextKey string

const (
	sessionContextKey contextKey = "session"
	csrfTokenKey      contextKey = "csrf_token"
	csrfCookieName               = "gms_csrf"
)

// generateCSRFToken creates a CSRF token from session ID using HMAC.
func generateCSRFToken(sessionID string, secretKey []byte) string {
	h := hmac.New(sha256.New, secretKey)
	h.Write([]byte(sessionID))

	return hex.EncodeToString(h.Sum(nil))
}

// generateRandomCSRFToken creates a random CSRF token for double-submit cookie pattern.
func generateRandomCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// validateCSRFToken checks if the provided token matches the expected token.
func validateCSRFToken(sessionID string, secretKey []byte, token string) bool {
	expected := generateCSRFToken(sessionID, secretKey)
	return hmac.Equal([]byte(expected), []byte(token))
}

// CSRFTokenFromContext retrieves the CSRF token from context.
func CSRFTokenFromContext(ctx context.Context) string {
	if token, ok := ctx.Value(csrfTokenKey).(string); ok {
		return token
	}

	return ""
}

// SessionFromContext retrieves the session from context.
func SessionFromContext(ctx context.Context) *Session {
	if session, ok := ctx.Value(sessionContextKey).(*Session); ok {
		return session
	}

	return nil
}

// SessionAuth middleware checks for valid session cookie.
func SessionAuth(store IStore, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cookieName)
			if err != nil {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}

			session, err := store.GetSession(r.Context(), cookie.Value)
			if err != nil {
				// Clear invalid cookie
				http.SetCookie(w, &http.Cookie{
					Name:     cookieName,
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
					Secure:   isSecureRequest(r),
				})
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)

				return
			}

			ctx := context.WithValue(r.Context(), sessionContextKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CSRFProtection middleware adds CSRF token to context and validates on POST/PUT/PATCH/DELETE.
// For authenticated users: uses HMAC-based token derived from session ID.
// For unauthenticated users: uses double-submit cookie pattern.
func CSRFProtection(secretKey []byte, sessionCookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get session from cookie (not from context, as this may run before SessionAuth)
			var sessionID string
			if cookie, err := r.Cookie(sessionCookieName); err == nil {
				sessionID = cookie.Value
			}

			var csrfToken string

			if sessionID != "" {
				// Authenticated: use HMAC-based token
				csrfToken = generateCSRFToken(sessionID, secretKey)
			} else {
				// Unauthenticated: use double-submit cookie pattern
				if cookie, err := r.Cookie(csrfCookieName); err == nil {
					csrfToken = cookie.Value
				} else {
					// Generate new token for GET requests
					csrfToken = generateRandomCSRFToken()
					http.SetCookie(w, &http.Cookie{
						Name:     csrfCookieName,
						Value:    csrfToken,
						Path:     "/admin",
						HttpOnly: true,
						SameSite: http.SameSiteStrictMode,
						Secure:   isSecureRequest(r),
					})
				}
			}

			// For state-changing methods, validate the CSRF token
			if r.Method == http.MethodPost || r.Method == http.MethodPut ||
				r.Method == http.MethodPatch || r.Method == http.MethodDelete {
				submittedToken := r.FormValue("csrf_token")
				if submittedToken == "" {
					submittedToken = r.Header.Get("X-CSRF-Token")
				}

				if sessionID != "" {
					// Authenticated: validate HMAC token
					if !validateCSRFToken(sessionID, secretKey, submittedToken) {
						http.Error(w, "Invalid CSRF token", http.StatusForbidden)
						return
					}
				} else {
					// Unauthenticated: validate double-submit cookie
					cookieToken := ""
					if cookie, err := r.Cookie(csrfCookieName); err == nil {
						cookieToken = cookie.Value
					}

					if cookieToken == "" || !hmac.Equal([]byte(cookieToken), []byte(submittedToken)) {
						http.Error(w, "Invalid CSRF token", http.StatusForbidden)
						return
					}
				}
			}

			// Add CSRF token to context for use in templates
			ctx := context.WithValue(r.Context(), csrfTokenKey, csrfToken)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
