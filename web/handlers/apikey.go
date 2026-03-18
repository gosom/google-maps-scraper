package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

const maxAPIKeysPerUser = 10

// APIKeyHandlers contains routes for API key management.
type APIKeyHandlers struct{ Deps Dependencies }

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

type createAPIKeyResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Key           string `json:"key"` // shown exactly once
	KeyHintPrefix string `json:"key_hint_prefix"`
	KeyHintSuffix string `json:"key_hint_suffix"`
}

type listAPIKeyItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	KeyHintPrefix string `json:"key_hint_prefix"`
	KeyHintSuffix string `json:"key_hint_suffix"`
	UsageCount    int64  `json:"usage_count"`
	CreatedAt     string `json:"created_at"`
	LastUsedAt    string `json:"last_used_at,omitempty"`
	RevokedAt     string `json:"revoked_at,omitempty"`
}

// ListAPIKeys returns all API keys for the authenticated user (including revoked).
// GET /api/v1/api-keys
func (h *APIKeyHandlers) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"})
		return
	}

	keys, err := h.Deps.APIKeyRepo.ListByUserID(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to list API keys")
		return
	}

	items := make([]listAPIKeyItem, 0, len(keys))
	for _, k := range keys {
		item := listAPIKeyItem{
			ID:            k.ID,
			Name:          k.Name,
			KeyHintPrefix: k.KeyHintPrefix,
			KeyHintSuffix: k.KeyHintSuffix,
			UsageCount:    k.UsageCount,
			CreatedAt:     k.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if k.LastUsedAt != nil {
			item.LastUsedAt = k.LastUsedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if k.RevokedAt != nil {
			item.RevokedAt = k.RevokedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, item)
	}

	renderJSON(w, http.StatusOK, items)
}

// CreateAPIKey generates and stores a new API key for the authenticated user.
// POST /api/v1/api-keys
func (h *APIKeyHandlers) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"})
		return
	}

	var req createAPIKeyRequest
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

	// Enforce per-user limit on active keys.
	activeKeys, err := h.Deps.APIKeyRepo.ListActiveByUserID(r.Context(), userID)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to check existing API keys")
		return
	}
	if len(activeKeys) >= maxAPIKeysPerUser {
		renderJSON(w, http.StatusConflict, models.APIError{
			Code:    http.StatusConflict,
			Message: "maximum number of active API keys reached (limit: 10); revoke an existing key before creating a new one",
		})
		return
	}

	apiKey, plaintext, err := auth.GenerateAPIKey(userID, req.Name, h.Deps.ServerSecret)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to generate API key")
		return
	}

	if err := h.Deps.APIKeyRepo.Create(r.Context(), apiKey); err != nil {
		internalError(w, h.Deps.Logger, err, "failed to store API key")
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("api_key_created", slog.String("user_id", userID), slog.String("key_id", apiKey.ID), slog.String("name", apiKey.Name))
	}

	renderJSON(w, http.StatusCreated, createAPIKeyResponse{
		ID:            apiKey.ID,
		Name:          apiKey.Name,
		Key:           plaintext,
		KeyHintPrefix: apiKey.KeyHintPrefix,
		KeyHintSuffix: apiKey.KeyHintSuffix,
	})
}

// RevokeAPIKey soft-deletes an API key owned by the authenticated user.
// DELETE /api/v1/api-keys/{id}
func (h *APIKeyHandlers) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"})
		return
	}

	keyID := mux.Vars(r)["id"]
	if keyID == "" {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "key id is required"})
		return
	}

	if err := h.Deps.APIKeyRepo.Revoke(r.Context(), keyID, userID); err != nil {
		if errors.Is(err, models.ErrAPIKeyNotFound) {
			renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "api key not found or already revoked"})
			return
		}
		internalError(w, h.Deps.Logger, err, "failed to revoke API key")
		return
	}

	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("api_key_revoked", slog.String("user_id", userID), slog.String("key_id", keyID))
	}

	w.WriteHeader(http.StatusNoContent)
}
