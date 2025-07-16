package web

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gosom/google-maps-scraper/subscription"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// SubscriptionHandler handles subscription-related HTTP requests
type SubscriptionHandler struct {
	subscriptionService subscription.ServiceInterface
	logger              Logger
}

// Logger interface for logging
type Logger interface {
	Printf(format string, v ...interface{})
}

// NewSubscriptionHandler creates a new subscription handler
func NewSubscriptionHandler(subscriptionService subscription.ServiceInterface, logger Logger) *SubscriptionHandler {
	return &SubscriptionHandler{
		subscriptionService: subscriptionService,
		logger:              logger,
	}
}

// CreateSubscriptionRequest represents a subscription creation request
type CreateSubscriptionRequest struct {
	PlanID string `json:"plan_id"`
}

// CreateSubscriptionResponse represents a subscription creation response
type CreateSubscriptionResponse struct {
	SubscriptionID string `json:"subscription_id"`
	Status         string `json:"status"`
	ClientSecret   string `json:"client_secret,omitempty"`
}

// apiCreateSubscription handles POST /api/v1/subscription/create
func (h *SubscriptionHandler) apiCreateSubscription(w http.ResponseWriter, r *http.Request) {
	h.logger.Printf("POST %s - Received create subscription request", r.URL.Path)

	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		h.logger.Printf("ERROR %s - User not authenticated: %v", r.URL.Path, err)
		h.renderError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	var req CreateSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Printf("ERROR %s - user: %s - Invalid request body: %v", r.URL.Path, userID, err)
		h.renderError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.PlanID == "" {
		h.logger.Printf("ERROR %s - user: %s - Plan ID is required", r.URL.Path, userID)
		h.renderError(w, http.StatusBadRequest, "Plan ID is required")
		return
	}

	h.logger.Printf("POST %s - user: %s - Creating subscription for plan: %s", r.URL.Path, userID, req.PlanID)

	userSub, err := h.subscriptionService.CreateSubscription(r.Context(), userID, req.PlanID)
	if err != nil {
		h.logger.Printf("ERROR %s - user: %s - Failed to create subscription for plan %s: %v", r.URL.Path, userID, req.PlanID, err)
		h.renderError(w, http.StatusInternalServerError, "Failed to create subscription")
		return
	}

	response := CreateSubscriptionResponse{
		SubscriptionID: userSub.StripeSubscriptionID,
		Status:         userSub.Status,
		ClientSecret:   userSub.ClientSecret,
	}

	h.renderJSON(w, http.StatusCreated, response)
	h.logger.Printf("SUCCESS %s - user: %s - Created subscription for plan: %s", r.URL.Path, userID, req.PlanID)
}

// apiGetSubscriptionStatus handles GET /api/v1/subscription/status
func (h *SubscriptionHandler) apiGetSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	h.logger.Printf("GET %s - Received get subscription status request", r.URL.Path)

	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		h.logger.Printf("ERROR %s - User not authenticated: %v", r.URL.Path, err)
		h.renderError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	h.logger.Printf("GET %s - user: %s - Fetching subscription status", r.URL.Path, userID)

	status, err := h.subscriptionService.GetUserSubscriptionStatus(r.Context(), userID)
	if err != nil {
		h.logger.Printf("ERROR %s - user: %s - Failed to get subscription status: %v", r.URL.Path, userID, err)
		h.renderError(w, http.StatusInternalServerError, "Failed to get subscription status")
		return
	}

	h.renderJSON(w, http.StatusOK, status)
	h.logger.Printf("SUCCESS %s - user: %s - Retrieved subscription status", r.URL.Path, userID)
}

