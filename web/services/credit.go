package services

import (
	"context"
	"database/sql"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/models"
)

// CreditService encapsulates credit-related operations.
type CreditService struct {
	db      *sql.DB
	billing *billing.Service
}

func NewCreditService(db *sql.DB, billingSvc *billing.Service) *CreditService {
	return &CreditService{db: db, billing: billingSvc}
}

// GetBalance returns credit balance info for a user.
func (s *CreditService) GetBalance(ctx context.Context, userID string) (models.CreditBalanceResponse, error) {
	var resp models.CreditBalanceResponse
	if s.db == nil {
		return resp, sql.ErrConnDone
	}
	const q = `SELECT id, credit_balance::text, total_credits_purchased::text FROM users WHERE id=$1`
	if err := s.db.QueryRowContext(ctx, q, userID).Scan(&resp.UserID, &resp.CreditBalance, &resp.TotalCreditsPurchased); err != nil {
		// If no row, return zero balance for authenticated user
		resp = models.CreditBalanceResponse{UserID: userID, CreditBalance: "0", TotalCreditsPurchased: "0"}
		return resp, nil
	}
	return resp, nil
}

// CreateCheckoutSession delegates to billing service.
func (s *CreditService) CreateCheckoutSession(ctx context.Context, req billing.CheckoutRequest) (billing.CheckoutResponse, error) {
	return s.billing.CreateCheckoutSession(ctx, req)
}

// Reconcile delegates to billing service.
func (s *CreditService) Reconcile(ctx context.Context, sessionID string) error {
	return s.billing.ReconcileSession(ctx, sessionID)
}

// HandleWebhook delegates to billing service.
func (s *CreditService) HandleWebhook(ctx context.Context, payload []byte, signature string) (int, error) {
	return s.billing.HandleWebhook(ctx, payload, signature)
}

// GetBillingHistory returns paginated credit transaction history for a user.
func (s *CreditService) GetBillingHistory(ctx context.Context, userID string, limit, offset int) (models.BillingHistoryResponse, error) {
	var resp models.BillingHistoryResponse
	resp.Limit = limit
	resp.Offset = offset

	if s.db == nil {
		return resp, sql.ErrConnDone
	}

	// Get total count
	const countQ = `SELECT COUNT(*) FROM credit_transactions WHERE user_id = $1`
	if err := s.db.QueryRowContext(ctx, countQ, userID).Scan(&resp.Total); err != nil {
		return resp, err
	}

	resp.HasMore = offset+limit < resp.Total

	// Get transactions
	const q = `SELECT id, type, amount::text, balance_before::text, balance_after::text, description,
		reference_id, reference_type, created_at
		FROM credit_transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, q, userID, limit, offset)
	if err != nil {
		return resp, err
	}
	defer rows.Close()

	resp.Transactions = make([]models.CreditTransaction, 0)
	for rows.Next() {
		var t models.CreditTransaction
		if err := rows.Scan(&t.ID, &t.Type, &t.Amount, &t.BalanceBefore, &t.BalanceAfter,
			&t.Description, &t.ReferenceID, &t.ReferenceType, &t.CreatedAt); err != nil {
			return resp, err
		}
		resp.Transactions = append(resp.Transactions, t)
	}

	return resp, rows.Err()
}
