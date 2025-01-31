// Package database provides access and utility functions to interact with the database.
// This includes methods to create, read, update, and delete records in various tables.
package database

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteAccountParams_validate(t *testing.T) {
	tests := []struct {
		name    string
		d       *DeleteAccountParams
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "success - valid input",
			d: &DeleteAccountParams{
				ID:           123,
				DeletionType: DeletionTypeSoft,
				AccountStatus: lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE,
			},
			wantErr: false,
		},
		{
			name: "failure - zero account ID",
			d: &DeleteAccountParams{
				ID:           0,
				DeletionType: DeletionTypeSoft,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.d.validate(); (err != nil) != tt.wantErr {
				t.Errorf("DeleteAccountParams.validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDb_DeleteAccount(t *testing.T) {
	// Create test accounts
	validAccount := testutils.GenerateRandomizedAccount()

	type args struct {
		ctx    context.Context
		params *DeleteAccountParams
		setup  func(t *testing.T) (*lead_scraper_servicev1.Account, error)
		clean  func(t *testing.T, id uint64)
	}

	tests := []struct {
		name     string
		args     args
		wantErr  bool
		errType  error
		validate func(t *testing.T, id uint64)
	}{
		{
			name:    "[success scenario] - delete existing account",
			wantErr: false,
			args: args{
				ctx: context.Background(),
				params: &DeleteAccountParams{
					DeletionType: DeletionTypeSoft,
				},
				setup: func(t *testing.T) (*lead_scraper_servicev1.Account, error) {
					// Create the account first
					acct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)
					require.Equal(t, validAccount.Email, acct.Email)
					return acct, nil
				},
				clean: func(t *testing.T, id uint64) {
					// Verify account no longer exists
					_, err := conn.GetAccount(context.Background(), &GetAccountInput{
						ID:       id,
					})
					require.Error(t, err)
				},
			},
			validate: func(t *testing.T, id uint64) {
				// Verify account no longer exists
				_, err := conn.GetAccount(context.Background(), &GetAccountInput{
					ID:       id,
				})
				require.Error(t, err)
			},
		},
		{
			name:    "[failure scenario] - zero account ID",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				params: &DeleteAccountParams{
					ID:           0,
					DeletionType: DeletionTypeSoft,
				},
			},
		},
		{
			name:    "[failure scenario] - non-existent account",
			wantErr: true,
			errType: ErrAccountDoesNotExist,
			args: args{
				ctx: context.Background(),
				params: &DeleteAccountParams{
					ID:           999999,
					DeletionType: DeletionTypeSoft,
					AccountStatus: lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE,
				},
			},
		},
		{
			name:    "[failure scenario] - context timeout",
			wantErr: true,
			args: args{
				ctx: func() context.Context {
					ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
					defer cancel()
					time.Sleep(2 * time.Millisecond)
					return ctx
				}(),
				params: &DeleteAccountParams{
					ID:           1,
					DeletionType: DeletionTypeSoft,
				},
			},
		},
		{
			name:    "[failure scenario] - delete already deleted account",
			wantErr: true,
			errType: ErrAccountDoesNotExist,
			args: args{
				ctx: context.Background(),
				params: &DeleteAccountParams{
					DeletionType: DeletionTypeSoft,
				},
				setup: func(t *testing.T) (*lead_scraper_servicev1.Account, error) {
					// Create and then delete an account
					acct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)

					// Delete the account
					err = conn.DeleteAccount(context.Background(), &DeleteAccountParams{
						ID:           acct.Id,
						DeletionType: DeletionTypeSoft,
						AccountStatus: acct.AccountStatus,
					})
					require.NoError(t, err)

					return acct, nil
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id uint64
			var err error

			// Run setup if provided
			if tt.args.setup != nil {
				createdAcct, err := tt.args.setup(t)
				require.NoError(t, err)
				if createdAcct != nil {
					tt.args.params.ID = createdAcct.Id
					tt.args.params.AccountStatus = createdAcct.AccountStatus
				}
			}

			// Cleanup after test
			defer func() {
				if tt.args.clean != nil {
					tt.args.clean(t, id)
				}
			}()

			err = conn.DeleteAccount(tt.args.ctx, tt.args.params)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)

			if tt.validate != nil {
				tt.validate(t, id)
			}
		})
	}
}

func TestDb_DeleteAccount_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	numAccounts := 100
	accounts := make([]*lead_scraper_servicev1.Account, numAccounts)

	// Create multiple accounts
	for i := 0; i < numAccounts; i++ {
		mockAccount := testutils.GenerateRandomizedAccount()
		createdAcct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
			Account:  mockAccount,
			OrgID:    "test-org",
			TenantID: "test-tenant",
		})
		require.NoError(t, err)
		require.NotNil(t, createdAcct)
		accounts[i] = createdAcct
	}

	// Delete accounts concurrently
	var wg sync.WaitGroup
	errors := make(chan error, numAccounts)

	for _, acct := range accounts {
		wg.Add(1)
		go func(account *lead_scraper_servicev1.Account) {
			defer wg.Done()
			err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
				ID:           account.Id,
				AccountStatus: account.AccountStatus,
				DeletionType: DeletionTypeSoft,
			})
			if err != nil {
				errors <- err
			}
		}(acct)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Error during stress test: %v", err)
	}

	// Verify all accounts are deleted
	for _, acct := range accounts {
		_, err := conn.GetAccount(context.Background(), &GetAccountInput{
			ID:       acct.Id,
		})
		require.Error(t, err)
	}
}

func TestDeleteAccountParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		d       *DeleteAccountParams
		wantErr bool
	}{
		{
			name: "success - valid params",
			d: &DeleteAccountParams{
				ID:           123,
				AccountStatus: lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE,
				DeletionType: DeletionTypeSoft,
			},
			wantErr: false,
		},
		{
			name: "failure - zero account ID",
			d: &DeleteAccountParams{
				ID:           0,
				AccountStatus: lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE,
				DeletionType: DeletionTypeSoft,
			},
			wantErr: true,
		},
		{
			name:    "failure - nil params",
			d:       nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.d.validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDb_DeleteAccountByEmail(t *testing.T) {
	// Create test accounts
	validAccount := testutils.GenerateRandomizedAccount()

	type args struct {
		ctx      context.Context
		email    string
		orgID    string
		tenantID string
		setup    func(t *testing.T) (*lead_scraper_servicev1.Account, error)
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
		errType error
	}{
		{
			name:    "[success scenario] - delete existing account by email",
			wantErr: false,
			args: args{
				ctx:      context.Background(),
				email:    validAccount.Email,
				orgID:    "test-org",
				tenantID: "test-tenant",
				setup: func(t *testing.T) (*lead_scraper_servicev1.Account, error) {
					// Create the account first
					acct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)
					require.Equal(t, validAccount.Email, acct.Email)
					return acct, nil
				},
			},
		},
		{
			name:    "[failure scenario] - empty email",
			wantErr: true,
			args: args{
				ctx:      context.Background(),
				email:    "",
				orgID:    "test-org",
				tenantID: "test-tenant",
			},
		},
		{
			name:    "[failure scenario] - non-existent email",
			wantErr: true,
			errType: ErrFailedToGetAccountByEmail,
			args: args{
				ctx:      context.Background(),
				email:    "nonexistent@example.com",
				orgID:    "test-org",
				tenantID: "test-tenant",
			},
		},
		{
			name:    "[failure scenario] - context timeout",
			wantErr: true,
			args: args{
				ctx: func() context.Context {
					ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
					defer cancel()
					time.Sleep(2 * time.Millisecond)
					return ctx
				}(),
				email:    validAccount.Email,
				orgID:    "test-org",
				tenantID: "test-tenant",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var account *lead_scraper_servicev1.Account
			var err error

			// Run setup if provided
			if tt.args.setup != nil {
				account, err = tt.args.setup(t)
				require.NoError(t, err)
				if account != nil {
					tt.args.email = account.Email
				}
			}

			err = conn.DeleteAccountByEmail(tt.args.ctx, tt.args.email)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)

			// Verify account no longer exists
			_, err = conn.GetAccountByEmail(context.Background(), tt.args.email)
			require.Error(t, err)
		})
	}
}

