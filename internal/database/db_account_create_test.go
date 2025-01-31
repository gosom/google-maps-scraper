// Package database provides access and utility functions to interact with the database.
// This includes methods to create, read, update, and delete records in various tables.
package database

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAccountInput_validate(t *testing.T) {
	tests := []struct {
		name    string
		d       *CreateAccountInput
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "success - valid input",
			d: &CreateAccountInput{
				Account:  testutils.GenerateRandomizedAccount(),
				OrgID:    "test-org",
				TenantID: "test-tenant",
			},
			wantErr: false,
		},
		{
			name: "failure - nil input",
			d: nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.d.validate(); (err != nil) != tt.wantErr {
				t.Errorf("CreateAccountInput.validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDb_CreateAccount(t *testing.T) {
	// Create test accounts
	validAccount := testutils.GenerateRandomizedAccount()
	validAccount.AccountStatus = lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE

	type args struct {
		ctx   context.Context
		input *CreateAccountInput
		clean func(t *testing.T, account *lead_scraper_servicev1.Account)
	}

	tests := []struct {
		name     string
		args     args
		wantErr  bool
		errType  error
		validate func(t *testing.T, account *lead_scraper_servicev1.Account)
	}{
		{
			name:    "[success scenario] - create new account",
			wantErr: false,
			args: args{
				ctx: context.Background(),
				input: &CreateAccountInput{
					Account:  validAccount,
					OrgID:    "test-org",
					TenantID: "test-tenant",
				},
				clean: func(t *testing.T, account *lead_scraper_servicev1.Account) {
					if account == nil {
						return
					}
					err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
						ID:           account.Id,
						DeletionType: DeletionTypeSoft,
					})
					if err != nil {
						t.Logf("Failed to cleanup test account: %v", err)
					}
				},
			},
			validate: func(t *testing.T, account *lead_scraper_servicev1.Account) {
				assert.NotNil(t, account)
				assert.Equal(t, validAccount.Email, account.Email)
				assert.Equal(t, "test-org", account.OrgId)
				assert.Equal(t, "test-tenant", account.TenantId)
				assert.Equal(t, lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE, account.AccountStatus)
			},
		},
		{
			name:    "[failure scenario] - nil input",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx:   context.Background(),
				input: nil,
			},
		},
		{
			name:    "[failure scenario] - nil account",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				input: &CreateAccountInput{
					Account:  nil,
					OrgID:    "test-org",
					TenantID: "test-tenant",
				},
			},
		},
		{
			name:    "[failure scenario] - empty email",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				input: &CreateAccountInput{
					Account: &lead_scraper_servicev1.Account{
						Email: "",
					},
					OrgID:    "test-org",
					TenantID: "test-tenant",
				},
			},
		},
		{
			name:    "[failure scenario] - empty org ID",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				input: &CreateAccountInput{
					Account:  validAccount,
					OrgID:    "",
					TenantID: "test-tenant",
				},
			},
		},
		{
			name:    "[failure scenario] - empty tenant ID",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				input: &CreateAccountInput{
					Account:  validAccount,
					OrgID:    "test-org",
					TenantID: "",
				},
			},
		},
		{
			name:    "[failure scenario] - context timeout",
			wantErr: true,
			args: args{
				ctx: context.Background(),
				input: &CreateAccountInput{
					Account:  validAccount,
					OrgID:    "test-org",
					TenantID: "test-tenant",
				},
			},
			validate: func(t *testing.T, account *lead_scraper_servicev1.Account) {
				// Create a new context with cancel for the actual operation
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel immediately
				
				// Try to create account with cancelled context
				_, err := conn.CreateAccount(ctx, &CreateAccountInput{
					Account:  validAccount,
					OrgID:    "test-org",
					TenantID: "test-tenant",
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "context canceled")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := conn.CreateAccount(tt.args.ctx, tt.args.input)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, account)

			if tt.validate != nil {
				tt.validate(t, account)
			}

			// Cleanup after test
			if tt.args.clean != nil {
				tt.args.clean(t, account)
			}
		})
	}
}

