package admin

import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/log"
)

// apiKeyFlash is an in-memory one-time store for newly created API keys.
// Keys are read once and deleted, surviving only the POST->redirect->GET cycle.
var apiKeyFlash = struct {
	sync.Mutex
	m map[string]string
}{m: make(map[string]string)}

// APIKeysPageHandler renders the API keys management page.
func APIKeysPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		keys, err := appState.Store.ListAPIKeys(r.Context(), session.UserID)
		if err != nil {
			log.Error("failed to list API keys", "error", err)
			http.Redirect(w, r, "/admin/?error=Failed+to+load+API+keys", http.StatusSeeOther)

			return
		}

		// Read and clear one-time flash for newly created key.
		apiKeyFlash.Lock()
		newKey := apiKeyFlash.m[session.ID]
		delete(apiKeyFlash.m, session.ID)
		apiKeyFlash.Unlock()

		data := map[string]any{
			"APIKeys": keys,
			"Success": r.URL.Query().Get("success"),
			"Error":   r.URL.Query().Get("error"),
			"NewKey":  newKey,
		}
		renderTemplate(appState, w, r, "api_keys.html", data)
	}
}

// CreateAPIKeyHandler handles API key creation.
func CreateAPIKeyHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			http.Redirect(w, r, "/admin/api-keys?error=Key+name+is+required", http.StatusSeeOther)
			return
		}

		if len(name) > 100 {
			http.Redirect(w, r, "/admin/api-keys?error=Key+name+must+be+100+characters+or+less", http.StatusSeeOther)
			return
		}

		hex, err := cryptoext.GenerateRandomHexString(32)
		if err != nil {
			log.Error("failed to generate API key", "error", err)
			http.Redirect(w, r, "/admin/api-keys?error=Failed+to+generate+key", http.StatusSeeOther)

			return
		}

		rawKey := "gms_" + hex
		keyHash := cryptoext.Sha256Hash(rawKey)
		keyPrefix := rawKey[:12]

		_, err = appState.Store.CreateAPIKey(r.Context(), session.UserID, name, keyHash, keyPrefix)
		if err != nil {
			log.Error("failed to create API key", "error", err)
			http.Redirect(w, r, "/admin/api-keys?error=Failed+to+create+key", http.StatusSeeOther)

			return
		}

		// Store in memory for the redirect, never touches the database.
		apiKeyFlash.Lock()
		apiKeyFlash.m[session.ID] = rawKey
		apiKeyFlash.Unlock()

		log.Info("audit", "action", "api_key_create", "user_id", session.UserID, "key_name", name, "ip", r.RemoteAddr)

		http.Redirect(w, r, "/admin/api-keys?success=API+key+created+successfully", http.StatusSeeOther)
	}
}

// RevokeAPIKeyHandler handles API key revocation.
func RevokeAPIKeyHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		keyIDStr := r.FormValue("key_id")

		keyID, err := strconv.Atoi(keyIDStr)
		if err != nil {
			http.Redirect(w, r, "/admin/api-keys?error=Invalid+key+ID", http.StatusSeeOther)
			return
		}

		if err := appState.Store.RevokeAPIKey(r.Context(), session.UserID, keyID); err != nil {
			log.Error("failed to revoke API key", "error", err)
			http.Redirect(w, r, "/admin/api-keys?error=Failed+to+revoke+key", http.StatusSeeOther)

			return
		}

		log.Info("audit", "action", "api_key_revoke", "user_id", session.UserID, "key_id", keyID, "ip", r.RemoteAddr)

		http.Redirect(w, r, "/admin/api-keys?success=API+key+revoked+successfully", http.StatusSeeOther)
	}
}
