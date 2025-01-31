package database

import (
	"context"
	"testing"
	"time"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDb_UpdateAccount(t *testing.T) {
	// Create test accounts
	validAccount := generateRandomizedAccount()

	tests := []struct {
		name     string
		account  *lead_scraper_servicev1.Account
		wantErr  bool
		errType  error
		validate func(t *testing.T, account *lead_scraper_servicev1.Account)
	}{
		{
			name:    "[success scenario] - update existing account",
			wantErr: false,
			validate: func(t *testing.T, account *lead_scraper_servicev1.Account) {
				assert.NotNil(t, account)
				assert.Equal(t, "updated@example.com", account.Email)
				assert.Equal(t, "test-org", account.OrgId)
				assert.Equal(t, "test-tenant", account.TenantId)
			},
		},
		{
			name:    "[failure scenario] - nil account",
			account: nil,
			wantErr: true,
			errType: ErrInvalidInput,
		},
		{
			name: "[failure scenario] - non-existent account",
			account: &lead_scraper_servicev1.Account{
				Id:       999999,
				OrgId:    "test-org",
				TenantId: "test-tenant",
				Email:    "nonexistent@example.com",
			},
			wantErr: true,
			errType: ErrAccountDoesNotExist,
		},
		{
			name: "[failure scenario] - invalid email",
			account: &lead_scraper_servicev1.Account{
				Email: "",
			},
			wantErr: true,
			errType: ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewDBTestConfig(conn)

			if tt.name == "[success scenario] - update existing account" {
				config = config.WithSetup(func(t *testing.T, db *Db) error {
					// Create the account first
					acct, err := db.CreateAccount(context.Background(), &CreateAccountInput{
						Account:  validAccount,
						OrgID:    "test-org",
						TenantID: "test-tenant",
					})
					require.NoError(t, err)
					require.NotNil(t, acct)

					// Update some fields
					acct.Email = "updated@example.com"
					tt.account = acct
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

				updatedAccount, err := db.UpdateAccount(ctx, tt.account)
				if tt.wantErr {
					require.Error(t, err)
					if tt.errType != nil {
						assert.ErrorIs(t, err, tt.errType)
					}
					return nil
				}

				require.NoError(t, err)
				require.NotNil(t, updatedAccount)

				if tt.validate != nil {
					tt.validate(t, updatedAccount)
				}
				return nil
			}

			WithTransaction(config, tt.name, test)(t)
		})
	}
}