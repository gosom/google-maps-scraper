package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gosom/google-maps-scraper/httpext"
	"github.com/gosom/google-maps-scraper/log"
)

// Routes sets up all admin routes.
func Routes(r chi.Router, appState *AppState, riverUIHandler http.Handler) {
	log.Debug("setting up admin routes")

	// Serve static files (CSS, JS, etc.) - no CSRF needed
	r.Handle("/admin/static/*", StaticFileHandler())

	r.Route("/admin", func(r chi.Router) {
		r.Use(httpext.LoggingMiddleware)
		r.Use(CSRFProtection(appState.EncryptionKey, appState.CookieName))

		r.Get("/login", LoginPageHandler(appState))
		r.Post("/login", LoginSubmitHandler(appState))

		r.Get("/login/2fa", TwoFactorVerifyPageHandler(appState))
		r.Post("/login/2fa", TwoFactorVerifySubmitHandler(appState))

		r.Post("/logout", LogoutHandler(appState))

		r.Group(func(r chi.Router) {
			r.Use(SessionAuth(appState.Store, appState.CookieName))

			r.Get("/", DashboardHandler(appState))
			r.Get("/settings", SettingsPageHandler(appState))
			r.Post("/settings/password", ChangePasswordHandler(appState))
			r.Get("/2fa/prompt", TwoFactorPromptPageHandler(appState))
			r.Get("/2fa/setup", TwoFactorSetupPageHandler(appState))
			r.Post("/2fa/setup", TwoFactorSetupSubmitHandler(appState))
			r.Post("/2fa/disable", TwoFactorDisableHandler(appState))
			r.Get("/api-keys", APIKeysPageHandler(appState))
			r.Post("/api-keys", CreateAPIKeyHandler(appState))
			r.Post("/api-keys/revoke", RevokeAPIKeyHandler(appState))
			r.Get("/jobs", JobsPageHandler(appState))
			r.Get("/jobs/{job_id}/download", DownloadJobResultsHandler(appState))
			r.Post("/jobs/{job_id}/delete", DeleteJobHandler(appState))
			r.Post("/jobs/delete", BatchDeleteJobsHandler(appState))
			r.Post("/jobs/delete-filtered", DeleteAllFilteredJobsHandler(appState))
			r.Get("/workers", WorkersPageHandler(appState))
			r.Get("/workers/stream", WorkersStreamHandler(appState))
			r.Post("/workers", ProvisionWorkerHandler(appState))
			r.Post("/workers/settings", SaveProviderTokenHandler(appState))
			r.Get("/workers/ssh-key/private", DownloadSSHKeyHandler(appState, "private"))
			r.Get("/workers/ssh-key/public", DownloadSSHKeyHandler(appState, "public"))
			r.Post("/workers/{id}/delete", DeleteWorkerHandler(appState))
			r.Get("/workers/{id}/terminal", TerminalPageHandler(appState))
			r.Get("/workers/{id}/terminal/ws", TerminalWSHandler(appState))
		})
	})

	// Mount River UI under /riverui/ (requires session auth)
	r.Group(func(r chi.Router) {
		r.Use(SessionAuth(appState.Store, appState.CookieName))
		r.Mount("/riverui/", riverUIHandler)
	})
	log.Info("River UI available at /riverui/ (requires login)")

	// Health check endpoint
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Root redirect
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
}
