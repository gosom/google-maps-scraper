package handlers

import (
	"encoding/json"
	"io"
	"log"
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
		log.Printf("ERROR: BillingSvc is nil in webhook handler")
		w.WriteHeader(http.StatusNotFound)
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: Failed to read webhook payload: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	log.Printf("DEBUG: Webhook received - payload length: %d, signature present: %t", len(payload), sig != "")

	cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc)
	code, err := cs.HandleWebhook(r.Context(), payload, sig)
	if err != nil {
		log.Printf("ERROR: Webhook processing failed: %v", err)
	}
	log.Printf("DEBUG: Webhook response code: %d", code)
	w.WriteHeader(code)
}
