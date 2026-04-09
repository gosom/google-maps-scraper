package handlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	webservices "github.com/gosom/google-maps-scraper/web/services"
)

func (h *APIHandlers) GetDashboard(w http.ResponseWriter, r *http.Request) {
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("request", slog.String("method", "GET"), slog.String("path", r.URL.Path))
	}
	if h.Deps.Auth == nil {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "Authentication not configured"})
		return
	}
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{Code: http.StatusUnauthorized, Message: "User not authenticated"})
		return
	}
	if h.Deps.DB == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "database not available"})
		return
	}

	limit := 5
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 20 {
			limit = parsed
		}
	}

	ds := webservices.NewDashboardService(h.Deps.DB)
	resp, err := ds.GetDashboard(r.Context(), userID, limit)
	if err != nil {
		internalError(w, h.Deps.Logger, err, "failed to load dashboard",
			slog.String("user_id", userID))
		return
	}
	renderJSON(w, http.StatusOK, resp)
}
