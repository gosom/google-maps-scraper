// Package testcontainers_test provides integration tests and examples for the testcontainers package.
// These tests demonstrate the usage of the package and verify its functionality.
package testcontainers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTestContext verifies the functionality of the TestContext type and its associated methods.
// It demonstrates various use cases and serves as documentation through examples.
//
// The test suite includes:
//   - Basic container initialization and cleanup
//   - Multiple container instance management
//   - Redis operations
//   - PostgreSQL operations
//
// Each test case demonstrates a different aspect of the package's functionality
// and serves as an example for users.
func TestTestContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Test case: Basic container initialization and cleanup
	t.Run("creates and cleans up test context", func(t *testing.T) {
		WithTestContext(t, func(ctx *TestContext) {
			// Test Redis connection
			result, err := ctx.Redis.Ping(ctx.ctx).Result()
			require.NoError(t, err)
			assert.Equal(t, "PONG", result)

			// Test PostgreSQL connection
			err = ctx.DB.Ping(ctx.ctx)
			require.NoError(t, err)
		})
	})

	// Test case: Multiple container instances
	t.Run("handles multiple test contexts", func(t *testing.T) {
		WithTestContext(t, func(ctx1 *TestContext) {
			WithTestContext(t, func(ctx2 *TestContext) {
				// Verify different ports
				assert.NotEqual(t, ctx1.RedisConfig.Port, ctx2.RedisConfig.Port)
				assert.NotEqual(t, ctx1.PostgresConfig.Port, ctx2.PostgresConfig.Port)
			})
		})
	})

	// Test case: Redis operations
	t.Run("verifies Redis operations", func(t *testing.T) {
		WithTestContext(t, func(ctx *TestContext) {
			// Test Set operation
			err := ctx.Redis.Set(ctx.ctx, "test_key", "test_value", time.Minute).Err()
			require.NoError(t, err)

			// Test Get operation
			val, err := ctx.Redis.Get(ctx.ctx, "test_key").Result()
			require.NoError(t, err)
			assert.Equal(t, "test_value", val)
		})
	})

	// Test case: PostgreSQL operations
	t.Run("verifies PostgreSQL operations", func(t *testing.T) {
		WithTestContext(t, func(ctx *TestContext) {
			// Create a test table
			_, err := ctx.DB.Exec(ctx.ctx, `
				CREATE TABLE test_table (
					id SERIAL PRIMARY KEY,
					name TEXT NOT NULL
				)
			`)
			require.NoError(t, err)

			// Insert a row
			_, err = ctx.DB.Exec(ctx.ctx, `
				INSERT INTO test_table (name) VALUES ($1)
			`, "test_name")
			require.NoError(t, err)

			// Query the row
			var name string
			err = ctx.DB.QueryRow(ctx.ctx, `
				SELECT name FROM test_table WHERE id = 1
			`).Scan(&name)
			require.NoError(t, err)
			assert.Equal(t, "test_name", name)
		})
	})
} 