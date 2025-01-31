package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
)

// UpdateAccount updates an existing account in the database
func (db *Db) UpdateAccount(ctx context.Context, account *lead_scraper_servicev1.Account) (*lead_scraper_servicev1.Account, error) {
	if account == nil {
		return nil, ErrInvalidInput
	}

	// Validate email
	if account.Email == "" {
		return nil, ErrInvalidInput
	}

	// check that the account exists
	existing, err := db.GetAccount(ctx, &GetAccountInput{
		ID: account.Id,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	if existing == nil {
		return nil, fmt.Errorf("account not found")
	}

	// Set the account status to match the existing account
	account.AccountStatus = existing.AccountStatus

	result, err := lead_scraper_servicev1.DefaultStrictUpdateAccount(ctx, account, db.Client.Engine)
	if err != nil {
		db.Logger.Error("failed to update account", zap.Error(err))
		return nil, fmt.Errorf("failed to update account: %w", err)
	}

	return result, nil
} 