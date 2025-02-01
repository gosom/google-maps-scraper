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

func TestGetAPIKey(t *testing.T) {
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
		id        uint64
		wantError bool
		errType   error
		validate  func(t *testing.T, apiKey *lead_scraper_servicev1.APIKey)
	}{
		{
			name:      "[success scenario] - valid id",
			id:        created.Id,
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
			name:      "[failure scenario] - invalid id",
			id:        0,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - non-existent id",
			id:        999999,
			wantError: true,
			errType:   ErrJobDoesNotExist,
		},
		{
			name:      "[failure scenario] - context timeout",
			id:        created.Id,
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

			result, err := conn.GetAPIKey(ctx, tt.id)

			if tt.wantError {
				require.Error(t, err)
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

func TestGetAPIKey_ConcurrentReads(t *testing.T) {
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

	numReads := 10
	var wg sync.WaitGroup
	errors := make(chan error, numReads)

	// Perform concurrent reads
	for i := 0; i < numReads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			apiKey, err := conn.GetAPIKey(context.Background(), created.Id)
			if err != nil {
				errors <- err
				return
			}
			assert.Equal(t, created.Id, apiKey.Id)
			assert.Equal(t, created.Name, apiKey.Name)
			assert.Equal(t, created.KeyHash, apiKey.KeyHash)
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent reads, got: %v", errs)
}
