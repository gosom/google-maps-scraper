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

func TestValidateAPIKey(t *testing.T) {
	// Create a test API key first
	testKey := testutils.GenerateRandomAPIKey()
	created, err := conn.CreateAPIKey(context.Background(), testKey)
	require.NoError(t, err)
	require.NotNil(t, created)

	// Clean up after all tests
	defer func() {
		if created != nil {
			err := conn.DeleteAPIKey(context.Background(), created.Id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		hash      string
		wantError bool
		errType   error
		validate  func(t *testing.T, apiKey *lead_scraper_servicev1.APIKey)
	}{
		{
			name:      "[success scenario] - valid hash",
			hash:      created.KeyHash,
			wantError: false,
			validate: func(t *testing.T, apiKey *lead_scraper_servicev1.APIKey) {
				assert.NotNil(t, apiKey)
				assert.Equal(t, created.Id, apiKey.Id)
				assert.Equal(t, created.Name, apiKey.Name)
				assert.Equal(t, created.KeyHash, apiKey.KeyHash)
				assert.Equal(t, created.KeyPrefix, apiKey.KeyPrefix)
				assert.Equal(t, created.OrgId, apiKey.OrgId)
				assert.Equal(t, created.TenantId, apiKey.TenantId)
				assert.Equal(t, created.Scopes, apiKey.Scopes)
				assert.Equal(t, created.AllowedIps, apiKey.AllowedIps)
				assert.Equal(t, created.IsTestKey, apiKey.IsTestKey)
				assert.Equal(t, created.RequestsPerSecond, apiKey.RequestsPerSecond)
				assert.Equal(t, created.RequestsPerDay, apiKey.RequestsPerDay)
				assert.Equal(t, created.ConcurrentRequests, apiKey.ConcurrentRequests)
				assert.Equal(t, created.MonthlyRequestQuota, apiKey.MonthlyRequestQuota)
				assert.Equal(t, created.Status, apiKey.Status)
			},
		},
		{
			name:      "[failure scenario] - empty hash",
			hash:      "",
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - non-existent hash",
			hash:      "non_existent_hash",
			wantError: true,
			errType:   ErrJobDoesNotExist,
		},
		{
			name:      "[failure scenario] - context timeout",
			hash:      created.KeyHash,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.name == "[failure scenario] - context timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
			}

			result, err := conn.ValidateAPIKey(ctx, tt.hash)

			if tt.wantError {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}
