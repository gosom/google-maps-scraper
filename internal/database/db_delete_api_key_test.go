package database

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteAPIKey(t *testing.T) {
	// Create a test API key first
	testKey := testutils.GenerateRandomAPIKey()
	created, err := conn.CreateAPIKey(context.Background(), testKey)
	require.NoError(t, err)
	require.NotNil(t, created)

	tests := []struct {
		name      string
		id        uint64
		wantError bool
		errType   error
		setup     func(t *testing.T) uint64
		validate  func(t *testing.T, id uint64)
	}{
		{
			name:      "[success scenario] - valid id",
			id:        created.Id,
			wantError: false,
			validate: func(t *testing.T, id uint64) {
				// Verify the key was deleted
				_, err := conn.GetAPIKey(context.Background(), id)
				assert.Error(t, err)
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
			name:      "[failure scenario] - already deleted key",
			wantError: true,
			errType:   ErrJobDoesNotExist,
			setup: func(t *testing.T) uint64 {
				// Create and delete a key
				key := testutils.GenerateRandomAPIKey()
				created, err := conn.CreateAPIKey(context.Background(), key)
				require.NoError(t, err)
				require.NotNil(t, created)

				err = conn.DeleteAPIKey(context.Background(), created.Id)
				require.NoError(t, err)

				return created.Id
			},
		},
		{
			name:      "[failure scenario] - context timeout",
			id:        created.Id,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id uint64
			if tt.setup != nil {
				id = tt.setup(t)
			} else {
				id = tt.id
			}

			ctx := context.Background()
			if tt.name == "[failure scenario] - context timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
			}

			err := conn.DeleteAPIKey(ctx, id)

			if tt.wantError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tt.validate != nil {
				tt.validate(t, id)
			}
		})
	}
}

func TestDeleteAPIKey_ConcurrentDeletions(t *testing.T) {
	numKeys := 5
	var wg sync.WaitGroup
	errors := make(chan error, numKeys)
	keyIDs := make([]uint64, numKeys)

	// Create test API keys
	for i := 0; i < numKeys; i++ {
		key := testutils.GenerateRandomAPIKey()
		key.Name = fmt.Sprintf("Test Key %d", i)
		created, err := conn.CreateAPIKey(context.Background(), key)
		require.NoError(t, err)
		require.NotNil(t, created)
		keyIDs[i] = created.Id
	}

	// Delete keys concurrently
	for i := 0; i < numKeys; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			if err := conn.DeleteAPIKey(context.Background(), id); err != nil {
				errors <- err
			}
		}(keyIDs[i])
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent deletions, got: %v", errs)

	// Verify all keys were deleted
	for _, id := range keyIDs {
		_, err := conn.GetAPIKey(context.Background(), id)
		assert.Error(t, err)
	}
}
