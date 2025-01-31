// Package database provides access and utility functions to interact with the database.
// This includes methods to create, read, update, and delete records in various tables.
package database

import (
	"context"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAccountInput_validate(t *testing.T) {
	tests := []struct {
		name    string
		d       *GetAccountInput
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.d.validate(); (err != nil) != tt.wantErr {
				t.Errorf("GetAccountInput.validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDb_GetAccount(t *testing.T) {
	// Create test accounts
	validAccount := testutils.GenerateRandomizedAccount()

	type args struct {
		ctx    context.Context
		input  *GetAccountInput
		setup  func(t *testing.T) (uint64, error)
		clean  func(t *testing.T, id uint64)
	}

	tests := []struct {
		name     string
		args     args
		wantErr  bool
		errType  error
		validate func(t *testing.T, account *lead_scraper_servicev1.Account)
	}{
		{
			name:    "[success scenario] - get existing account",
			wantErr: false,
			args: args{
				ctx: context.Background(),
				input: &GetAccountInput{
					ID:       1,
				},
				setup: func(t *testing.T) (uint64, error) {
					// Create the account first
					acct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)
					require.Equal(t, validAccount.Email, acct.Email)
					return acct.Id, nil
				},
				clean: func(t *testing.T, id uint64) {
					err := conn.DeleteAccount(context.Background(), &DeleteAccountParams{
						ID:           id,
						OrgID:        "test-org",
						TenantID:     "test-tenant",
						DeletionType: DeletionTypeSoft,
					})
					require.NoError(t, err)
				},
			},
			validate: func(t *testing.T, account *lead_scraper_servicev1.Account) {
				assert.NotNil(t, account)
				assert.Equal(t, validAccount.Email, account.Email)
				assert.Equal(t, "test-org", account.OrgId)
				assert.Equal(t, "test-tenant", account.TenantId)
			},
		},
		{
			name:    "[failure scenario] - zero account ID",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				input: &GetAccountInput{
					ID:       0,
				},
			},
		},
		{
			name:    "[failure scenario] - non-existent account",
			wantErr: true,
			errType: ErrAccountDoesNotExist,
			args: args{
				ctx: context.Background(),
				input: &GetAccountInput{
					ID:       999999,
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
				input: &GetAccountInput{
					ID:       1,
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
				id, err = tt.args.setup(t)
				require.NoError(t, err)
				if id != 0 {
					tt.args.input.ID = id
				}
			}

			// Cleanup after test
			defer func() {
				if tt.args.clean != nil {
					tt.args.clean(t, id)
				}
			}()

			account, err := conn.GetAccount(tt.args.ctx, tt.args.input)
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
		})
	}
}

func TestListAccountsInput_validate(t *testing.T) {
	tests := []struct {
		name    string
		d       *ListAccountsInput
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "success - valid input",
			d: &ListAccountsInput{
				OrgID:    "test-org",
				TenantID: "test-tenant",
				Limit:    10,
				Offset:   0,
			},
			wantErr: false,
		},
		{
			name: "failure - empty org ID",
			d: &ListAccountsInput{
				OrgID:    "",
				TenantID: "test-tenant",
				Limit:    10,
				Offset:   0,
			},
			wantErr: true,
		},
		{
			name: "failure - empty tenant ID",
			d: &ListAccountsInput{
				OrgID:    "test-org",
				TenantID: "",
				Limit:    10,
				Offset:   0,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.d.validate(); (err != nil) != tt.wantErr {
				t.Errorf("ListAccountsInput.validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDb_ListAccounts(t *testing.T) {
	// Clean up any existing test accounts first
	cleanupCtx := context.Background()
	b := conn.QueryOperator.AccountORM
	_, err := b.WithContext(cleanupCtx).
		Where(b.OrgId.Eq("test-org")).
		Where(b.TenantId.Eq("test-tenant")).
		Unscoped().
		Delete()
	require.NoError(t, err)

	// Verify cleanup was successful
	count, err := b.WithContext(cleanupCtx).
		Where(b.OrgId.Eq("test-org")).
		Where(b.TenantId.Eq("test-tenant")).
		Count()
	require.NoError(t, err)
	require.Equal(t, int64(0), count, "cleanup failed: test accounts still exist")

	// Create test accounts
	numAccounts := 5
	accounts := make([]*lead_scraper_servicev1.Account, 0, numAccounts)
	for i := 0; i < numAccounts; i++ {
		mockAccount := testutils.GenerateRandomizedAccount()
		createdAcct, err := conn.CreateAccount(context.Background(), &CreateAccountInput{
			Account:  mockAccount,
			OrgID:    "test-org",
			TenantID: "test-tenant",
		})
		require.NoError(t, err)
		require.NotNil(t, createdAcct)
		require.Equal(t, mockAccount.Email, createdAcct.Email)
		
		// Verify the account was created
		verifyAcct, err := conn.GetAccount(context.Background(), &GetAccountInput{
			ID:       createdAcct.Id,
		})
		require.NoError(t, err)
		require.NotNil(t, verifyAcct)
		accounts = append(accounts, createdAcct)
	}

	// Verify we created the expected number of accounts
	require.Len(t, accounts, numAccounts)

	// Clean up after test
	defer func() {
		cleanupCtx := context.Background()
		for _, acct := range accounts {
			err := conn.DeleteAccount(cleanupCtx, &DeleteAccountParams{
				ID:           acct.Id,
				OrgID:        "test-org",
				TenantID:     "test-tenant",
				DeletionType: DeletionTypeHard,
			})
			require.NoError(t, err)
		}

		// Verify cleanup
		count, err := b.WithContext(cleanupCtx).
			Where(b.OrgId.Eq("test-org")).
			Where(b.TenantId.Eq("test-tenant")).
			Count()
		require.NoError(t, err)
		require.Equal(t, int64(0), count, "cleanup failed: test accounts still exist")
	}()

	type args struct {
		ctx   context.Context
		input *ListAccountsInput
	}

	tests := []struct {
		name     string
		args     args
		wantErr  bool
		errType  error
		validate func(t *testing.T, accounts []*lead_scraper_servicev1.Account)
	}{
		{
			name:    "[success scenario] - list all accounts",
			wantErr: false,
			args: args{
				ctx: context.Background(),
				input: &ListAccountsInput{
					OrgID:    "test-org",
					TenantID: "test-tenant",
					Limit:    10,
					Offset:   0,
				},
			},
			validate: func(t *testing.T, accounts []*lead_scraper_servicev1.Account) {
				assert.NotNil(t, accounts)
				assert.Len(t, accounts, numAccounts)
				for _, acct := range accounts {
					assert.Equal(t, "test-org", acct.OrgId)
					assert.Equal(t, "test-tenant", acct.TenantId)
				}
			},
		},
		{
			name:    "[success scenario] - paginated list",
			wantErr: false,
			args: args{
				ctx: context.Background(),
				input: &ListAccountsInput{
					OrgID:    "test-org",
					TenantID: "test-tenant",
					Limit:    2,
					Offset:   0,
				},
			},
			validate: func(t *testing.T, accounts []*lead_scraper_servicev1.Account) {
				assert.NotNil(t, accounts)
				assert.Len(t, accounts, 2)
			},
		},
		{
			name:    "[failure scenario] - invalid limit",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				input: &ListAccountsInput{
					OrgID:    "test-org",
					TenantID: "test-tenant",
					Limit:    0,
					Offset:   0,
				},
			},
		},
		{
			name:    "[failure scenario] - invalid offset",
			wantErr: true,
			errType: ErrInvalidInput,
			args: args{
				ctx: context.Background(),
				input: &ListAccountsInput{
					OrgID:    "test-org",
					TenantID: "test-tenant",
					Limit:    10,
					Offset:   -1,
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
				input: &ListAccountsInput{
					OrgID:    "test-org",
					TenantID: "test-tenant",
					Limit:    10,
					Offset:   0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts, err := conn.ListAccounts(tt.args.ctx, tt.args.input)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			if tt.validate != nil {
				tt.validate(t, accounts)
			}
		})
	}
}

func TestGetAccountInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   *GetAccountInput
		wantErr bool
	}{
		{
			name: "success - valid input",
			input: &GetAccountInput{
				ID:       123,
			},
			wantErr: false,
		},
		{
			name: "failure - zero account ID",
			input: &GetAccountInput{
				ID:       0,
			},
			wantErr: true,
		},
		{
			name:    "failure - nil input",
			input:   nil,
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

func TestListAccountsInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   *ListAccountsInput
		wantErr bool
	}{
		{
			name: "success - valid input",
			input: &ListAccountsInput{
				OrgID:    "test-org",
				TenantID: "test-tenant",
				Limit:    10,
				Offset:   0,
			},
			wantErr: false,
		},
		{
			name: "failure - zero limit",
			input: &ListAccountsInput{
				OrgID:    "test-org",
				TenantID: "test-tenant",
				Limit:    0,
				Offset:   0,
			},
			wantErr: true,
		},
		{
			name: "failure - negative offset",
			input: &ListAccountsInput{
				OrgID:    "test-org",
				TenantID: "test-tenant",
				Limit:    10,
				Offset:   -1,
			},
			wantErr: true,
		},
		{
			name: "failure - empty org ID",
			input: &ListAccountsInput{
				OrgID:    "",
				TenantID: "test-tenant",
				Limit:    10,
				Offset:   0,
			},
			wantErr: true,
		},
		{
			name: "failure - empty tenant ID",
			input: &ListAccountsInput{
				OrgID:    "test-org",
				TenantID: "",
				Limit:    10,
				Offset:   0,
			},
			wantErr: true,
		},
		{
			name:    "failure - nil input",
			input:   nil,
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
