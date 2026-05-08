package handlers

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	svix "github.com/svix/svix-webhooks/go"

	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// Provisioner is the narrow interface the Clerk webhook handler depends on.
// Lets tests inject a fake without spinning up the full provisioning chain.
// Implemented by *services.UserProvisioning.
type Provisioner interface {
	Provision(ctx context.Context, userID, email string) (postgres.User, error)
}

// ClerkWebhookHandler verifies and dispatches Svix-signed Clerk webhooks.
// Routed at POST /webhooks/clerk in web/web.go (outside the auth middleware
// — authentication is via Svix signature, not user JWT).
// H4: verifiers holds one entry per signing secret so the previous secret
// remains valid during key rotation.
type ClerkWebhookHandler struct {
	db           *sql.DB
	verifiers    []*svix.Webhook
	provisioning Provisioner
	logger       *slog.Logger
}

// clerkWebhookMinKeyBytes is the minimum byte length of the decoded HMAC key.
// A shorter key (e.g. the bare "whsec_" prefix that decodes to zero bytes) is
// computationally valid in Go's HMAC but produces forgeable signatures.
const clerkWebhookMinKeyBytes = 16

// NewClerkWebhookHandler validates every signing secret and constructs the
// handler. signingSecrets must be non-empty; the active secret should be
// first so it is tried first on every request.
//
// H4: accepts multiple secrets so the previous secret stays valid during
// rotation. All secrets are validated at startup; the handler tries each in
// order until one succeeds or all fail.
//
// The caller (web/web.go) should treat a returned error as a fatal startup
// error when any CLERK_WEBHOOK_SIGNING_SECRET* var is set; when both vars
// are empty the caller skips construction entirely (route is not mounted).
func NewClerkWebhookHandler(db *sql.DB, signingSecrets []string, provisioning Provisioner, logger *slog.Logger) (*ClerkWebhookHandler, error) {
	if len(signingSecrets) == 0 {
		return nil, errors.New("clerk_webhook: no signing secrets provided")
	}

	verifiers := make([]*svix.Webhook, 0, len(signingSecrets))
	for i, secret := range signingSecrets {
		if secret == "" {
			return nil, fmt.Errorf("clerk_webhook: signing secret %d is empty", i)
		}

		wh, err := svix.NewWebhook(secret)
		if err != nil {
			return nil, fmt.Errorf("clerk_webhook: invalid signing secret %d: %w", i, err)
		}

		// Defense-in-depth: svix.NewWebhook accepts a bare "whsec_" prefix which
		// decodes to a zero-length HMAC key — HMAC over an empty key is
		// computationally valid, producing forgeable signatures. Reject any secret
		// whose decoded body is shorter than clerkWebhookMinKeyBytes.
		rawKey, decErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		if decErr != nil || len(rawKey) < clerkWebhookMinKeyBytes {
			return nil, fmt.Errorf("clerk_webhook: signing secret %d decodes to too few bytes (min %d)", i, clerkWebhookMinKeyBytes)
		}

		verifiers = append(verifiers, wh)
	}

	return &ClerkWebhookHandler{
		db:           db,
		verifiers:    verifiers,
		provisioning: provisioning,
		logger:       logger,
	}, nil
}

// clerkWebhookTimeout caps total processing time per delivery. Well under
// Svix's 15s delivery timeout. Stripe customer creation (post-Provision)
// is ~500ms p99, so 10s leaves wide margin.
const clerkWebhookTimeout = 10 * time.Second

// maxLoggedEventTypeLen caps the event-type string included in log records.
// Prevents log injection / oversized fields when an unexpected event type
// is received (e.g. from a replayed or crafted delivery).
const maxLoggedEventTypeLen = 64

// clerkEvent is the minimal envelope read from the verified body. Other
// envelope fields (object, timestamp, instance_id) exist but we don't read
// them — Svix has already enforced the timestamp via signature verification,
// and we dispatch on Type alone.
type clerkEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// clerkUserCreatedData is the subset of user.created data we read. Phone /
// Web3 / external-account arrays are unmarshalled as []json.RawMessage so we
// can len()-check them for diagnostic logging without committing to their
// schemas (Clerk extends those over time). They let the missing-email log
// distinguish "expected empty" (Web3/phone signup) from "real anomaly"
// (email-flow user with no email) so Loki alerts fire only on true bugs.
type clerkUserCreatedData struct {
	ID                    string `json:"id"`
	PrimaryEmailAddressID string `json:"primary_email_address_id"`
	EmailAddresses        []struct {
		ID           string `json:"id"`
		EmailAddress string `json:"email_address"`
	} `json:"email_addresses"`
	PhoneNumbers     []json.RawMessage `json:"phone_numbers"`
	Web3Wallets      []json.RawMessage `json:"web3_wallets"`
	ExternalAccounts []json.RawMessage `json:"external_accounts"`
}

