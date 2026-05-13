package admin

import (
	"net/http"
	"strings"

	"github.com/gosom/google-maps-scraper/cryptoext"
)

// renderTemplate renders a template with the given data.
func renderTemplate(appState *AppState, w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if data == nil {
		data = make(map[string]any)
	}

	data["CSRFToken"] = CSRFTokenFromContext(r.Context())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := appState.Templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// validatePassword validates a user's password.
func validatePassword(user *User, password string) error {
	if !cryptoext.VerifyPassword(password, user.PasswordHash) {
		return ErrInvalidPassword
	}

	return nil
}

// isSecureRequest checks if the request was made over HTTPS.
// Checks both direct TLS and X-Forwarded-Proto header for reverse proxy setups.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// Check X-Forwarded-Proto header for reverse proxy setups
	if proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); strings.EqualFold(proto, "https") {
		return true
	}

	return false
}

// splitBackupCodes splits a comma-separated string of backup codes.
func splitBackupCodes(codes string) []string {
	return strings.Split(codes, ",")
}
