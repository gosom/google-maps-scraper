package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/user"
	"github.com/gosom/google-maps-scraper/postgres"
)

// SignupBonusAmount is the credit amount granted to new users on signup ($1.00)
const SignupBonusAmount = 1.0

// AuthMiddleware handles Clerk authentication and adds user info to the request context
type AuthMiddleware struct {
	db       *sql.DB
	userAPI  *user.Client
	userRepo postgres.UserRepository
	logger   *slog.Logger
}

// ContextKey is used to store user information in the request context
type ContextKey string

const (
	// UserIDKey is the context key for storing the user ID
	UserIDKey ContextKey = "user_id"
	// AuthHeaderName is the name of the authentication header
	AuthHeaderName = "Authorization"
	// DevUserHeaderName allows local integration tests to bypass Clerk auth when explicitly enabled.
	// This MUST remain opt-in via BRAZA_DEV_AUTH_BYPASS=1 to avoid production misuse.
	DevUserHeaderName = "X-Braza-Dev-User"
)

// NewAuthMiddleware creates a new AuthMiddleware
func NewAuthMiddleware(clerkAPIKey string, db *sql.DB, userRepo postgres.UserRepository, logger *slog.Logger) (*AuthMiddleware, error) {
	// Configure Clerk SDK with the provided secret key
	clerk.SetKey(clerkAPIKey)

	return &AuthMiddleware{
		db: db,
		userAPI: user.NewClient(&clerk.ClientConfig{
			BackendConfig: clerk.BackendConfig{
				Key: clerk.String(clerkAPIKey),
			},
		}),
		userRepo: userRepo,
		logger:   logger,
	}, nil
}

// Authenticate is the middleware function for authentication
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	extractToken := func(r *http.Request) string {
		// Prefer Authorization header
		authHeader := strings.TrimSpace(r.Header.Get(AuthHeaderName))
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}
		// Fallback to __session cookie (used by Clerk)
		if sessionCookie, err := r.Cookie("__session"); err == nil && sessionCookie.Value != "" {
			return sessionCookie.Value
		}
		return ""
	}

	// Clerk middleware: verify token (header or cookie) and attach session claims
	clerkAuth := clerkhttp.RequireHeaderAuthorization(
		clerkhttp.AuthorizationJWTExtractor(extractToken),
	)

	clerkHandler := clerkAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Retrieve verified session claims from context
		claims, ok := clerk.SessionClaimsFromContext(r.Context())
		if !ok || claims == nil || claims.Subject == "" {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		userID := claims.Subject

		// Check if user exists in our database
		userRecord, err := m.userRepo.GetByID(r.Context(), userID)
		if err != nil {
			// If user doesn't exist, fetch from Clerk and create locally
			clerkUser, err := m.userAPI.Get(r.Context(), userID)
			if err != nil {
				m.logger.Error("failed_to_retrieve_user_from_clerk", slog.String("user_id", userID), slog.Any("error", err))
				http.Error(w, "Failed to retrieve user information", http.StatusInternalServerError)
				return
			}

			// Get the primary email
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

			userRecord = postgres.User{
				ID:    userID,
				Email: email,
			}
			if err := m.userRepo.Create(r.Context(), &userRecord); err != nil {
				m.logger.Error("failed_to_create_user", slog.String("user_id", userID), slog.Any("error", err))
				http.Error(w, "Failed to create user record: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Grant signup bonus
			if err := m.grantSignupBonus(r.Context(), userID); err != nil {
				m.logger.Error("failed_to_grant_signup_bonus", slog.String("user_id", userID), slog.Any("error", err))
				// Don't block signup if bonus fails
			} else {
				m.logger.Info("signup_bonus_granted", slog.Float64("amount", SignupBonusAmount), slog.String("user_id", userID))
			}
		}

		// Add user ID to request context
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}))

	// Optional dev bypass for local integration tests. This avoids depending on
	// Clerk connectivity from the test runner environment.
	if strings.TrimSpace(os.Getenv("BRAZA_DEV_AUTH_BYPASS")) == "1" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if devUserID := strings.TrimSpace(r.Header.Get(DevUserHeaderName)); devUserID != "" {
				if m.logger != nil {
					m.logger.Warn("dev_auth_bypass", slog.String("user_id", devUserID))
				}
				ctx := context.WithValue(r.Context(), UserIDKey, devUserID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			clerkHandler.ServeHTTP(w, r)
		})
	}

	return clerkHandler
}

// grantSignupBonus awards the signup bonus credit to a newly created user.
// It is idempotent: if a bonus transaction already exists for this user, it no-ops.
func (m *AuthMiddleware) grantSignupBonus(ctx context.Context, userID string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Idempotency check: skip if this user already received a signup bonus.
	// Uses FOR UPDATE to prevent concurrent grants via row-level locking.
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

	// Lock the user row and get current balance
	var currentBalance float64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1 FOR UPDATE", userID).Scan(&currentBalance)
	if err != nil {
		return err
	}

	newBalance := currentBalance + SignupBonusAmount

	// Update user credit balance
	_, err = tx.ExecContext(ctx, `
		UPDATE users 
		SET credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
		    updated_at = NOW()
		WHERE id = $2`, SignupBonusAmount, userID)
	if err != nil {
		return err
	}

	// Insert credit transaction for audit
	_, err = tx.ExecContext(ctx, `
		INSERT INTO credit_transactions (user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
		VALUES ($1, 'bonus', $2, $3, $4, 'Signup bonus', 'signup_bonus', 'system')`,
		userID, SignupBonusAmount, currentBalance, newBalance)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetUserID extracts the user ID from the request context
func GetUserID(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(UserIDKey).(string)
	if !ok {
		return "", errors.New("user not authenticated")
	}
	return userID, nil
}