func (h *ClerkWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// H7: recover from any panic inside this handler and return 500 so Svix
	// retries the delivery. The middleware Recovery() is not in the webhook
	// chain, so we add our own guard here.
	defer func() {
		if rec := recover(); rec != nil {
			h.logger.Error("clerk_webhook_panic_recovered",
				slog.Any("panic", rec))
			// Best-effort: response may already be partially written.
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}()

	ctx, cancel := context.WithTimeout(r.Context(), clerkWebhookTimeout)
	defer cancel()

	// 1. Read raw body BEFORE any parsing — signature is computed over bytes.
	// Body size is capped by the MaxBodySize(64KiB) middleware in the webhook
	// chain (web/web.go baseWebhookMws); no inner MaxBytesReader is needed.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn("clerk_webhook_body_read_failed", slog.Any("error", err))
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			// 413 Request Entity Too Large — distinct from a malformed read so
			// alerting/dashboards that watch for "payload too large" can fire.
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "body read", http.StatusBadRequest)
		}
		return
	}

	// 2. Verify signature. H4: try each verifier in order (active first, then
	// previous secrets during rotation). svix-webhooks/go v1.93 takes raw bytes
	// + http.Header.
	var verifyErr error
	for _, v := range h.verifiers {
		if err := v.Verify(body, r.Header); err == nil {
			verifyErr = nil
			break
		} else {
			verifyErr = err
		}
	}
	if verifyErr != nil {
		// M17: log the real client IP, not the proxy's RemoteAddr.
		sourceIP := "unknown"
		if ip := auth.ClientIP(r); ip != nil {
			sourceIP = ip.String()
		}
		h.logger.Warn("clerk_webhook_signature_invalid",
			slog.String("source_ip", sourceIP),
			slog.Any("error", verifyErr))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	msgID := r.Header.Get("svix-id")
	if msgID == "" {
		// Verify already requires this header; defensive.
		http.Error(w, "missing svix-id", http.StatusUnauthorized)
		return
	}

	// 3. Claim the event in the dedupe table. We persist the dedupe row
	// BEFORE parsing JSON, accepting that a malformed-body redelivery would
	// see a dedupe hit and return 200 — but malformed JSON from Clerk is
	// not a real-world failure mode for a stable provider, and persisting
	// first eliminates double-processing on the success path.
	claimed, err := h.claimEvent(ctx, msgID)
	if err != nil {
		h.logger.Error("clerk_webhook_dedupe_failed", slog.String("svix_id", msgID), slog.Any("error", err))
		http.Error(w, "transient", http.StatusServiceUnavailable)
		return
	}
	if !claimed {
		h.logger.Info("clerk_webhook_duplicate_ignored", slog.String("svix_id", msgID))
		w.WriteHeader(http.StatusOK)
		return
	}

	// 4. Parse the event envelope.
	var evt clerkEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		h.logger.Warn("clerk_webhook_malformed_json", slog.String("svix_id", msgID), slog.Any("error", err))
		http.Error(w, "malformed", http.StatusBadRequest)
		return
	}

	// 5. Dispatch on event type. Only user.created today (D1).
	switch evt.Type {
	case "user.created":
		if h.handleUserCreated(ctx, msgID, evt.Data) {
			// Provisioning failed; dedupe row was released so Svix will retry.
			http.Error(w, "transient — please retry", http.StatusServiceUnavailable)
			return
		}
	default:
		// Acknowledge and ignore — returning 4xx would make Svix retry
		// forever for events we don't subscribe to.
		// M4: cap the logged event type to prevent oversized / injected log fields.
		loggedType := evt.Type
		if len(loggedType) > maxLoggedEventTypeLen {
			loggedType = loggedType[:maxLoggedEventTypeLen]
		}
		h.logger.Info("clerk_webhook_event_ignored",
			slog.String("svix_id", msgID), slog.String("type", loggedType))
	}

	w.WriteHeader(http.StatusOK)
}

