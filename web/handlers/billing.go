package handlers

import (
	"io"
	"log/slog"
	"net/http"

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
	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc, h.Deps.Logger)
	resp, err := cs.GetBalance(r.Context(), userID)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("credit_balance_fetch_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "Failed to retrieve credit balance"})
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
	// Strict JSON decoding via the shared helper. The S-L2 guard
	// (DisallowUnknownFields) is now centralized in decodeStrict, which
	// also rejects trailing data — closing the parser-divergence gap the
	// original S-L2 patch left open. See web/handlers/decode.go for the
	// full security rationale.
	var req checkoutSessionRequest
	if err := decodeStrict(r, &req); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("checkout_decode_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		}
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "invalid payload"})
		return
	}
	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc, h.Deps.Logger)
	out, err := cs.CreateCheckoutSession(r.Context(), billing.CheckoutRequest{UserID: userID, Credits: req.Credits, Currency: req.Currency})
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("checkout_session_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		}
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: "Failed to create checkout session"})
		return
	}
	renderJSON(w, http.StatusCreated, out)
}

func (h *BillingHandlers) Reconcile(w http.ResponseWriter, r *http.Request) {
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
	// Strict JSON decoding via the shared helper — see decode.go.
	var req reconcileRequest
	if err := decodeStrict(r, &req); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Warn("reconcile_decode_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		}
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "invalid payload"})
		return
	}
	if req.SessionID == "" {
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{Code: http.StatusUnprocessableEntity, Message: "invalid payload"})
		return
	}
	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc, h.Deps.Logger)
	if err := cs.Reconcile(r.Context(), req.SessionID, userID); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("reconcile_failed",
				slog.String("session_id", req.SessionID),
				slog.String("user_id", userID),
				slog.Any("error", err),
			)
		}
		renderJSON(w, http.StatusNotFound, models.APIError{Code: http.StatusNotFound, Message: "session not found or does not belong to user"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BillingHandlers) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if h.Deps.BillingSvc == nil {
		h.Deps.Logger.Error("billing_svc_nil_in_webhook_handler",
			slog.String("path", r.URL.Path), slog.String("method", r.Method))
		w.WriteHeader(http.StatusNotFound)
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		h.Deps.Logger.Error("webhook_payload_read_failed",
			slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	h.Deps.Logger.Debug("webhook_received", slog.Int("payload_length", len(payload)), slog.Bool("signature_present", sig != ""))

	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc, h.Deps.Logger)
	code, err := cs.HandleWebhook(r.Context(), payload, sig)
	if err != nil {
		h.Deps.Logger.Error("webhook_processing_failed",
			slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
	}
	h.Deps.Logger.Debug("webhook_response", slog.Int("code", code))
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

	// Parse pagination params (page-based, unified across all endpoints).
	page, limit, offset, err := parsePagination(r, 50)
	if err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	// Optional transaction type filter. Empty string means "no filter".
	// Reject unknown values with 400 instead of silently ignoring so clients
	// get feedback when they send a typo like ?type=purchases (plural).
	typeFilter := r.URL.Query().Get("type")
	if !webservices.IsAllowedBillingHistoryType(typeFilter) {
		renderJSON(w, http.StatusBadRequest, models.APIError{
			Code:    http.StatusBadRequest,
			Message: "invalid type filter (allowed: purchase, consumption, bonus, refund, adjustment)",
		})
		return
	}

	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc, h.Deps.Logger)
	resp, err := cs.GetBillingHistory(r.Context(), userID, page, limit, offset, typeFilter)
	if err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("billing_history_fetch_failed",
				slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
		}
		renderJSON(w, http.StatusInternalServerError, models.APIError{Code: http.StatusInternalServerError, Message: "Failed to retrieve billing history"})
		return
	}
	renderJSON(w, http.StatusOK, resp)
}
