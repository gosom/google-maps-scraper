package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/encryption"
	"github.com/gosom/google-maps-scraper/pkg/googlesheets"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/web/auth"
)

type IntegrationHandler struct {
	repo          models.IntegrationRepository
	jobService    JobService
	sheetsService *googlesheets.Service
	log           *slog.Logger
}

func NewIntegrationHandler(repo models.IntegrationRepository, jobService JobService, sheetsService *googlesheets.Service) *IntegrationHandler {
	return &IntegrationHandler{
		repo:          repo,
		jobService:    jobService,
		sheetsService: sheetsService,
		log:           pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "integration"),
	}
}

func (h *IntegrationHandler) googleConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("GOOGLE_REDIRECT_URL"),
		Scopes: []string{
			"https://www.googleapis.com/auth/spreadsheets",
			"https://www.googleapis.com/auth/drive.file", // Allows creating files/folders and managing them
			"https://www.googleapis.com/auth/userinfo.email",
		},
		Endpoint: google.Endpoint,
	}
}

func (h *IntegrationHandler) HandleGoogleAuth(w http.ResponseWriter, r *http.Request) {
	// Generate a state token to prevent CSRF
	state := uuid.New().String()

	// Store state in a secure cookie.
	// Use IS_PRODUCTION env var to unconditionally set Secure in production
	// rather than trusting the client-supplied X-Forwarded-Proto header (CWE-614).
	isSecure := r.TLS != nil || os.Getenv("IS_PRODUCTION") == "1"
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // 5 minutes
	})

	// AccessTypeOffline is required to get a refresh token
	url := h.googleConfig().AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	h.log.Info("google_oauth_initiated")
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (h *IntegrationHandler) HandleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Verify state
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, "State cookie not found", http.StatusBadRequest)
		return
	}

	// Clear the cookie.
	// Use IS_PRODUCTION env var (not X-Forwarded-Proto) to set Secure flag (CWE-614).
	isSecure := r.TLS != nil || os.Getenv("IS_PRODUCTION") == "1"
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	if r.URL.Query().Get("state") != stateCookie.Value {
		h.log.Warn("google_oauth_invalid_state")
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		h.log.Warn("google_oauth_missing_code")
		http.Error(w, "Code not found", http.StatusBadRequest)
		return
	}

	token, err := h.googleConfig().Exchange(ctx, code)
	if err != nil {
		h.log.Error("google_oauth_token_exchange_failed",
			slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Failed to exchange token", http.StatusInternalServerError)
		return
	}

	// Get user from context using auth helper
	userIDStr, err := auth.GetUserID(ctx)
	if err != nil {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	// Encrypt tokens
	encryptedAccessToken, err := encryption.Encrypt(token.AccessToken)
	if err != nil {
		h.log.Error("google_oauth_encrypt_access_token_failed",
			slog.String("user_id", userIDStr), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Failed to encrypt access token", http.StatusInternalServerError)
		return
	}

	encryptedRefreshToken, err := encryption.Encrypt(token.RefreshToken)
	if err != nil {
		h.log.Error("google_oauth_encrypt_refresh_token_failed",
			slog.String("user_id", userIDStr), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		http.Error(w, "Failed to encrypt refresh token", http.StatusInternalServerError)
		return
	}

	integration := &models.UserIntegration{
		UserID:       userIDStr,
		Provider:     "google",
		AccessToken:  []byte(encryptedAccessToken),
		RefreshToken: []byte(encryptedRefreshToken),
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
	// Redirect back to frontend
	http.Redirect(w, r, "/dashboard/integrations?success=true", http.StatusTemporaryRedirect)
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

	// Parse request body for optional filename
	var req struct {
		Name string `json:"name"`
	}
	// Ignore error here as body is optional
	_ = json.NewDecoder(r.Body).Decode(&req)

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

	decryptedAccessToken, err := encryption.Decrypt(string(integration.AccessToken))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt access token: %w", err)
	}

	token := &oauth2.Token{
		AccessToken: decryptedAccessToken,
		TokenType:   "Bearer",
		Expiry:      integration.Expiry,
	}

	if len(integration.RefreshToken) > 0 {
		decryptedRefreshToken, err := encryption.Decrypt(string(integration.RefreshToken))
		if err == nil {
			token.RefreshToken = decryptedRefreshToken
		}
	}

	tokenSource := h.googleConfig().TokenSource(ctx, token)
	return oauth2.NewClient(ctx, tokenSource), nil
}

func (h *IntegrationHandler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	googleEnabled := os.Getenv("GOOGLE_CLIENT_ID") != "" &&
		os.Getenv("GOOGLE_CLIENT_SECRET") != "" &&
		os.Getenv("GOOGLE_REDIRECT_URL") != ""

	config := map[string]bool{
		"google_sheets": googleEnabled,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(config)
}
