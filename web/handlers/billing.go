package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
	webservices "github.com/gosom/google-maps-scraper/web/services"
)

// kept for request parsing in this layer
type checkoutSessionRequest struct {
	Credits  string `json:"credits"`
	Currency string `json:"currency"`
}

type reconcileRequest struct {
	SessionID string `json:"session_id"`
}

func (h *BillingHandlers) GetCreditBalance(w http.ResponseWriter, r *http.Request) {
	if h.Deps.DB == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "database not available"})
		return
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
	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc)
	resp, err := cs.GetBalance(r.Context(), userID)
	if err != nil {
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, resp)
}

func (h *BillingHandlers) CreateCheckoutSession(w http.ResponseWriter, r *http.Request) {
	if h.Deps.BillingSvc == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "billing not configured"})
		return
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
	var req checkoutSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "invalid payload"})
		return
	}
	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc)
	out, err := cs.CreateCheckoutSession(r.Context(), billing.CheckoutRequest{UserID: userID, Credits: req.Credits, Currency: req.Currency})
	if err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, out)
}

func (h *BillingHandlers) Reconcile(w http.ResponseWriter, r *http.Request) {
	if h.Deps.BillingSvc == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "billing not configured"})
		return
	}
	var req reconcileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "invalid payload"})
		return
	}
	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc)
	if err := cs.Reconcile(r.Context(), req.SessionID); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *BillingHandlers) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if h.Deps.BillingSvc == nil {
		slog.Error("billing_svc_nil_in_webhook_handler")
		w.WriteHeader(http.StatusNotFound)
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("webhook_payload_read_failed", slog.Any("error", err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	slog.Debug("webhook_received", slog.Int("payload_length", len(payload)), slog.Bool("signature_present", sig != ""))

	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc)
	code, err := cs.HandleWebhook(r.Context(), payload, sig)
	if err != nil {
		slog.Error("webhook_processing_failed", slog.Any("error", err))
	}
	slog.Debug("webhook_response", slog.Int("code", code))
	w.WriteHeader(code)
}

func (h *BillingHandlers) GetBillingHistory(w http.ResponseWriter, r *http.Request) {
	if h.Deps.DB == nil {
		renderJSON(w, http.StatusServiceUnavailable, models.APIError{Code: http.StatusServiceUnavailable, Message: "database not available"})
		return
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

	// Parse pagination params
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc)
	resp, err := cs.GetBillingHistory(r.Context(), userID, limit, offset)
	if err != nil {
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: err.Error()})
		return
	}
	renderJSON(w, http.StatusOK, resp)
}
