package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

func generateID() string {
	return uuid.New().String()
}

const maxWebhookConfigsPerUser = 10

// WebhookHandlers contains routes for webhook config management.
type WebhookHandlers struct{ Deps Dependencies }

// ---- request / response types ----

type createWebhookRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type createWebhookResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Secret string `json:"secret"` // shown exactly once
}

type listWebhookItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	VerifiedAt string `json:"verified_at,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

type updateWebhookRequest struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// ---- handlers ----

// ListWebhooks returns all webhook configs for the authenticated user.
// GET /api/v1/webhooks
func (h *WebhookHandlers) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"})
		return
	}

	configs, err := h.Deps.WebhookConfigRepo.ListByUserID(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to list webhook configs")
		return
	}

	items := make([]listWebhookItem, 0, len(configs))
	for _, c := range configs {
		item := listWebhookItem{
			ID:        c.ID,
			Name:      c.Name,
			URL:       c.URL,
			CreatedAt: c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			UpdatedAt: c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if c.VerifiedAt != nil {
			item.VerifiedAt = c.VerifiedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if c.RevokedAt != nil {
			item.RevokedAt = c.RevokedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, item)
	}

	renderJSON(w, http.StatusOK, items)
}

// CreateWebhook generates and stores a new webhook config.
// POST /api/v1/webhooks
func (h *WebhookHandlers) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"})
		return
	}

	var req createWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "invalid request body"})
		return
	}
	if req.Name == "" {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "name is required"})
		return
	}
	if len(req.Name) > 100 {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "name must be 100 characters or fewer"})
		return
	}
	if req.URL == "" {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "url is required"})
		return
	}

	// Validate and resolve webhook URL (SSRF prevention).
	resolvedIP, err := ValidateWebhookURL(req.URL)
	if err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "invalid webhook URL: " + err.Error()})
		return
	}

	// Enforce per-user limit.
	active, err := h.Deps.WebhookConfigRepo.ListActiveByUserID(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to check existing webhooks")
		return
	}
	if len(active) >= maxWebhookConfigsPerUser {
		renderJSON(w, http.StatusConflict, models.APIError{
			Code:    http.StatusConflict,
			Message: "maximum number of active webhook configs reached (limit: 10); revoke an existing one before creating a new one",
		})
		return
	}

	// Generate signing secret: 32 bytes of crypto/rand, hex-encoded (256-bit entropy).
	id := generateID()
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		internalError(w, h.Deps.Logger, err, "failed to generate signing secret")
		return
	}
	plaintextSecret := hex.EncodeToString(secretBytes)

	mac := hmac.New(sha256.New, h.Deps.ServerSecret)
	mac.Write([]byte(plaintextSecret))
	secretHash := hex.EncodeToString(mac.Sum(nil))

	cfg := &models.WebhookConfig{
		ID:         id,
		UserID:     userID,
		Name:       req.Name,
		URL:        req.URL,
		SecretHash: secretHash,
		ResolvedIP: &resolvedIP,
	}

	if err := h.Deps.WebhookConfigRepo.Create(r.Context(), cfg); err != nil {
		internalError(w, h.Deps.Logger, err, "failed to store webhook config")
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("webhook_config_created", slog.String("user_id", userID), slog.String("webhook_id", id))
	}

	renderJSON(w, http.StatusCreated, createWebhookResponse{
		ID:     id,
		Name:   req.Name,
		URL:    req.URL,
		Secret: plaintextSecret,
	})
}

// UpdateWebhook modifies a webhook config's mutable fields.
// PATCH /api/v1/webhooks/{id}
func (h *WebhookHandlers) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"})
		return
	}

	webhookID := mux.Vars(r)["id"]
	if webhookID == "" {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "webhook id is required"})
		return
	}

	var req updateWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "invalid request body"})
		return
	}

	// Fetch existing to merge partial update.
	existing, err := h.Deps.WebhookConfigRepo.GetByID(r.Context(), webhookID)
	if err != nil {
		if err == models.ErrWebhookConfigNotFound {
			renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "webhook config not found"})
			return
		}
		internalError(w, h.Deps.Logger, err, "failed to fetch webhook config")
		return
	}
	if existing.UserID != userID {
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "webhook config not found"})
		return
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.URL != "" {
		resolvedIP, err := ValidateWebhookURL(req.URL)
		if err != nil {
			renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "invalid webhook URL: " + err.Error()})
			return
		}
		existing.URL = req.URL
		existing.ResolvedIP = &resolvedIP
	}

	if err := h.Deps.WebhookConfigRepo.Update(r.Context(), existing); err != nil {
		if err == models.ErrWebhookConfigNotFound {
			renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "webhook config not found or already revoked"})
			return
		}
		internalError(w, h.Deps.Logger, err, "failed to update webhook config")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RevokeWebhook soft-deletes a webhook config.
// DELETE /api/v1/webhooks/{id}
func (h *WebhookHandlers) RevokeWebhook(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"})
		return
	}

	webhookID := mux.Vars(r)["id"]
	if webhookID == "" {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "webhook id is required"})
		return
	}

	if err := h.Deps.WebhookConfigRepo.Revoke(r.Context(), webhookID, userID); err != nil {
		if err == models.ErrWebhookConfigNotFound {
			renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "webhook config not found or already revoked"})
			return
		}
		internalError(w, h.Deps.Logger, err, "failed to revoke webhook config")
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("webhook_config_revoked", slog.String("user_id", userID), slog.String("webhook_id", webhookID))
	}

	w.WriteHeader(http.StatusNoContent)
}
