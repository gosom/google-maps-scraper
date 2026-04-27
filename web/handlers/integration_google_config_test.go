package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/pkg/appenv"
	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
)

// TestIntegrationHandler_UsesInjectedGoogleConfig asserts that googleConfig()
// uses the injected pkgconfig.GoogleConfig and ignores os.Getenv at request time.
func TestIntegrationHandler_UsesInjectedGoogleConfig(t *testing.T) {
	h := NewIntegrationHandler(
		nil, nil, nil, nil,
		appenv.Environment(0),
		pkgconfig.GoogleConfig{
			ClientID:     "from-config-id",
			ClientSecret: "from-config-secret",
			RedirectURL:  "https://x.example.com/cb",
		},
		slog.Default(),
	)

	// env values should be IGNORED
	t.Setenv("GOOGLE_CLIENT_ID", "from-env-id-WRONG")
	t.Setenv("GOOGLE_CLIENT_SECRET", "from-env-secret-WRONG")
	t.Setenv("GOOGLE_REDIRECT_URL", "from-env-url-WRONG")

	cfg := h.googleConfig()
	assert.Equal(t, "from-config-id", cfg.ClientID)
	assert.Equal(t, "from-config-secret", cfg.ClientSecret)
	assert.Equal(t, "https://x.example.com/cb", cfg.RedirectURL)
}

// TestIntegrationHandler_HandleGetConfig_UsesInjectedConfig asserts that
// HandleGetConfig determines google_sheets enablement from the injected
// config, not from os.Getenv at request time.
func TestIntegrationHandler_HandleGetConfig_UsesInjectedConfig(t *testing.T) {
	// Ensure env vars are set to something wrong — they must be ignored.
	t.Setenv("GOOGLE_CLIENT_ID", "env-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "env-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "https://env-url.example.com/cb")

	t.Run("all_three_fields_non_empty_returns_true", func(t *testing.T) {
		h := NewIntegrationHandler(
			nil, nil, nil, nil,
			appenv.Environment(0),
			pkgconfig.GoogleConfig{
				ClientID:     "injected-id",
				ClientSecret: "injected-secret",
				RedirectURL:  "https://injected.example.com/cb",
			},
			slog.Default(),
		)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/config", nil)
		h.HandleGetConfig(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var got map[string]bool
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.True(t, got["google_sheets"], "expected google_sheets=true when all three fields are injected")
	})

	t.Run("empty_client_id_returns_false", func(t *testing.T) {
		h := NewIntegrationHandler(
			nil, nil, nil, nil,
			appenv.Environment(0),
			pkgconfig.GoogleConfig{
				ClientID:     "", // deliberately empty
				ClientSecret: "injected-secret",
				RedirectURL:  "https://injected.example.com/cb",
			},
			slog.Default(),
		)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/config", nil)
		h.HandleGetConfig(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var got map[string]bool
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.False(t, got["google_sheets"], "expected google_sheets=false when ClientID is empty")
	})

	t.Run("empty_client_secret_returns_false", func(t *testing.T) {
		h := NewIntegrationHandler(
			nil, nil, nil, nil,
			appenv.Environment(0),
			pkgconfig.GoogleConfig{
				ClientID:     "injected-id",
				ClientSecret: "", // deliberately empty
				RedirectURL:  "https://injected.example.com/cb",
			},
			slog.Default(),
		)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/config", nil)
		h.HandleGetConfig(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var got map[string]bool
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.False(t, got["google_sheets"], "expected google_sheets=false when ClientSecret is empty")
	})

	t.Run("empty_redirect_url_returns_false", func(t *testing.T) {
		h := NewIntegrationHandler(
			nil, nil, nil, nil,
			appenv.Environment(0),
			pkgconfig.GoogleConfig{
				ClientID:     "injected-id",
				ClientSecret: "injected-secret",
				RedirectURL:  "", // deliberately empty
			},
			slog.Default(),
		)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/config", nil)
		h.HandleGetConfig(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var got map[string]bool
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.False(t, got["google_sheets"], "expected google_sheets=false when RedirectURL is empty")
	})

	t.Run("all_empty_injected_returns_false_despite_env_vars_set", func(t *testing.T) {
		// GOOGLE_* env vars are set (above), but config has empty values.
		// Handler must use injected config, not env vars.
		h := NewIntegrationHandler(
			nil, nil, nil, nil,
			appenv.Environment(0),
			pkgconfig.GoogleConfig{}, // all empty
			slog.Default(),
		)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/config", nil)
		h.HandleGetConfig(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var got map[string]bool
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.False(t, got["google_sheets"], "expected google_sheets=false when injected config is empty (env vars must be ignored)")
	})
}
