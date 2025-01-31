package database

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	postgresdb "github.com/SolomonAIEngineering/backend-core-library/database/postgres"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// generateRandomizedAccount creates a new account with randomized data for testing
func generateRandomizedAccount() *lead_scraper_servicev1.Account {
	randomID := rand.Int63n(1000000)
	return &lead_scraper_servicev1.Account{
		Email:         fmt.Sprintf("test%d@example.com", randomID),
		AccountStatus: lead_scraper_servicev1.Account_ACCOUNT_STATUS_ACTIVE,
	}
}

// TestDBFunc represents a database test function that can be wrapped with transaction management
type TestDBFunc func(t *testing.T, testDb *Db) error

// DBTestConfig holds configuration for database testing
type DBTestConfig struct {
	BaseDb         *Db
	QueryTimeout   time.Duration
	SetupFunc      func(t *testing.T, testDb *Db) error
	CleanupFunc    func(t *testing.T, testDb *Db) error
}

// NewDBTestConfig creates a new configuration for database testing
func NewDBTestConfig(baseDb *Db) *DBTestConfig {
	return &DBTestConfig{
		BaseDb:       baseDb,
		QueryTimeout: 30 * time.Second,
	}
}

// WithSetup adds a setup function to the config
func (c *DBTestConfig) WithSetup(setup func(t *testing.T, testDb *Db) error) *DBTestConfig {
	c.SetupFunc = setup
	return c
}

// WithCleanup adds a cleanup function to the config
func (c *DBTestConfig) WithCleanup(cleanup func(t *testing.T, testDb *Db) error) *DBTestConfig {
	c.CleanupFunc = cleanup
	return c
}

// createTestDb creates a new test database instance with the given transaction
func createTestDb(tx *gorm.DB, baseDb *Db, timeout time.Duration) *Db {
	return &Db{
		Client: &postgresdb.Client{
			Engine:       tx,
			QueryTimeout: &timeout,
		},
		Logger:        baseDb.Logger,
		QueryOperator: baseDb.QueryOperator,
	}
}

// WithTransaction wraps a test function with automatic transaction management
func WithTransaction(config *DBTestConfig, testName string, testFn TestDBFunc) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		// Start a setup transaction and commit it
		if config.SetupFunc != nil {
			setupTx := config.BaseDb.Client.Engine.Begin()
			require.NoError(t, setupTx.Error)

			setupDb := createTestDb(setupTx, config.BaseDb, config.QueryTimeout)

			// Run setup
			err := config.SetupFunc(t, setupDb)
			if err != nil {
				setupTx.Rollback()
				t.Fatalf("Failed to setup test: %v", err)
			}

			// Commit the setup transaction
			err = setupTx.Commit().Error
			require.NoError(t, err, "Failed to commit setup transaction")
		}

		// Start a new transaction for the test
		tx := config.BaseDb.Client.Engine.Begin()
		require.NoError(t, tx.Error)

		// Create test database instance
		testDb := createTestDb(tx, config.BaseDb, config.QueryTimeout)

		// Ensure we rollback the test transaction at the end
		defer func() {
			tx.Rollback()
		}()

		// Run the test
		if err := testFn(t, testDb); err != nil {
			t.Errorf("Test failed: %v", err)
		}

		// Run cleanup if provided
		if config.CleanupFunc != nil {
			cleanupTx := config.BaseDb.Client.Engine.Begin()
			require.NoError(t, cleanupTx.Error)

			cleanupDb := createTestDb(cleanupTx, config.BaseDb, config.QueryTimeout)

			err := config.CleanupFunc(t, cleanupDb)
			if err != nil {
				cleanupTx.Rollback()
				t.Errorf("Failed to cleanup test: %v", err)
				return
			}

			err = cleanupTx.Commit().Error
			if err != nil {
				t.Errorf("Failed to commit cleanup transaction: %v", err)
			}
		}
	}
} 