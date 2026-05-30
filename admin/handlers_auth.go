package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/log"
)

// dummyPasswordHash is used for constant-time comparison when user doesn't exist.
// This prevents timing attacks that could enumerate valid usernames.
var dummyPasswordHash string

func init() {
	// Generate a dummy hash at startup for timing-safe comparisons
	dummyPasswordHash, _ = cryptoext.HashPassword("dummy-password-for-timing-safety")
}

// LoginPageHandler renders the login page.
func LoginPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(appState.CookieName); err == nil {
			if _, err := appState.Store.GetSession(r.Context(), cookie.Value); err == nil {
				http.Redirect(w, r, "/admin", http.StatusSeeOther)
				return
			}
		}

		data := map[string]any{
			"Error": r.URL.Query().Get("error"),
		}
		renderTemplate(appState, w, r, "login.html", data)
	}
}

// LoginSubmitHandler handles the login form submission.
func LoginSubmitHandler(appState *AppState) http.HandlerFunc {
	const (
		maxLoginAttempts = 5
		loginRateWindow  = 15 * time.Minute
	)

	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/admin/login?error=Invalid+form", http.StatusSeeOther)
			return
		}

		username := r.FormValue("username")
		password := r.FormValue("password")

		// Check rate limit before attempting login
		rateLimitKey := "login:" + username

		result, err := appState.RateLimiter.Check(r.Context(), rateLimitKey, maxLoginAttempts, loginRateWindow)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=Internal+error", http.StatusSeeOther)
			return
		}

		if !result.Allowed {
			remaining := time.Until(result.ResetAt).Round(time.Minute)
			errorMsg := fmt.Sprintf("Too+many+login+attempts.+Try+again+in+%d+minutes", int(remaining.Minutes()))
			http.Redirect(w, r, "/admin/login?error="+errorMsg, http.StatusSeeOther)

			return
		}

		// Fetch user - but always perform password check to prevent timing attacks
		user, _ := appState.Store.GetUser(r.Context(), username)

		// Always perform password verification to prevent timing-based username enumeration
		var passwordValid bool
		if user != nil {
			passwordValid = cryptoext.VerifyPassword(password, user.PasswordHash)
		} else {
			// User doesn't exist - still run bcrypt to maintain constant timing
			_ = cryptoext.VerifyPassword(password, dummyPasswordHash)
			passwordValid = false
		}

		if !passwordValid {
			http.Redirect(w, r, "/admin/login?error=Invalid+credentials", http.StatusSeeOther)
			return
		}

		// At this point, password is valid
		// For users with 2FA: don't reset rate limit yet (wait for 2FA completion)
		// For users without 2FA: reset rate limit now (login is complete)

		if user.TOTPEnabled {
			ipAddress := r.RemoteAddr
			userAgent := r.UserAgent()

			pendingSession, err := appState.Store.CreateSession(r.Context(), user.ID, ipAddress, userAgent, 5*time.Minute)
			if err != nil {
				http.Redirect(w, r, "/admin/login?error=Failed+to+create+session", http.StatusSeeOther)
				return
			}

			http.SetCookie(w, &http.Cookie{
				Name:     appState.CookieName + "_pending",
				Value:    pendingSession.ID,
				Path:     "/",
				HttpOnly: true,
				Secure:   isSecureRequest(r),
				SameSite: http.SameSiteLaxMode,
				MaxAge:   300,
			})

			http.Redirect(w, r, "/admin/login/2fa", http.StatusSeeOther)

			return
		}

		// No 2FA - login is complete, reset rate limit
		_ = appState.RateLimiter.Reset(r.Context(), rateLimitKey)

		ipAddress := r.RemoteAddr
		userAgent := r.UserAgent()

		session, err := appState.Store.CreateSession(r.Context(), user.ID, ipAddress, userAgent, 24*time.Hour)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=Failed+to+create+session", http.StatusSeeOther)
			return
		}

		log.Info("audit", "action", "login", "user_id", user.ID, "ip", ipAddress)

		http.SetCookie(w, &http.Cookie{
			Name:     appState.CookieName,
			Value:    session.ID,
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecureRequest(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400,
		})

		// Redirect to 2FA prompt for users without 2FA
		http.Redirect(w, r, "/admin/2fa/prompt", http.StatusSeeOther)
	}
}

// LogoutHandler handles logout.
func LogoutHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Require POST method to prevent CSRF via GET
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		if cookie, err := r.Cookie(appState.CookieName); err == nil {
			_ = appState.Store.DeleteSession(r.Context(), cookie.Value)
		}

		log.Info("audit", "action", "logout", "ip", r.RemoteAddr)

		http.SetCookie(w, &http.Cookie{
			Name:     appState.CookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   isSecureRequest(r),
		})

		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	}
}
