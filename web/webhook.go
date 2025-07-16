package web

import (
	"io"
	"net/http"

	"github.com/gosom/google-maps-scraper/subscription"
	stripeClient "github.com/gosom/google-maps-scraper/stripe"
)

// WebhookHandler handles Stripe webhook events
type WebhookHandler struct {
	stripeClient        stripeClient.Client
	subscriptionService subscription.ServiceInterface
	webhookSecret       string
	logger              Logger
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(
	stripeClient stripeClient.Client,
	subscriptionService subscription.ServiceInterface,
	webhookSecret string,
	logger Logger,
) *WebhookHandler {
	return &WebhookHandler{
		stripeClient:        stripeClient,
		subscriptionService: subscriptionService,
		webhookSecret:       webhookSecret,
		logger:              logger,
	}
}

// HandleStripeWebhook handles POST /webhooks/stripe
func (h *WebhookHandler) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	h.logger.Printf("POST %s - Received webhook request", r.URL.Path)

	// Read the raw body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Printf("ERROR %s - Failed to read webhook body: %v", r.URL.Path, err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Get the signature header
	signature := r.Header.Get("Stripe-Signature")
	if signature == "" {
		h.logger.Printf("ERROR %s - Missing Stripe-Signature header", r.URL.Path)
		http.Error(w, "Missing Stripe-Signature header", http.StatusBadRequest)
		return
	}

	// Verify webhook signature
	event, err := h.stripeClient.VerifyWebhook(body, signature, h.webhookSecret)
	if err != nil {
		h.logger.Printf("ERROR %s - Failed to verify webhook signature: %v", r.URL.Path, err)
		http.Error(w, "Invalid webhook signature", http.StatusBadRequest)
		return
	}

	// Log event for debugging
	h.logger.Printf("POST %s - Verified webhook event: %s (ID: %s)", r.URL.Path, event.Type, event.ID)

	// Process the webhook event
	err = h.subscriptionService.ProcessWebhookEvent(r.Context(), event)
	if err != nil {
		h.logger.Printf("ERROR %s - Failed to process webhook event %s (ID: %s): %v", r.URL.Path, event.Type, event.ID, err)
		http.Error(w, "Failed to process webhook event", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
	h.logger.Printf("SUCCESS %s - Successfully processed webhook event %s (ID: %s)", r.URL.Path, event.Type, event.ID)
}