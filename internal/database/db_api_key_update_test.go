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

func TestUpdateAPIKey(t *testing.T) {
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
		apiKey    *lead_scraper_servicev1.APIKey
		wantError bool
		errType   error
		validate  func(t *testing.T, apiKey *lead_scraper_servicev1.APIKey)
	}{
		{
			name: "[success scenario] - valid update",
			apiKey: &lead_scraper_servicev1.APIKey{
				Id:                 created.Id,
				Name:              "Updated Key Name",
				KeyHash:           "updated_hash",
				KeyPrefix:         "updated_prefix",
				OrgId:             "updated_org",
				TenantId:          "updated_tenant",
				Scopes:            []string{"read", "write", "admin"},
				AllowedIps:        []string{"192.168.1.1", "192.168.1.2"},
				IsTestKey:         true,
				RequestsPerSecond: 50,
				RequestsPerDay:    5000,
				ConcurrentRequests: 5,
				MonthlyRequestQuota: 100000,
				Status:             1, // Status 1 for active
			},
			wantError: false,
			validate: func(t *testing.T, apiKey *lead_scraper_servicev1.APIKey) {
				assert.NotNil(t, apiKey)
				assert.Equal(t, created.Id, apiKey.Id)
				assert.Equal(t, "Updated Key Name", apiKey.Name)
				assert.Equal(t, "updated_hash", apiKey.KeyHash)
				assert.Equal(t, "updated_prefix", apiKey.KeyPrefix)
				assert.Equal(t, "updated_org", apiKey.OrgId)
				assert.Equal(t, "updated_tenant", apiKey.TenantId)
				assert.Equal(t, []string{"read", "write", "admin"}, apiKey.Scopes)
				assert.Equal(t, []string{"192.168.1.1", "192.168.1.2"}, apiKey.AllowedIps)
				assert.True(t, apiKey.IsTestKey)
				assert.Equal(t, int32(50), apiKey.RequestsPerSecond)
				assert.Equal(t, int32(5000), apiKey.RequestsPerDay)
				assert.Equal(t, int32(5), apiKey.ConcurrentRequests)
				assert.Equal(t, int64(100000), apiKey.MonthlyRequestQuota)
				assert.Equal(t, int32(1), apiKey.Status)
			},
		},
		{
			name:      "[failure scenario] - nil api key",
			apiKey:    nil,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - zero id",
			apiKey: &lead_scraper_servicev1.APIKey{
				Id:   0,
				Name: "Updated Key Name",
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - non-existent id",
			apiKey: &lead_scraper_servicev1.APIKey{
				Id:   999999,
				Name: "Updated Key Name",
			},
			wantError: true,
			errType:   ErrJobDoesNotExist,
		},
		{
			name: "[failure scenario] - invalid status",
			apiKey: &lead_scraper_servicev1.APIKey{
				Id:     created.Id,
				Status: 999, // Invalid status
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - context timeout",
			apiKey: &lead_scraper_servicev1.APIKey{
				Id:   created.Id,
				Name: "Updated Key Name",
			},
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

			result, err := conn.UpdateAPIKey(ctx, tt.apiKey)

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

func TestUpdateAPIKey_ConcurrentUpdates(t *testing.T) {
	// Create a test API key first
	testKey := testutils.GenerateRandomAPIKey()
	created, err := conn.CreateAPIKey(context.Background(), testKey)
	require.NoError(t, err)
	require.NotNil(t, created)

	// Clean up after test
	defer func() {
		err := conn.DeleteAPIKey(context.Background(), created.Id)
		require.NoError(t, err)
	}()

	numUpdates := 5
	var wg sync.WaitGroup
	errors := make(chan error, numUpdates)
	results := make(chan *lead_scraper_servicev1.APIKey, numUpdates)

	// Perform concurrent updates
	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			updateKey := &lead_scraper_servicev1.APIKey{
				Id:                 created.Id,
				Name:              fmt.Sprintf("Updated Key %d", index),
				KeyHash:           fmt.Sprintf("hash_%d", index),
				KeyPrefix:         fmt.Sprintf("prefix_%d", index),
				OrgId:             created.OrgId,
				TenantId:          created.TenantId,
				Scopes:            []string{"read", "write"},
				AllowedIps:        []string{"192.168.1.1"},
				IsTestKey:         true,
				RequestsPerSecond: int32(index + 1),
				RequestsPerDay:    int32((index + 1) * 1000),
				ConcurrentRequests: int32(index + 1),
				MonthlyRequestQuota: int64((index + 1) * 10000),
				Status:             1, // Status 1 for active
			}

			result, err := conn.UpdateAPIKey(context.Background(), updateKey)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}(i)
	}

	wg.Wait()
	close(errors)
	close(results)

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent updates, got: %v", errs)

	// Verify the final state
	finalKey, err := conn.GetAPIKey(context.Background(), created.Id)
	require.NoError(t, err)
	require.NotNil(t, finalKey)
	assert.Equal(t, created.Id, finalKey.Id)
	assert.NotEqual(t, created.Name, finalKey.Name)
	assert.NotEqual(t, created.KeyHash, finalKey.KeyHash)
	assert.NotEqual(t, created.KeyPrefix, finalKey.KeyPrefix)
	assert.Equal(t, int32(1), finalKey.Status)
}
