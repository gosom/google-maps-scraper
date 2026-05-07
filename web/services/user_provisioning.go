// Package services hosts cross-cutting orchestration that is reused by
// multiple HTTP handlers. UserProvisioning is the single source of truth
// for "make sure a Postgres users row exists for this Clerk user, with any
// one-time signup side effects applied." Called by both the Clerk
// /webhooks/clerk handler (eager path) and the auth middleware's
// lazy-provisioning fallback (when a request arrives before the webhook).
package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/postgres"
)

// SignupBonusAmount is the credit amount granted to new users on signup ($2.00).
const SignupBonusAmount = 2.0

// UserProvisioning is a service that ensures a users row exists for a given
// Clerk user ID and email, and grants one-time signup side effects.
type UserProvisioning struct {
	db         *sql.DB
	userRepo   postgres.UserRepository
	billingSvc *billing.Service // nil-safe (test/no-Stripe builds)
	logger     *slog.Logger
}

// NewUserProvisioning creates a new UserProvisioning service.
// billingSvc may be nil (in test builds or when Stripe is not configured);
// when nil, Stripe customer creation is skipped.
func NewUserProvisioning(
	db *sql.DB,
	userRepo postgres.UserRepository,
	billingSvc *billing.Service,
	logger *slog.Logger,
) *UserProvisioning {
	return &UserProvisioning{db: db, userRepo: userRepo, billingSvc: billingSvc, logger: logger}
}

// Provision ensures a users row exists for the given Clerk user ID and
// email, and grants one-time signup side effects (Stripe customer, signup
// bonus). Safe to call concurrently and repeatedly: every step is
// idempotent. Returns the canonical user row from the DB.
//
// Order is intentional: idempotent INSERT first, then re-read to get the
// canonical row regardless of which concurrent caller actually inserted
// it. Stripe customer creation and the signup bonus follow; their failures
// are non-fatal — the same contract the inlined chain in auth.go used.
func (s *UserProvisioning) Provision(ctx context.Context, userID, email string) (postgres.User, error) {
	if userID == "" || email == "" {
		return postgres.User{}, errors.New("user_provisioning: userID and email are required")
	}

	// Step 1 — idempotent insert. ON CONFLICT (id) DO NOTHING (postgres/user.go)
	// means concurrent callers all return nil; the loser simply does not insert.
	newUser := postgres.User{ID: userID, Email: email}
	if err := s.userRepo.Create(ctx, &newUser); err != nil {
		return postgres.User{}, fmt.Errorf("user_provisioning: create user: %w", err)
	}

	// Step 2 — re-read to get the canonical row regardless of which caller
	// actually inserted it. Avoids relying on the in-memory newUser struct,
	// whose fields are only correct by coincidence (both callers build it
	// from the same Clerk user object today, but that may diverge later).
	dbUser, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return postgres.User{}, fmt.Errorf("user_provisioning: re-read after create: %w", err)
	}

	// Step 3 — lazy Stripe customer creation. Idempotent: short-circuits
	// if dbUser.StripeCustomerID is already set; otherwise uses a per-user
	// Stripe idempotency key. Non-fatal: log and continue.
	if s.billingSvc != nil {
		existingCustomerID := ""
		if dbUser.StripeCustomerID != nil {
			existingCustomerID = *dbUser.StripeCustomerID
		}
		if _, err := s.billingSvc.EnsureStripeCustomer(ctx, dbUser.ID, dbUser.Email, existingCustomerID, s.userRepo); err != nil {
			s.logger.Error("stripe_customer_ensure_failed_on_provision",
				slog.String("user_id", dbUser.ID), slog.Any("error", err))
		}
	}

	// Step 4 — signup bonus. Idempotent via reference_id='signup_bonus'
	// unique check inside the function. Non-fatal: log and continue.
	granted, err := s.grantSignupBonus(ctx, dbUser.ID)
	switch {
	case err != nil:
		s.logger.Error("failed_to_grant_signup_bonus",
			slog.String("user_id", dbUser.ID), slog.Any("error", err))
	case granted:
		s.logger.Info("signup_bonus_granted",
			slog.Float64("amount", SignupBonusAmount), slog.String("user_id", dbUser.ID))
		// No log on the false/nil path — grantSignupBonus already logged signup_bonus_already_granted internally if it wants to.
	}

	// Newly created users always default to RoleUser; align in-memory struct
	// so callers don't need a second GetByID just to read role.
	if dbUser.Role == "" {
		dbUser.Role = models.RoleUser
	}
	return dbUser, nil
}

// grantSignupBonus grants a one-time signup credit bonus to the user.
// Returns (true, nil) when the bonus was actually granted, (false, nil) when
// it was already granted (idempotent no-op), and (false, err) on any failure.
func (s *UserProvisioning) grantSignupBonus(ctx context.Context, userID string) (granted bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			s.logger.Error("rollback_failed", slog.Any("error", rbErr))
		}
	}()

	// Step 1: lock the user row first. Matches the lock order used by billing/
	// payment paths (users → credit_transactions), avoiding lock-order inversion
	// deadlocks if a concurrent payment touches both tables.
	var currentBalance float64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1 FOR UPDATE`,
		userID).Scan(&currentBalance); err != nil {
		return false, fmt.Errorf("user_provisioning: lock user balance: %w", err)
	}

	// Step 2: idempotency check. The partial unique index idx_unique_signup_bonus
	// (migration 000022) is the real correctness guarantee; this EXISTS just
	// short-circuits the no-op case so we don't UPDATE the balance to the same
	// value and INSERT a doomed row.
	var alreadyGranted bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM credit_transactions WHERE user_id = $1 AND reference_id = 'signup_bonus' AND reference_type = 'system')`,
		userID).Scan(&alreadyGranted); err != nil {
		return false, fmt.Errorf("user_provisioning: check existing bonus: %w", err)
	}
	if alreadyGranted {
		return false, nil // not granted (already had one) — no error
	}

	// Step 3: UPDATE balance and INSERT bonus tx.
	newBalance := currentBalance + SignupBonusAmount
	if _, err := tx.ExecContext(ctx, `
    UPDATE users
    SET credit_balance = COALESCE(credit_balance, 0) + $1::numeric,
        updated_at = NOW()
    WHERE id = $2`, SignupBonusAmount, userID); err != nil {
		return false, fmt.Errorf("user_provisioning: update balance: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
    INSERT INTO credit_transactions (id, user_id, type, amount, balance_before, balance_after, description, reference_id, reference_type)
    VALUES ($1, $2, 'bonus', $3, $4, $5, 'Signup bonus', 'signup_bonus', 'system')`,
		uuid.Must(uuid.NewV7()).String(), userID, SignupBonusAmount, currentBalance, newBalance); err != nil {
		return false, fmt.Errorf("user_provisioning: insert bonus tx: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("user_provisioning: commit bonus tx: %w", err)
	}
	return true, nil
}
