package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/appenv"
	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
	"github.com/gosom/google-maps-scraper/pkg/encryption"
	"github.com/gosom/google-maps-scraper/pkg/googlesheets"
	"github.com/gosom/google-maps-scraper/web/auth"
)

type IntegrationHandler struct {
	repo          models.IntegrationRepository
	enc           *encryption.Encryptor // nil means encryption disabled
	jobService    JobService
	sheetsService *googlesheets.Service
	env           appenv.Environment
	google        pkgconfig.GoogleConfig
	log           *slog.Logger
}

func NewIntegrationHandler(repo models.IntegrationRepository, enc *encryption.Encryptor, jobService JobService, sheetsService *googlesheets.Service, env appenv.Environment, googleCfg pkgconfig.GoogleConfig, logger *slog.Logger) *IntegrationHandler {
	return &IntegrationHandler{
		repo:          repo,
		enc:           enc,
		jobService:    jobService,
		sheetsService: sheetsService,
		env:           env,
		google:        googleCfg,
		// 3rd-tier tag (handler under the api module): "service" rather
		// than "component" so we don't shadow the component=<runner>
		// tag set in main.go or the module=api tag set in web/web.go.
		log: logger.With(slog.String("service", "integration")),
	}
}

func (h *IntegrationHandler) googleConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     h.google.ClientID,
		ClientSecret: h.google.ClientSecret,
		RedirectURL:  h.google.RedirectURL,
		Scopes: []string{
			// drive.file is the minimum scope needed: it lets us create new
			// spreadsheets on the user's Drive (via Files.Create with the
			// google-apps.spreadsheet MIME type) and read/write only those
			// files. We never touch the user's other Drive content.
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/userinfo.email",
		},
		Endpoint: google.Endpoint,
	}
}

