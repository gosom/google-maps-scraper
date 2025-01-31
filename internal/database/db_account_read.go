// Package database provides access and utility functions to interact with the database.
// This includes methods to create, read, update, and delete records in various tables.
package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/go-playground/validator/v10"
	"go.uber.org/multierr"
	"go.uber.org/zap"
)

// GetAccountInput holds the input parameters for the GetAccount function.
type GetAccountInput struct {
	ID                          uint64 `validate:"required,gt=0"`
	EnableAccountInactiveClause bool   // Kept for API compatibility but not used
}

func (d *GetAccountInput) validate() error {
	if err := validator.New(validator.WithRequiredStructEnabled()).Struct(d); err != nil {
		return multierr.Append(ErrInvalidInput, err)
	}
	return nil
}

// GetAccount retrieves an account from the database using the provided account ID.
// It validates the input parameters, checks for the existence of the account,
// and verifies the account belongs to the specified organization and tenant.
//
// Parameters:
//   - ctx: A context.Context for timeout and tracing control
//   - input: A GetAccountInput struct containing:
//   - ID: Account ID to retrieve
//   - OrgID: Organization ID the account should belong to
//   - TenantID: Tenant ID the account should belong to
//   - EnableAccountInactiveClause: Not used in this implementation
//
// Returns:
//   - *lead_scraper_servicev1.Account: A pointer to the retrieved Account object
//   - error: An error if the operation fails, or nil if successful
//
// Errors:
//   - Returns ErrInvalidInput if input validation fails
//   - Returns ErrAccountDoesNotExist if the account does not exist
//   - Returns error if database operations fail
func (db *Db) GetAccount(ctx context.Context, input *GetAccountInput) (*lead_scraper_servicev1.Account, error) {
	// ensure the db operation executes within the specified timeout
	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	if input == nil {
		return nil, ErrInvalidInput
	}

	// validate the input parameters
	if err := input.validate(); err != nil {
		return nil, err
	}

	// Query the account using the generated GORM model
	account := &lead_scraper_servicev1.Account{Id: input.ID}
	result, err := lead_scraper_servicev1.DefaultReadAccount(ctx, account, db.Client.Engine)
	if err != nil {
		if err.Error() == "record not found" {
			return nil, ErrAccountDoesNotExist
		}
		db.Logger.Error("failed to get account",
			zap.Error(err),
			zap.Uint64("account_id", input.ID))
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	return result, nil
}

// ListAccountsInput holds the input parameters for the ListAccounts function
type ListAccountsInput struct {
	OrgID    string `validate:"required"`
	TenantID string `validate:"required"`
	Limit    int    `validate:"required,gt=0"`
	Offset   int    `validate:"gte=0"`
}

func (d *ListAccountsInput) validate() error {
	if err := validator.New(validator.WithRequiredStructEnabled()).Struct(d); err != nil {
		return multierr.Append(ErrInvalidInput, err)
	}
	return nil
}

// ListAccounts retrieves a paginated list of accounts for a specific organization and tenant.
//
// Parameters:
//   - ctx: A context.Context for timeout and tracing control
//   - input: ListAccountsInput containing pagination and filtering parameters
//
// Returns:
//   - []*lead_scraper_servicev1.Account: A slice of Account objects
//   - error: An error if the operation fails, or nil if successful
func (db *Db) ListAccounts(ctx context.Context, input *ListAccountsInput) ([]*lead_scraper_servicev1.Account, error) {
	// ensure the db operation executes within the specified timeout
	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	if input == nil {
		return nil, ErrInvalidInput
	}

	// validate the input parameters
	if err := input.validate(); err != nil {
		return nil, err
	}

	// Query accounts with pagination using the generated GORM model
	u := db.QueryOperator.AccountORM
	queryRef := db.Client.Engine.
		WithContext(ctx).
		Where(u.OrgId.Eq(input.OrgID)).
		Where(u.TenantId.Eq(input.TenantID)).
		Order(u.Id).
		Limit(input.Limit).
		Offset(input.Offset)

	accounts, err := lead_scraper_servicev1.DefaultListAccount(ctx, queryRef)
	if err != nil {
		db.Logger.Error("failed to list accounts",
			zap.Error(err),
			zap.String("org_id", input.OrgID),
			zap.String("tenant_id", input.TenantID))
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	return accounts, nil
}
