package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/user"
	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/postgres"
)

// SignupBonusAmount is the credit amount granted to new users on signup ($1.00).
const SignupBonusAmount = 1.0

// AuthMiddleware handles Clerk authentication and adds user info to the request context.
type AuthMiddleware struct {
	db           *sql.DB
	userAPI      *user.Client
	userRepo     postgres.UserRepository
	apiKeyRepo   models.APIKeyRepository // nil if API key auth is not configured
	serverSecret []byte                  // HMAC secret for API key lookup hashes
	logger       *slog.Logger
}

// ContextKey is used to store user information in the request context.
type ContextKey string

const (
	// UserIDKey is the context key for storing the user ID.
	UserIDKey ContextKey = "user_id"
	// APIKeyIDKey is the context key for the API key UUID (set only for API key auth).
	APIKeyIDKey ContextKey = "api_key_id"
	// APIKeyPlanTierKey is kept for rate-limiter compatibility; always empty with the
	// full implementation (no plan tiers in api_keys schema).
	APIKeyPlanTierKey ContextKey = "api_key_plan_tier"
	// UserRoleKey is the context key for storing the user's RBAC role.
	UserRoleKey ContextKey = "user_role"
	// AuthHeaderName is the name of the authentication header.
	AuthHeaderName = "Authorization"
)

// NewAuthMiddleware creates a new AuthMiddleware.
// apiKeyRepo and serverSecret may be nil/empty; when either is nil/empty, API key
// authentication is disabled and all Bearer tokens are validated as Clerk JWTs.
func NewAuthMiddleware(clerkAPIKey string, db *sql.DB, userRepo postgres.UserRepository, apiKeyRepo models.APIKeyRepository, serverSecret []byte, logger *slog.Logger) (*AuthMiddleware, error) {
	clerk.SetKey(clerkAPIKey)

	return &AuthMiddleware{
		db: db,
		userAPI: user.NewClient(&clerk.ClientConfig{
			BackendConfig: clerk.BackendConfig{
				Key: clerk.String(clerkAPIKey),
			},
		}),
		userRepo:     userRepo,
		apiKeyRepo:   apiKeyRepo,
		serverSecret: serverSecret,
		logger:       logger,
	}, nil
}

// writeUnauthorized writes a 401 JSON response.
func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    http.StatusUnauthorized,
		"message": msg,
	})
}

// extractBearerToken returns the raw token from the Authorization header,
// or the __session cookie value as a fallback (Clerk browser sessions).
func extractBearerToken(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get(AuthHeaderName))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[len("Bearer "):])
	}
	if sessionCookie, err := r.Cookie("__session"); err == nil && sessionCookie.Value != "" {
		return sessionCookie.Value
	}
	return ""
}

// Authenticate is the middleware function for authentication.
// It dispatches to API key auth when the Bearer token has the APIKeyPrefix,
// and falls back to Clerk JWT validation otherwise.
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return m.authenticateRequest(next)
}

