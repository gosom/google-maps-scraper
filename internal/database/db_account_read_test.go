// Package database provides access and utility functions to interact with the database.
// This includes methods to create, read, update, and delete records in various tables.
package database

import (
	"context"
	"testing"
	"time"

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
		{
			name: "success - valid input",
			d: &GetAccountInput{
				ID: 1,
			},
			wantErr: false,
		},
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
	validAccount := generateRandomizedAccount()

	tests := []struct {
		name     string
		input    *GetAccountInput
		wantErr  bool
		errType  error
		validate func(t *testing.T, account *lead_scraper_servicev1.Account)
	}{
		{
			name:    "[success scenario] - get existing account",
			wantErr: false,
			input: &GetAccountInput{
				ID: 1, // Will be updated in setup
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
			input: &GetAccountInput{
				ID: 0,
			},
		},
		{
			name:    "[failure scenario] - non-existent account",
			wantErr: true,
			errType: ErrAccountDoesNotExist,
			input: &GetAccountInput{
				ID: 999999,
			},
		},
		{
			name:    "[failure scenario] - context timeout",
			wantErr: true,
			input: &GetAccountInput{
				ID: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewDBTestConfig(conn)

			if tt.name == "[success scenario] - get existing account" {
				config = config.WithSetup(func(t *testing.T, db *Db) error {
					// Create the account first
					acct, err := db.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)
					require.Equal(t, validAccount.Email, acct.Email)

					// Update the input with the created account ID
					tt.input.ID = acct.Id
					return nil
				})
			}

			test := func(t *testing.T, db *Db) error {
				ctx := context.Background()
				if tt.name == "[failure scenario] - context timeout" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
					defer cancel()
					time.Sleep(2 * time.Millisecond)
				}

				account, err := db.GetAccount(ctx, tt.input)
				if tt.wantErr {
					require.Error(t, err)
					if tt.errType != nil {
						assert.ErrorIs(t, err, tt.errType)
					}
					return nil
				}

				require.NoError(t, err)
				require.NotNil(t, account)

				if tt.validate != nil {
					tt.validate(t, account)
				}
				return nil
			}

			WithTransaction(config, tt.name, test)(t)
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
				Limit:    10,
				Offset:   0,
			},
			wantErr: false,
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
	tests := []struct {
		name     string
		input    *ListAccountsInput
		numAccounts int
		wantErr  bool
		errType  error
		validate func(t *testing.T, accounts []*lead_scraper_servicev1.Account)
	}{
		{
			name:    "[success scenario] - list all accounts",
			wantErr: false,
			numAccounts: 5,
			input: &ListAccountsInput{
				Limit:    10,
				Offset:   0,
			},
			validate: func(t *testing.T, accounts []*lead_scraper_servicev1.Account) {
				assert.NotNil(t, accounts)
				assert.Len(t, accounts, 5)
			},
		},
		{
			name:    "[success scenario] - list accounts with offset",
			wantErr: false,
			numAccounts: 5,
			input: &ListAccountsInput{
				Limit:    2,
				Offset:   2,
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
			input: &ListAccountsInput{
				Limit:    0,
				Offset:   0,
			},
		},
		{
			name:    "[failure scenario] - invalid offset",
			wantErr: true,
			errType: ErrInvalidInput,
			input: &ListAccountsInput{
				Limit:    10,
				Offset:   -1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewDBTestConfig(conn)

			if tt.numAccounts > 0 {
				config = config.WithSetup(func(t *testing.T, db *Db) error {
					// Create test accounts
					for i := 0; i < tt.numAccounts; i++ {
						mockAccount := generateRandomizedAccount()
						createdAcct, err := db.CreateAccount(context.Background(), &CreateAccountInput{
							Account:  mockAccount,
							OrgID:    "test-org",
							TenantID: "test-tenant",
						})
						require.NoError(t, err)
						require.NotNil(t, createdAcct)
						require.Equal(t, mockAccount.Email, createdAcct.Email)
						require.Equal(t, "test-org", createdAcct.OrgId)
						require.Equal(t, "test-tenant", createdAcct.TenantId)
					}
					return nil
				})
			}

			test := func(t *testing.T, db *Db) error {
				accounts, err := db.ListAccounts(context.Background(), tt.input)
				if tt.wantErr {
					require.Error(t, err)
					if tt.errType != nil {
						assert.ErrorIs(t, err, tt.errType)
					}
					return nil
				}

				require.NoError(t, err)
				require.NotNil(t, accounts)

				if tt.validate != nil {
					tt.validate(t, accounts)
				}
				return nil
			}

			WithTransaction(config, tt.name, test)(t)
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
				Limit:    10,
				Offset:   0,
			},
			wantErr: false,
		},
		{
			name: "failure - zero limit",
			input: &ListAccountsInput{
				Limit:    0,
				Offset:   0,
			},
			wantErr: true,
		},
		{
			name: "failure - negative offset",
			input: &ListAccountsInput{
				Limit:    10,
				Offset:   -1,
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
