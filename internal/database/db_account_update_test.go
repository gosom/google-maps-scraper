package database

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDb_UpdateAccount(t *testing.T) {
	// Create test accounts
	validAccount := testutils.GenerateRandomizedAccount()

	type args struct {
		ctx     context.Context
		account *lead_scraper_servicev1.Account
		setup   func(t *testing.T) (*lead_scraper_servicev1.Account, error)
		clean   func(t *testing.T, account *lead_scraper_servicev1.Account)
	}

	tests := []struct {
		name     string
		args     args
		wantErr  bool
		errType  error
		validate func(t *testing.T, account *lead_scraper_servicev1.Account)
	}{
		{
			name:    "[success scenario] - update existing account",
			wantErr: false,
			args: args{
				ctx: context.Background(),
				setup: func(t *testing.T) (*lead_scraper_servicev1.Account, error) {
					// Create the account first
					acct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)

					// Update some fields
					acct.Email = "updated@example.com"
					return acct, nil
				},
				clean: func(t *testing.T, account *lead_scraper_servicev1.Account) {
					err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
						ID:           account.Id,
						OrgID:        account.OrgId,
						TenantID:     account.TenantId,
						DeletionType: DeletionTypeSoft,
					})
					require.NoError(t, err)
				},
			},
			validate: func(t *testing.T, account *lead_scraper_servicev1.Account) {
				assert.NotNil(t, account)
				assert.Equal(t, "updated@example.com", account.Email)
				assert.Equal(t, "test-org", account.OrgId)
				assert.Equal(t, "test-tenant", account.TenantId)
			},
		},
		{
			name:    "[failure scenario] - nil account",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx:     context.Background(),
				account: nil,
			},
		},
		{
			name:    "[failure scenario] - non-existent account",
			wantErr: true,
			errType: ErrAccountDoesNotExist,
			args: args{
				ctx: context.Background(),
				account: &lead_scraper_servicev1.Account{
					Id:       999999,
					OrgId:    "test-org",
					TenantId: "test-tenant",
					Email:    "nonexistent@example.com",
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
				account: &lead_scraper_servicev1.Account{
					Id:       1,
					OrgId:    "test-org",
					TenantId: "test-tenant",
					Email:    "test@example.com",
				},
			},
		},
		{
			name:    "[failure scenario] - invalid email",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				setup: func(t *testing.T) (*lead_scraper_servicev1.Account, error) {
					// Create the account first
					acct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)

					// Set invalid email
					acct.Email = ""
					return acct, nil
				},
				clean: func(t *testing.T, account *lead_scraper_servicev1.Account) {
					err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
						ID:           account.Id,
						OrgID:        account.OrgId,
						TenantID:     account.TenantId,
						DeletionType: DeletionTypeSoft,
					})
					require.NoError(t, err)
				},
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
					tt.args.account = account
				}
			}

			// Cleanup after test
			defer func() {
				if tt.args.clean != nil && account != nil {
					tt.args.clean(t, account)
				}
			}()

			updatedAccount, err := conn.UpdateAccount(tt.args.ctx, tt.args.account)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, updatedAccount)

			if tt.validate != nil {
				tt.validate(t, updatedAccount)
			}
		})
	}
}

func TestDb_UpdateAccount_ConcurrentUpdates(t *testing.T) {
	// Create a test account
	validAccount := testutils.GenerateRandomizedAccount()
	createdAccount, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
		Account:  validAccount,
		OrgID:    "test-org",
		TenantID: "test-tenant",
	})
	require.NoError(t, err)
	require.NotNil(t, createdAccount)

	// Clean up after test
	defer func() {
		err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
			ID:           createdAccount.Id,
			OrgID:        createdAccount.OrgId,
			TenantID:     createdAccount.TenantId,
			DeletionType: DeletionTypeSoft,
		})
		require.NoError(t, err)
	}()

	// Perform concurrent updates
	numUpdates := 5
	var wg sync.WaitGroup
	errors := make(chan error, numUpdates)

	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			updatedAccount := *createdAccount
			updatedAccount.Email = fmt.Sprintf("updated%d@example.com", index)
			_, err := conn.UpdateAccount(context.Background(), &updatedAccount)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Error during concurrent updates: %v", err)
	}

	// Verify final state
	finalAccount, err := conn.GetAccount(context.Background(), &GetAccountInput{
		ID:       createdAccount.Id,
	})
	require.NoError(t, err)
	require.NotNil(t, finalAccount)
	assert.Contains(t, finalAccount.Email, "updated")
}
