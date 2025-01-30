// Package database provides access and utility functions to interact with the database.
// This includes methods to create, read, update, and delete records in various tables.
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/Vector/vector-leads-scraper/internal/constants"
	"github.com/go-playground/validator/v10"
	"go.uber.org/multierr"
	"gorm.io/gen/field"
)

var (
	// ErrAccountDoesNotExist is returned when attempting to operate on a non-existent account
	ErrAccountDoesNotExist = errors.New("account does not exist")
	// ErrFailedToDeleteAccount is returned when the account deletion operation fails
	ErrFailedToDeleteAccount = errors.New("failed to delete account")
)

type DeletionType string

const (
	DeletionTypeSoft DeletionType = "soft"
	DeletionTypeHard DeletionType = "hard"
)

// DeleteAccountParams holds the parameters for deleting an account
type DeleteAccountParams struct {
	ID       uint64 `validate:"required,gt=0"`
	OrgID    string `validate:"required"`
	TenantID string `validate:"required"`
	DeletionType DeletionType `validate:"required"`
}

func (d *DeleteAccountParams) validate() error {
	if err := validator.New(validator.WithRequiredStructEnabled()).Struct(d); err != nil {
		return multierr.Append(ErrInvalidInput, err)
	}
	return nil
}

// DeleteAccount deletes an account from the database based on the provided account ID.
// It validates the account ID, checks if the account exists, and performs a soft deletion
// by marking it as inactive in the database.
//
// Parameters:
//   - ctx: A context.Context for timeout and tracing control
//   - params: DeleteAccountParams containing the account ID and tenant information
//
// Returns:
//   - error: An error if the operation fails, or nil if successful
//
// Errors:
//   - Returns ErrInvalidInput if params validation fails
//   - Returns ErrAccountDoesNotExist if the account does not exist
//   - Returns error if database operations fail
func (db *Db) DeleteAccount(ctx context.Context, params *DeleteAccountParams) error {
	// ensure the db operation executes within the specified timeout
	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	if params == nil {
		return ErrInvalidInput
	}

	// validate the input parameters
	if err := params.validate(); err != nil {
		return err
	}

	// Check if account exists
	account, err := db.GetAccount(ctx, &GetAccountInput{ID: params.ID, OrgID: params.OrgID, TenantID: params.TenantID})
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	if account == nil {
		return ErrAccountDoesNotExist
	}

	// Verify account belongs to the specified org and tenant
	if account.OrgId != params.OrgID || account.TenantId != params.TenantID {
		return fmt.Errorf("account does not belong to the specified organization or tenant")
	}

	// perform soft deletion
	b := db.QueryOperator.AccountORM
	queryRef := b.WithContext(ctx)			
	if params.DeletionType == DeletionTypeSoft {
		queryRef = queryRef.Where(b.Id.Eq(params.ID)).Select(field.AssociationFields)
	} else {
		queryRef = queryRef.Where(b.Id.Eq(params.ID)).Unscoped().Select(field.AssociationFields)
	}

	res, err := queryRef.Delete()
	if err != nil {	
			return err
		}
		if res.RowsAffected == 0 {
		return ErrFailedToDeleteAccount
	}

	return nil
}

// DeleteAccountByEmail deletes an account based on email address
func (db *Db) DeleteAccountByEmail(ctx context.Context, email string, orgID string, tenantID string) error {
	if email == constants.EMPTY {
		return fmt.Errorf("email cannot be empty")
	}

	// Get account by email
	account, err := db.GetAccountByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("failed to get account by email: %w", err)
	}
	if account == nil {
		return ErrAccountDoesNotExist
	}

	// Delete using the standard delete function
	return db.DeleteAccount(ctx, &DeleteAccountParams{
		ID:       account.Id,
		OrgID:    orgID,
		TenantID: tenantID,
	})
} 