// authenticateRequest builds the actual auth handler (API key or Clerk JWT).
func (m *AuthMiddleware) authenticateRequest(next http.Handler) http.Handler {
	clerkAuth := clerkhttp.RequireHeaderAuthorization(
		clerkhttp.AuthorizationJWTExtractor(extractBearerToken),
	)

	clerkHandler := clerkAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := clerk.SessionClaimsFromContext(r.Context())
		if !ok || claims == nil || claims.Subject == "" {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}
		userID := claims.Subject

		dbUser, err := m.userRepo.GetByID(r.Context(), userID)
		if err != nil {
			// User not found — auto-provision from Clerk
			clerkUser, err := m.userAPI.Get(r.Context(), userID)
			if err != nil {
				m.logger.Error("failed_to_retrieve_user_from_clerk", slog.String("user_id", userID), slog.Any("error", err))
				http.Error(w, "Failed to retrieve user information", http.StatusInternalServerError)
				return
			}

			var email string
			if clerkUser.PrimaryEmailAddressID != nil {
				primaryID := *clerkUser.PrimaryEmailAddressID
				for _, emailAddr := range clerkUser.EmailAddresses {
					if emailAddr.ID == primaryID {
						email = emailAddr.EmailAddress
						break
					}
				}
			} else if len(clerkUser.EmailAddresses) > 0 {
				email = clerkUser.EmailAddresses[0].EmailAddress
			}
			if email == "" {
				m.logger.Error("user_has_no_email", slog.String("user_id", userID))
				http.Error(w, "User has no email address", http.StatusBadRequest)
				return
			}

			newUser := postgres.User{ID: userID, Email: email}
			if err := m.userRepo.Create(r.Context(), &newUser); err != nil {
				slog.Error("failed to create user record",
					slog.String("user_id", userID), slog.String("path", r.URL.Path), slog.String("method", r.Method), slog.Any("error", err))
				http.Error(w, "Failed to create user record", http.StatusInternalServerError)
				return
			}
			if err := m.grantSignupBonus(r.Context(), userID); err != nil {
				m.logger.Error("failed_to_grant_signup_bonus", slog.String("user_id", userID), slog.Any("error", err))
			} else {
				m.logger.Info("signup_bonus_granted", slog.Float64("amount", SignupBonusAmount), slog.String("user_id", userID))
			}

			// Newly created users always have the default role ("user").
			// Set explicitly so the in-memory struct matches the DB default,
			// rather than relying on GetUserRole()'s empty-string fallback.
			newUser.Role = models.RoleUser
			dbUser = newUser
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, UserIDKey, userID)
		ctx = context.WithValue(ctx, UserRoleKey, dbUser.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get(AuthHeaderName))
		var bearerToken string
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			bearerToken = strings.TrimSpace(authHeader[len("Bearer "):])
		}

		// Also accept X-API-Key header as an alternative to Authorization: Bearer.
		// This is a common pattern (AWS API Gateway, Anthropic) and helps users
		// whose Authorization header is occupied by a proxy or integration tool.
		if bearerToken == "" {
			if xKey := strings.TrimSpace(r.Header.Get("X-API-Key")); xKey != "" {
				bearerToken = xKey
			}
		}

		if m.apiKeyRepo != nil && len(m.serverSecret) > 0 && strings.HasPrefix(bearerToken, APIKeyPrefix) {
			userID, keyID, err := ValidateAPIKey(r.Context(), bearerToken, m.serverSecret, m.apiKeyRepo)
			if err != nil {
				// Small delay on failed attempts to slow brute-force attacks.
				time.Sleep(100 * time.Millisecond)
				m.logger.Warn("api_key_auth_rejected",
					slog.String("reason", err.Error()),
					slog.String("path", r.URL.Path),
				)
				writeUnauthorized(w, "invalid or revoked API key")
				return
			}

			// Record API key usage asynchronously to avoid adding latency.
			// Extract IP before launching goroutine — accessing the request
			// after ServeHTTP returns is unsafe because the server may recycle it.
			ip := clientIP(r)
			go func() {
				if err := m.apiKeyRepo.UpdateLastUsed(context.Background(), keyID, ip); err != nil {
					m.logger.Warn("api_key_update_last_used_failed", slog.String("key_id", keyID), slog.Any("error", err))
				}
			}()

			ctx := r.Context()
			ctx = context.WithValue(ctx, UserIDKey, userID)
			ctx = context.WithValue(ctx, APIKeyIDKey, keyID)
			if apiUser, err := m.userRepo.GetByID(r.Context(), userID); err == nil {
				ctx = context.WithValue(ctx, UserRoleKey, apiUser.Role)
			} else {
				// Role lookup failed (transient DB error, etc.). Default to "user"
				// via GetUserRole() — safe fallback that denies admin access.
				m.logger.Warn("api_key_role_lookup_failed",
					slog.String("user_id", userID),
					slog.String("key_id", keyID),
					slog.Any("error", err),
				)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		clerkHandler.ServeHTTP(w, r)
	})
}

// clientIP extracts the client IP from the request, preferring X-Forwarded-For
// when present (common behind reverse proxies). Only the first entry is trusted.
func clientIP(r *http.Request) net.IP {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.SplitN(xff, ",", 2)[0]
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip
		}
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	return nil
}

// GetAPIKeyID extracts the API key UUID from the request context.
// Returns an empty string when the request was not authenticated via an API key.
func GetAPIKeyID(ctx context.Context) string {
	id, _ := ctx.Value(APIKeyIDKey).(string)
	return id
}

// GetAPIKeyPlanTier returns the plan tier for rate-limiter compatibility.
// Always returns an empty string with the full API key implementation (no plan tiers).
func GetAPIKeyPlanTier(ctx context.Context) string {
	tier, _ := ctx.Value(APIKeyPlanTierKey).(string)
	return tier
}

// grantSignupBonus awards the signup bonus credit to a newly created user.
// It is idempotent: if a bonus transaction already exists for this user, it no-ops.
func (m *AuthMiddleware) grantSignupBonus(ctx context.Context, userID string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			m.logger.Error("rollback_failed", slog.Any("error", rbErr))
		}
	}()

	var alreadyGranted bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM credit_transactions WHERE user_id = $1 AND reference_id = 'signup_bonus' AND reference_type = 'system' FOR UPDATE)",
		userID).Scan(&alreadyGranted)
	if err != nil {
		return err
	}
	if alreadyGranted {
		m.logger.Info("signup_bonus_already_granted", slog.String("user_id", userID))
		return nil
	}

	var currentBalance float64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&currentBalance)
	if err != nil {
		return err
	}

	newBalance := currentBalance + SignupBonusAmount

	_, err = tx.ExecContext(ctx, `
		UPDATE users
		SET credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
		    updated_at = NOW()
		WHERE id = $2`, SignupBonusAmount, userID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO credit_transactions (id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
		VALUES ($1, $2, 'bonus', $3, $4, $5, 'Signup bonus', 'signup_bonus', 'system')`,
		uuid.Must(uuid.NewV7()).String(), userID, SignupBonusAmount, currentBalance, newBalance)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetUserID extracts the user ID from the request context.
func GetUserID(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(UserIDKey).(string)
	if !ok || userID == "" {
		return "", errors.New("user not authenticated")
	}
	return userID, nil
}

// GetUserRole extracts the user's RBAC role from the request context.
// Returns "user" (the default role) when no role has been set.
func GetUserRole(ctx context.Context) string {
	role, _ := ctx.Value(UserRoleKey).(string)
	if role == "" {
		return "user"
	}
	return role
}