// apiCancelSubscription handles POST /api/v1/subscription/cancel
func (h *SubscriptionHandler) apiCancelSubscription(w http.ResponseWriter, r *http.Request) {
	h.logger.Printf("POST %s - Received cancel subscription request", r.URL.Path)

	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		h.logger.Printf("ERROR %s - User not authenticated: %v", r.URL.Path, err)
		h.renderError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	h.logger.Printf("POST %s - user: %s - Canceling subscription", r.URL.Path, userID)

	err = h.subscriptionService.CancelSubscription(r.Context(), userID)
	if err != nil {
		h.logger.Printf("ERROR %s - user: %s - Failed to cancel subscription: %v", r.URL.Path, userID, err)
		h.renderError(w, http.StatusInternalServerError, "Failed to cancel subscription")
		return
	}

	response := map[string]string{
		"message": "Subscription canceled successfully",
	}

	h.renderJSON(w, http.StatusOK, response)
	h.logger.Printf("SUCCESS %s - user: %s - Subscription canceled", r.URL.Path, userID)
}

// apiGetPlans handles GET /api/v1/subscription/plans
func (h *SubscriptionHandler) apiGetPlans(w http.ResponseWriter, r *http.Request) {
	h.logger.Printf("GET %s - Received get plans request", r.URL.Path)

	plans, err := h.subscriptionService.GetPlans(r.Context())
	if err != nil {
		h.logger.Printf("ERROR %s - Failed to get plans: %v", r.URL.Path, err)
		h.renderError(w, http.StatusInternalServerError, "Failed to get plans")
		return
	}

	h.renderJSON(w, http.StatusOK, plans)
	h.logger.Printf("SUCCESS %s - Retrieved %d subscription plans", r.URL.Path, len(plans))
}

// BillingPortalRequest represents a billing portal request
type BillingPortalRequest struct {
	ReturnURL string `json:"return_url"`
}

// BillingPortalResponse represents a billing portal response
type BillingPortalResponse struct {
	URL string `json:"url"`
}

// apiCreateBillingPortal handles POST /api/v1/subscription/portal
func (h *SubscriptionHandler) apiCreateBillingPortal(w http.ResponseWriter, r *http.Request) {
	h.logger.Printf("POST %s - Received create billing portal request", r.URL.Path)

	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		h.logger.Printf("ERROR %s - User not authenticated: %v", r.URL.Path, err)
		h.renderError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	var req BillingPortalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Printf("ERROR %s - user: %s - Invalid request body: %v", r.URL.Path, userID, err)
		h.renderError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.ReturnURL == "" {
		h.logger.Printf("ERROR %s - user: %s - Return URL is required", r.URL.Path, userID)
		h.renderError(w, http.StatusBadRequest, "Return URL is required")
		return
	}

	h.logger.Printf("POST %s - user: %s - Creating billing portal session", r.URL.Path, userID)

	portalURL, err := h.subscriptionService.CreateBillingPortalSession(r.Context(), userID, req.ReturnURL)
	if err != nil {
		h.logger.Printf("ERROR %s - user: %s - Failed to create billing portal: %v", r.URL.Path, userID, err)
		h.renderError(w, http.StatusInternalServerError, "Failed to create billing portal")
		return
	}

	response := BillingPortalResponse{
		URL: portalURL,
	}

	h.renderJSON(w, http.StatusOK, response)
	h.logger.Printf("SUCCESS %s - user: %s - Created billing portal session", r.URL.Path, userID)
}

// renderJSON renders a JSON response
func (h *SubscriptionHandler) renderJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

// renderError renders an error response
func (h *SubscriptionHandler) renderError(w http.ResponseWriter, code int, message string) {
	errorResponse := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	h.renderJSON(w, code, errorResponse)
}

// RegisterRoutes registers subscription routes with the router
func (h *SubscriptionHandler) RegisterRoutes(router *mux.Router) {
	// All subscription routes require authentication
	router.HandleFunc("/subscription/create", h.apiCreateSubscription).Methods(http.MethodPost)
	router.HandleFunc("/subscription/status", h.apiGetSubscriptionStatus).Methods(http.MethodGet)
	router.HandleFunc("/subscription/cancel", h.apiCancelSubscription).Methods(http.MethodPost)
	router.HandleFunc("/subscription/plans", h.apiGetPlans).Methods(http.MethodGet)
	router.HandleFunc("/subscription/portal", h.apiCreateBillingPortal).Methods(http.MethodPost)
}