func TestDb_BatchDeleteAccounts(t *testing.T) {
	// Create multiple test accounts
	numAccounts := 10
	accounts := make([]*lead_scraper_servicev1.Account, 0, numAccounts)
	accountIDs := make([]uint64, 0, numAccounts)

	for i := 0; i < numAccounts; i++ {
		mockAccount := testutils.GenerateRandomizedAccount()
		createdAcct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
			Account:  mockAccount,
			OrgID:    "test-org",
			TenantID: "test-tenant",
		})
		require.NoError(t, err)
		require.NotNil(t, createdAcct)
		accounts = append(accounts, createdAcct)
		accountIDs = append(accountIDs, createdAcct.Id)
	}

	type args struct {
		ctx    context.Context
		params *BatchDeleteAccountsParams
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
		errType error
	}{
		{
			name:    "[success scenario] - delete all accounts in batches",
			wantErr: false,
			args: args{
				ctx: context.Background(),
				params: &BatchDeleteAccountsParams{
					IDs:          accountIDs,
					DeletionType: DeletionTypeSoft,
					BatchSize:    3,
				},
			},
		},
		{
			name:    "[failure scenario] - nil params",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx:    context.Background(),
				params: nil,
			},
		},
		{
			name:    "[failure scenario] - empty IDs",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				params: &BatchDeleteAccountsParams{
					IDs:          []uint64{},
					DeletionType: DeletionTypeSoft,
					BatchSize:    3,
				},
			},
		},
		{
			name:    "[failure scenario] - invalid batch size",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				params: &BatchDeleteAccountsParams{
					IDs:          accountIDs,
					DeletionType: DeletionTypeSoft,
					BatchSize:    0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := conn.BatchDeleteAccounts(tt.args.ctx, tt.args.params)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)

			// Verify accounts are deleted
			if tt.args.params != nil {
				for _, id := range tt.args.params.IDs {
					_, err := conn.GetAccount(context.Background(), &GetAccountInput{
						ID:       id,
					})
					require.Error(t, err)
					assert.ErrorIs(t, err, ErrAccountDoesNotExist)
				}
			}
		})
	}
}

func TestDb_BatchDeleteAccounts_LargeBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large batch test in short mode")
	}

	// Create a large number of test accounts
	numAccounts := 100
	accounts := make([]*lead_scraper_servicev1.Account, 0, numAccounts)
	accountIDs := make([]uint64, 0, numAccounts)

	for i := 0; i < numAccounts; i++ {
		mockAccount := testutils.GenerateRandomizedAccount()
		createdAcct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
			Account:  mockAccount,
			OrgID:    "test-org",
			TenantID: "test-tenant",
		})
		require.NoError(t, err)
		require.NotNil(t, createdAcct)
		accounts = append(accounts, createdAcct)
		accountIDs = append(accountIDs, createdAcct.Id)
	}

	// Delete accounts in batches
	err := conn.BatchDeleteAccounts(context.Background(), &BatchDeleteAccountsParams{
		IDs:          accountIDs,
		DeletionType: DeletionTypeSoft,
		BatchSize:    10,
	})
	require.NoError(t, err)

	// Verify all accounts are deleted
	for _, id := range accountIDs {
		_, err := conn.GetAccount(context.Background(), &GetAccountInput{
			ID:       id,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrAccountDoesNotExist)
	}
}
