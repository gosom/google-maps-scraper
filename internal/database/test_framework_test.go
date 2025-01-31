package database

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	postgresdb "github.com/SolomonAIEngineering/backend-core-library/database/postgres"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
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

		// Start a transaction for both setup and test
		tx := config.BaseDb.Client.Engine.Begin()
		if tx.Error != nil {
			t.Fatalf("Failed to begin transaction: %v", tx.Error)
		}

		// Create test database instance
		testDb := createTestDb(tx, config.BaseDb, config.QueryTimeout)

		// Always ensure we clean up the transaction
		var committed bool
		defer func() {
			if r := recover(); r != nil {
				if !committed {
					tx.Rollback()
				}
				panic(r) // re-throw panic after cleanup
			}
			// If we haven't committed or rolled back yet, do it now
			if !committed {
				tx.Rollback()
			}
		}()

		// Clean up the database first
		err := tx.Exec("DELETE FROM accounts").Error
		if err != nil {
			t.Errorf("Failed to clean up database: %v", err)
			return
		}

		// Run setup if provided
		if config.SetupFunc != nil {
			err := config.SetupFunc(t, testDb)
			if err != nil {
				t.Errorf("Failed to setup test: %v", err)
				return
			}
		}

		// Run the test
		testErr := testFn(t, testDb)

		// Run cleanup if provided
		if config.CleanupFunc != nil {
			err := config.CleanupFunc(t, testDb)
			if err != nil {
				t.Errorf("Failed to cleanup test: %v", err)
				return
			}
		}

		// If test failed, return
		if testErr != nil {
			t.Errorf("Test failed: %v", testErr)
			return
		}

		// If we got here, commit the transaction
		if err := tx.Commit().Error; err != nil {
			t.Errorf("Failed to commit transaction: %v", err)
			return
		}
		committed = true
	}
} 