// handleUserCreated parses and provisions the user.created payload.
// Returns true if provisioning failed AND the dedupe row was released
// (caller should respond 503 to trigger Svix redelivery).
// Returns false on success or on validation-only skips (no retry needed).
func (h *ClerkWebhookHandler) handleUserCreated(ctx context.Context, msgID string, raw json.RawMessage) (retry bool) {
	var data clerkUserCreatedData
	if err := json.Unmarshal(raw, &data); err != nil {
		h.logger.Warn("clerk_webhook_user_created_data_invalid",
			slog.String("svix_id", msgID), slog.Any("error", err))
		return false
	}

	email := primaryEmailFromClerkPayload(data)

	// Defense-in-depth observability: a documented Clerk invariant says
	// email_addresses[] and primary_email_address_id are consistent (primary
	// is set iff at least one entry exists). Clerk's "Send Example" fixture
	// violates this, but real production traffic should not. A distinct log
	// key lets ops set a Loki alert that fires ONLY when Clerk ships a
	// genuinely malformed payload — not for legitimate empty-email cases.
	if len(data.EmailAddresses) == 0 && data.PrimaryEmailAddressID != "" {
		h.logger.Warn("clerk_webhook_email_invariant_violated",
			slog.String("svix_id", msgID),
			slog.String("user_id", data.ID),
			slog.String("primary_email_address_id", data.PrimaryEmailAddressID))
	}

	if data.ID == "" || email == "" {
		// Loki/Grafana alert pattern (suggested):
		//   {app="brezelscraper-backend"} |= "clerk_webhook_user_created_missing_fields"
		//     | json | phone_numbers_count="0" | web3_wallets_count="0"
		//     | external_accounts_count="0"
		// → fires ONLY when there's no email AND no other identity signal,
		// i.e. Clerk gave us an unprovisionable user (real bug). Web3/phone/
		// external-only signups are filtered out — those are expected per
		// Clerk's auth strategies and acked silently by this handler.
		h.logger.Warn("clerk_webhook_user_created_missing_fields",
			slog.String("svix_id", msgID),
			slog.String("user_id", data.ID),
			slog.Bool("has_email", email != ""),
			slog.Int("phone_numbers_count", len(data.PhoneNumbers)),
			slog.Int("web3_wallets_count", len(data.Web3Wallets)),
			slog.Int("external_accounts_count", len(data.ExternalAccounts)),
		)
		return false
	}

	if _, err := h.provisioning.Provision(ctx, data.ID, email); err != nil {
		h.logger.Error("clerk_webhook_provisioning_failed",
			slog.String("svix_id", msgID),
			slog.String("user_id", data.ID),
			slog.Any("error", err))

		// H1: On provisioning failure post-claim, the dedupe row would block
		// Svix redelivery from retrying. Delete the row so the next delivery
		// can re-attempt. Best-effort: a deletion failure is logged but not
		// returned (we already failed provisioning).
		if _, delErr := h.db.ExecContext(ctx, `DELETE FROM processed_webhook_events WHERE event_id = $1`, msgID); delErr != nil {
			h.logger.Error("clerk_webhook_dedupe_release_failed",
				slog.String("svix_id", msgID), slog.Any("error", delErr))
		}
		return true
	}

	h.logger.Info("clerk_webhook_user_provisioned",
		slog.String("svix_id", msgID), slog.String("user_id", data.ID))
	return false
}

// claimEvent inserts a row into processed_webhook_events and returns true
// if this caller is the first to see the message ID. The table's processed_at,
// processing_result, and metadata columns all have DB defaults so we only
// set the two columns that are provider-specific.
func (h *ClerkWebhookHandler) claimEvent(ctx context.Context, msgID string) (bool, error) {
	// event_type is hardcoded rather than derived from the parsed body
	// because we claim BEFORE parsing JSON (so a redelivery of the same
	// svix-id sees a dedupe hit even if the first parse failed). The Clerk
	// Dashboard webhook is configured to send only user.created events, so
	// any other event type that slips through (e.g., manual replay of an
	// ignored event) gets deduped under this label — a harmless operational
	// imprecision rather than a correctness issue.
	const q = `
		INSERT INTO processed_webhook_events (event_id, event_type)
		VALUES ($1, 'clerk.user.created')
		ON CONFLICT (event_id) DO NOTHING
	`
	res, err := h.db.ExecContext(ctx, q, msgID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// primaryEmailFromClerkPayload picks the primary email from a webhook
// user.created data payload. Falls back to the first email if no primary
// is set; returns "" if the user has no email addresses.
func primaryEmailFromClerkPayload(d clerkUserCreatedData) string {
	if d.PrimaryEmailAddressID != "" {
		for _, ea := range d.EmailAddresses {
			if ea.ID == d.PrimaryEmailAddressID {
				return ea.EmailAddress
			}
		}
	}
	if len(d.EmailAddresses) > 0 {
		return d.EmailAddresses[0].EmailAddress
	}
	return ""
}
