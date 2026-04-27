package handlers

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/notify"
	"github.com/gosom/google-maps-scraper/web/auth"
	webservices "github.com/gosom/google-maps-scraper/web/services"
)

// SupportHandlers handles support-related API requests.
type SupportHandlers struct {
	Deps   Dependencies
	Sender notify.Sender
}

// supportRequest is the JSON body from the frontend.
// All user-identifying fields are fetched server-side — NEVER trust client-provided userId/email.
type supportRequest struct {
	Category string `json:"category" validate:"required,oneof=bug feature billing account other"`
	Subject  string `json:"subject"  validate:"max=200"`
	Message  string `json:"message"  validate:"required,min=10,max=5000"`
}

// sanitizeSubject strips control characters that could cause log injection
// or (in non-API email transports) header injection.
// Defense-in-depth: Resend's JSON API is structurally immune to header injection,
// but we sanitize anyway so the value is safe for any downstream use.
func sanitizeSubject(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\x00' || unicode.IsControl(r) {
			return -1 // strip
		}
		return r
	}, strings.TrimSpace(s))
}

// SubmitSupportRequest handles POST /api/v1/support.
func (h *SupportHandlers) SubmitSupportRequest(w http.ResponseWriter, r *http.Request) {
	// 1. Decode request body (strict: unknown fields rejected, trailing data rejected)
	var req supportRequest
	if err := decodeStrict(r, &req); err != nil {
		if h.Deps.Logger != nil {
			h.Deps.Logger.Error("support_decode_failed", slog.Any("error", err))
		}
		renderJSON(w, http.StatusUnprocessableEntity, models.APIError{
			Code:    http.StatusUnprocessableEntity,
			Message: "We couldn't process your message. Please check your input and try again.",
		})
		return
	}

	// 2. Validate with struct tags (enum allowlist on category, length limits)
	if err := validate.Struct(req); err != nil {
		renderJSON(w, http.StatusBadRequest, models.APIError{
			Code:    http.StatusBadRequest,
			Message: formatValidationErrors(err),
		})
		return
	}

	// 3. Extract authenticated user ID from context (set by auth middleware)
	userID, err := auth.GetUserID(r.Context())
	if err != nil || userID == "" {
		renderJSON(w, http.StatusUnauthorized, models.APIError{
			Code:    http.StatusUnauthorized,
			Message: "Please sign in to send a support request, or email support@brezel.ai directly.",
		})
		return
	}

	// 4. Fetch user data server-side (NEVER trust client-provided email/balance)
	var userEmail, creditBalance string
	if h.Deps.UserRepo != nil {
		user, err := h.Deps.UserRepo.GetByID(r.Context(), userID)
		if err != nil {
			if h.Deps.Logger != nil {
				h.Deps.Logger.Warn("support_user_lookup_failed",
					slog.String("user_id", userID),
					slog.Any("error", err),
				)
			}
			// Non-fatal: send the ticket anyway, just without enrichment
			userEmail = "unknown"
		} else {
			userEmail = user.Email
		}
	}

	if h.Deps.DB != nil && h.Deps.BillingSvc != nil {
		cs := webservices.NewCreditService(h.Deps.DB, h.Deps.BillingSvc, h.Deps.Logger)
		resp, err := cs.GetBalance(r.Context(), userID)
		if err == nil {
			creditBalance = resp.CreditBalance
		}
	}

	// 5. Sanitize inputs
	req.Subject = sanitizeSubject(req.Subject)
	req.Message = strings.TrimSpace(req.Message)

	// 6. Build enriched support request
	supportReq := notify.SupportRequest{
		Category:      req.Category,
		Subject:       req.Subject,
		Message:       req.Message,
		UserID:        userID,
		UserEmail:     userEmail,
		CreditBalance: creditBalance,
		UserAgent:     r.UserAgent(),
	}

	// 7. Send via configured sender (Resend or log fallback)
	if err := h.Sender.Send(r.Context(), supportReq); err != nil {
		internalError(w, h.Deps.Logger, err, "Something went wrong on our end. Please email support@brezel.ai directly and we'll help you right away.",
			slog.String("user_id", userID),
			slog.String("category", req.Category),
		)
		return
	}

	// 8. Log success (no message content — PII risk)
	if h.Deps.Logger != nil {
		h.Deps.Logger.Info("support_request_sent",
			slog.String("user_id", userID),
			slog.String("category", req.Category),
		)
	}

	renderJSON(w, http.StatusOK, map[string]bool{"success": true})
}
