package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListAPIKeys(t *testing.T) {
	// Create multiple test API keys
	numKeys := 5
	keyIDs := make([]uint64, numKeys)

	for i := 0; i < numKeys; i++ {
		key := testutils.GenerateRandomAPIKey()
		key.Name = fmt.Sprintf("Test Key %d", i)
		created, err := conn.CreateAPIKey(context.Background(), key)
		require.NoError(t, err)
		require.NotNil(t, created)
		keyIDs[i] = created.Id
	}

	// Clean up after all tests
	defer func() {
		for _, id := range keyIDs {
			err := conn.DeleteAPIKey(context.Background(), id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantError bool
		errType   error
		validate  func(t *testing.T, keys []*lead_scraper_servicev1.APIKey)
	}{
		{
			name:      "[success scenario] - get all keys",
			limit:     10,
			offset:    0,
			wantError: false,
			validate: func(t *testing.T, keys []*lead_scraper_servicev1.APIKey) {
				assert.Len(t, keys, numKeys)
				for i, key := range keys {
					assert.NotNil(t, key)
					assert.NotZero(t, key.Id)
					assert.Equal(t, fmt.Sprintf("Test Key %d", i), key.Name)
				}
			},
		},
		{
			name:      "[success scenario] - pagination first page",
			limit:     3,
			offset:    0,
			wantError: false,
			validate: func(t *testing.T, keys []*lead_scraper_servicev1.APIKey) {
				assert.Len(t, keys, 3)
				for i, key := range keys {
					assert.NotNil(t, key)
					assert.NotZero(t, key.Id)
					assert.Equal(t, fmt.Sprintf("Test Key %d", i), key.Name)
				}
			},
		},
		{
			name:      "[success scenario] - pagination second page",
			limit:     3,
			offset:    3,
			wantError: false,
			validate: func(t *testing.T, keys []*lead_scraper_servicev1.APIKey) {
				assert.Len(t, keys, 2) // Only 2 remaining keys
				for i, key := range keys {
					assert.NotNil(t, key)
					assert.NotZero(t, key.Id)
					assert.Equal(t, fmt.Sprintf("Test Key %d", i+3), key.Name)
				}
			},
		},
		{
			name:      "[success scenario] - empty result",
			limit:     10,
			offset:    numKeys + 1,
			wantError: false,
			validate: func(t *testing.T, keys []*lead_scraper_servicev1.APIKey) {
				assert.Empty(t, keys)
			},
		},
		{
			name:      "[failure scenario] - invalid limit",
			limit:     -1,
			offset:    0,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - invalid offset",
			limit:     10,
			offset:    -1,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - context timeout",
			limit:     10,
			offset:    0,
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

			results, err := conn.ListAPIKeys(ctx, tt.limit, tt.offset)

			if tt.wantError {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				assert.Nil(t, results)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, results)

			if tt.validate != nil {
				tt.validate(t, results)
			}
		})
	}
}

func TestListAPIKeys_EmptyDatabase(t *testing.T) {
	results, err := conn.ListAPIKeys(context.Background(), 10, 0)
	require.NoError(t, err)
	assert.Empty(t, results)
}