func TestDb_GetAccountByEmail(t *testing.T) {
	type args struct {
		ctx          context.Context
		accountEmail string
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    *lead_scraper_servicev1.AccountORM
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.GetAccountByEmail(tt.args.ctx, tt.args.accountEmail)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.GetAccountByEmail() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.GetAccountByEmail() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_CreateAccount_DuplicateEmail(t *testing.T) {
	// Create initial account
	validAccount := testutils.GenerateRandomizedAccount()
	validAccount.AccountStatus = lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE

	createdAccount, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
		Account:  validAccount,
		OrgID:    "test-org",
		TenantID: "test-tenant",
	})
	require.NoError(t, err)
	require.NotNil(t, createdAccount)

	// Clean up after test
	defer func() {
		if createdAccount != nil {
			err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
				ID:           createdAccount.Id,
				DeletionType: DeletionTypeSoft,
			})
			if err != nil {
				t.Logf("Failed to cleanup test account: %v", err)
			}
		}
	}()

	// Try to create account with same email
	duplicateAccount := testutils.GenerateRandomizedAccount()
	duplicateAccount.Email = validAccount.Email
	duplicateAccount.AccountStatus = lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE

	_, err = conn.CreateAccount(context.Background(), &CreateAccountInput{
		Account:  duplicateAccount,
		OrgID:    "test-org",
		TenantID: "test-tenant",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountAlreadyExists)
}

func TestDb_CreateAccount_ConcurrentCreation(t *testing.T) {
	numAccounts := 5
	var wg sync.WaitGroup
	errors := make(chan error, numAccounts)
	accounts := make(chan *lead_scraper_servicev1.Account, numAccounts)

	// Create accounts concurrently
	for i := 0; i < numAccounts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mockAccount := testutils.GenerateRandomizedAccount()
			mockAccount.AccountStatus = lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE

			account, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
				Account:  mockAccount,
				OrgID:    "test-org",
				TenantID: "test-tenant",
			})
			if err != nil {
				errors <- err
				return
			}
			accounts <- account
		}()
	}

	wg.Wait()
	close(errors)
	close(accounts)

	// Clean up created accounts
	createdAccounts := make([]*lead_scraper_servicev1.Account, 0)
	for account := range accounts {
		createdAccounts = append(createdAccounts, account)
	}

	// Clean up in a deferred function to ensure cleanup happens even if test fails
	defer func() {
		for _, account := range createdAccounts {
			if account != nil {
				err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
					ID:           account.Id,
					DeletionType: DeletionTypeSoft,
				})
				if err != nil {
					t.Logf("Failed to cleanup test account: %v", err)
				}
			}
		}
	}()

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent creation, got: %v", errs)

	// Verify all accounts were created successfully
	require.Equal(t, numAccounts, len(createdAccounts), "Expected %d accounts to be created, got %d", numAccounts, len(createdAccounts))
	for _, account := range createdAccounts {
		require.NotNil(t, account)
		require.NotZero(t, account.Id)
		require.Equal(t, lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE, account.AccountStatus)
	}
}

func TestCreateAccountInput_Validate(t *testing.T) {
	validAccount := testutils.GenerateRandomizedAccount()

	tests := []struct {
		name    string
		input   *CreateAccountInput
		wantErr bool
	}{
		{
			name: "success - valid input",
			input: &CreateAccountInput{
				Account:  validAccount,
				OrgID:    "test-org",
				TenantID: "test-tenant",
			},
			wantErr: false,
		},
		{
			name:    "failure - nil input",
			input:   nil,
			wantErr: true,
		},
		{
			name: "failure - nil account",
			input: &CreateAccountInput{
				Account:  nil,
				OrgID:    "test-org",
				TenantID: "test-tenant",
			},
			wantErr: true,
		},
		{
			name: "failure - empty email",
			input: &CreateAccountInput{
				Account: &lead_scraper_servicev1.Account{
					Email: "",
				},
				OrgID:    "test-org",
				TenantID: "test-tenant",
			},
			wantErr: true,
		},
		{
			name: "failure - empty org ID",
			input: &CreateAccountInput{
				Account:  validAccount,
				OrgID:    "",
				TenantID: "test-tenant",
			},
			wantErr: true,
		},
		{
			name: "failure - empty tenant ID",
			input: &CreateAccountInput{
				Account:  validAccount,
				OrgID:    "test-org",
				TenantID: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
