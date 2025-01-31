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
	AccountStatus               lead_scraper_servicev1.Account_AccountStatus `validate:"omitempty"`
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

	// Query the account by id and account status. NOTE: we only include the account status if it is not unspecifed
	var account lead_scraper_servicev1.AccountORM
	query := db.Client.Engine.WithContext(ctx)

	if input.AccountStatus != lead_scraper_servicev1.Account_ACCOUNT_STATUS_UNSPECIFIED {
		query = query.Where("account_status = ?", input.AccountStatus.String())
	}

	if input.EnableAccountInactiveClause {
		query = query.Where("account_status IN (?)", []string{
			lead_scraper_servicev1.Account_ACCOUNT_STATUS_SUSPENDED.String(),
		})
	}

	err := query.Where("id = ?", input.ID).First(&account).Error
	if err != nil {
		if err.Error() == "record not found" {
			return nil, ErrAccountDoesNotExist
		}
		db.Logger.Error("failed to get account",
			zap.Error(err),
			zap.Uint64("account_id", input.ID))
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	// convert to protobuf
	acctPb, err := account.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert account to protobuf: %w", err)
	}

	return &acctPb, nil
}

// ListAccountsInput holds the input parameters for the ListAccounts function
type ListAccountsInput struct {
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

	// Query accounts with pagination using GORM directly
	var accounts []*lead_scraper_servicev1.AccountORM
	if err := db.Client.Engine.
		WithContext(ctx).
		Order("id").
		Limit(input.Limit).
		Offset(input.Offset).
		Find(&accounts).Error; err != nil {
		db.Logger.Error("failed to list accounts",
			zap.Error(err))
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	// Convert ORM objects to protobuf objects
	result := make([]*lead_scraper_servicev1.Account, len(accounts))
	for i, account := range accounts {
		pb, err := account.ToPB(ctx)
		if err != nil {
			db.Logger.Error("failed to convert account to protobuf",
				zap.Error(err))
			return nil, fmt.Errorf("failed to convert account to protobuf: %w", err)
		}
		result[i] = &pb
	}

	return result, nil
}