// HandleGoogleCallback exchanges an OAuth authorization code for tokens and
// persists the user's Google integration. Called by the Next.js frontend
// after it has validated the CSRF state cookie (which lives on the frontend
// origin, where Clerk's host-only __session cookie is also scoped).
func (h *IntegrationHandler) HandleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		h.log.Warn("google_oauth_missing_code")
		http.Error(w, "Code not found", http.StatusBadRequest)
		return
	}

	token, err := h.googleConfig().Exchange(ctx, body.Code)
	if err != nil {
		h.log.Error("google_oauth_token_exchange_failed",
			slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Failed to exchange token", http.StatusInternalServerError)
		return
	}

	userIDStr, err := auth.GetUserID(ctx)
	if err != nil {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	// Pass plaintext tokens; the repository encrypts on Save.
	integration := &models.UserIntegration{
		UserID:       userIDStr,
		Provider:     "google",
		AccessToken:  []byte(token.AccessToken),
		RefreshToken: []byte(token.RefreshToken),
		Expiry:       token.Expiry,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := h.repo.Save(ctx, integration); err != nil {
		h.log.Error("google_oauth_save_integration_failed",
			slog.String("user_id", userIDStr), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Failed to save integration", http.StatusInternalServerError)
		return
	}

	h.log.Info("google_oauth_integration_saved", slog.String("user_id", userIDStr))
	w.WriteHeader(http.StatusNoContent)
}

func (h *IntegrationHandler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	client, err := h.getHTTPClient(ctx, userID)
	if err != nil {
		// If integration not found, return not connected
		if err == models.ErrNotFound { // Assuming models.ErrNotFound exists or we check error string
			// Actually repo.Get usually returns error if not found.
			// Let's check if we can distinguish not found.
			// For now, if error, we assume not connected or error.
			// But getHTTPClient returns error if not found.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"connected": false}`))
			return
		}
		slog.Error("failed_to_get_integration_client", slog.String("user_id", userID), slog.Any("error", err))
		http.Error(w, "Failed to get integration client", http.StatusInternalServerError)
		return
	}

	// Get user info from Google
	email, err := h.sheetsService.GetUserInfo(ctx, client)
	if err != nil {
		// If we can't get info, maybe token is revoked or expired and refresh failed.
		// We return connected: true but unknown email to indicate the link exists in DB.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connected": true, "email": "Unknown"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"connected": true, "email": email})
}

func (h *IntegrationHandler) HandleExportJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	jobID := vars["id"]
	if jobID == "" {
		http.Error(w, "Job ID is required", http.StatusBadRequest)
		return
	}

	// Verify job ownership (ownership enforced in DB query)
	if _, err = h.jobService.Get(ctx, jobID, userID); err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Get CSV content
	csvReader, filename, err := h.jobService.GetCSVReader(ctx, jobID)
	if err != nil {
		h.log.Error("google_sheets_get_csv_failed",
			slog.String("user_id", userID), slog.String("job_id", jobID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Failed to get CSV", http.StatusInternalServerError)
		return
	}
	defer csvReader.Close()

	client, err := h.getHTTPClient(ctx, userID)
	if err != nil {
		h.log.Warn("google_sheets_no_integration", slog.String("user_id", userID), slog.String("job_id", jobID))
		http.Error(w, "Google integration not found or invalid", http.StatusNotFound)
		return
	}

	// Parse request body for optional filename. decodeStrictOptional
	// treats an empty body as success (so the existing "no body =
	// default filename" behavior is preserved), but unknown fields and
	// trailing data on a non-empty body are now rejected.
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeStrictOptional(r, &req); err != nil {
		h.log.Warn("google_sheets_decode_failed",
			slog.String("user_id", userID), slog.String("job_id", jobID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Invalid request body", http.StatusUnprocessableEntity)
		return
	}

	// Upload to Google Sheets
	if filename == "" {
		filename = fmt.Sprintf("job-%s.csv", jobID)
	}

	// If user provided a custom name, use it after sanitization
	if req.Name != "" {
		// Sanitize filename: allow alphanumeric, spaces, hyphens, underscores, and dots
		// Also limit length to 100 characters
		sanitized := ""
		for _, r := range req.Name {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '-' || r == '_' || r == '.' {
				sanitized += string(r)
			}
		}

		// Trim spaces
		sanitized = strings.TrimSpace(sanitized)

		// Limit length
		if len(sanitized) > 100 {
			sanitized = sanitized[:100]
		}

		if sanitized != "" {
			filename = sanitized
		}
	}

	h.log.Info("google_sheets_upload_started", slog.String("user_id", userID), slog.String("job_id", jobID), slog.String("filename", filename))
	sheetURL, err := h.sheetsService.UploadCSV(ctx, client, filename, csvReader)
	if err != nil {
		h.log.Error("google_sheets_upload_failed",
			slog.String("user_id", userID), slog.String("job_id", jobID), slog.String("filename", filename),
			slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Failed to upload to Google Sheets", http.StatusInternalServerError)
		return
	}

	h.log.Info("google_sheets_upload_complete", slog.String("user_id", userID), slog.String("job_id", jobID), slog.String("filename", filename))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": sheetURL})
}

// getHTTPClient retrieves the user's integration, decrypts tokens, and returns an authenticated HTTP client
func (h *IntegrationHandler) getHTTPClient(ctx context.Context, userID string) (*http.Client, error) {
	integration, err := h.repo.Get(ctx, userID, "google")
	if err != nil {
		return nil, err
	}

	// Tokens are already decrypted by the repository's Get method
	token := &oauth2.Token{
		AccessToken: string(integration.AccessToken),
		TokenType:   "Bearer",
		Expiry:      integration.Expiry,
	}

	if len(integration.RefreshToken) > 0 {
		token.RefreshToken = string(integration.RefreshToken)
	}

	tokenSource := h.googleConfig().TokenSource(ctx, token)
	return oauth2.NewClient(ctx, tokenSource), nil
}

func (h *IntegrationHandler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	googleEnabled := h.google.ClientID != "" &&
		h.google.ClientSecret != "" &&
		h.google.RedirectURL != ""

	config := map[string]bool{
		"google_sheets": googleEnabled,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(config)
}
