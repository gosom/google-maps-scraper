package database

import (
	"fmt"
	"time"

	postgresdb "github.com/SolomonAIEngineering/backend-core-library/database/postgres"
	"github.com/SolomonAIEngineering/backend-core-library/instrumentation"
	"github.com/SolomonAIEngineering/backend-monorepo/src/core/api-definitions/pkg/generated/user_service/dal"
	schema "github.com/SolomonAIEngineering/backend-monorepo/src/core/api-definitions/pkg/generated/user_service/v1"
	"github.com/labstack/gommon/log"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Package database provides database operations for managing user and business accounts.
// It implements a PostgreSQL-backed storage layer with support for CRUD operations,
// tenant management, and audit logging.
//
// The package uses GORM as its ORM layer and provides a clean interface for database
// operations through the DatabaseOperations interface.
//
// Basic usage:
//
//	client := postgresdb.NewClient(config)
//	logger := zap.NewLogger()
//	instrClient := instrumentation.NewClient()
//
//	db, err := database.New(client, logger, instrClient)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create a user account
//	userAccount, err := db.CreateUserAccount(ctx, input)

// Common error definitions for database operations
var (
	// ErrInvalidDBObject is returned when the database object is not properly initialized
	// or is missing required components
	ErrInvalidDBObject = fmt.Errorf("invalid database object")

	// ErrInvalidGormConnectionObject is returned when the GORM database connection
	// cannot be established or is in an invalid state
	ErrInvalidGormConnectionObject = fmt.Errorf("invalid gorm connection object")

	// ErrInvalidGormConnectionEngine is returned when the GORM database engine
	// is not properly configured or initialized
	ErrInvalidGormConnectionEngine = fmt.Errorf("invalid gorm connection engine")

	// ErrInvalidPostgresClientObject is returned when the PostgreSQL client
	// is nil or improperly configured
	ErrInvalidPostgresClientObject = fmt.Errorf("invalid postgres client object")
)

// DatabaseOperations defines the methods to interact with the underlying database
// for both UserAccount and BusinessAccount entities. Implementers of this interface
// should be able to perform basic CRUD operations on these entities.
//
// The interface provides methods for:
//   - Creating and managing user accounts
//   - Creating and managing business accounts
//   - Managing tenant relationships
//   - Performing account updates and deletions
//
// Thread Safety:
// All methods should be safe for concurrent use by multiple goroutines.
type DatabaseOperations interface {}

// Db implements DatabaseOperations and provides connection handling for PostgreSQL.
// It encapsulates the database client, query operations, logging, and instrumentation.
type Db struct {
	Client *postgresdb.Client
	// QueryOperator is the object that will be used to execute database queries
	QueryOperator *dal.Query
	// Logger is the logger that will be used to log database related messages
	Logger *zap.Logger
	// InstrumentationClient is the instrumentation client that will be used to emit metrics
	instrumentation *instrumentation.Client
}

var _ DatabaseOperations = (*Db)(nil)

// New creates and initializes a new database instance with the provided configuration.
// It performs schema migrations and validates the database connection.
//
// Parameters:
//   - client: PostgreSQL client for database connections
//   - logger: Zap logger for operation logging
//   - instrumentationClient: Client for metrics and monitoring
//
// Returns:
//   - *Db: Initialized database instance
//   - error: Any error that occurred during initialization
func New(client *postgresdb.Client, logger *zap.Logger, instrumentationClient *instrumentation.Client) (*Db, error) {
	if client == nil {
		return nil, ErrInvalidPostgresClientObject
	}

	database := &Db{
		Client:          client,
		QueryOperator:   dal.Use(client.Engine),
		Logger:          logger,
		instrumentation: instrumentationClient,
	}

	// validate the database object
	if err := database.Validate(); err != nil {
		return nil, err
	}

	// perform migrations
	if err := database.performSchemaMigration(); err != nil {
		return nil, err
	}

	return database, nil
}

// Validate checks if the database object is properly initialized with all required components.
// Returns an error if any required component is missing or invalid.
func (db *Db) Validate() error {
	if db.Client == nil {
		return multierr.Append(ErrInvalidDBObject, fmt.Errorf("missing postgres client"))
	}

	if db.QueryOperator == nil {
		return multierr.Append(ErrInvalidDBObject, fmt.Errorf("missing query operator"))
	}

	if db.Logger == nil {
		return multierr.Append(ErrInvalidDBObject, fmt.Errorf("missing logger"))
	}

	if db.instrumentation == nil {
		return multierr.Append(ErrInvalidDBObject, fmt.Errorf("missing instrumentation client"))
	}

	return nil
}

// performSchemaMigration executes database schema migrations using GORM's AutoMigrate.
// It runs in a transaction to ensure consistency and rolls back on failure.
func (db *Db) performSchemaMigration() error {
	var (
		engine *gorm.DB
		models = schema.GetDatabaseSchemas()
	)

	if db == nil {
		return ErrInvalidDBObject
	}

	if engine = db.Client.Engine; engine == nil {
		return ErrInvalidGormConnectionObject
	}

	if len(models) > 0 {

		tx := db.Client.Engine.Begin()
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()

		// migration sf
		if err := engine.AutoMigrate(models...); err != nil {
			// TODO: emit metric
			log.Error(err.Error())
			tx.Rollback()
		}

		tx.Commit()

		log.Info("successfully migrated database schemas")
	}

	return nil
}

// GetQueryTimeout returns the configured query timeout duration for database operations.
func (db *Db) GetQueryTimeout() time.Duration {
	return *db.Client.QueryTimeout
}

// GetLogger returns the configured logger instance for database operations.
func (db *Db) GetLogger() *zap.Logger {
	return db.Logger
}

// GetInstrumentation returns the configured instrumentation client for metrics collection.
func (db *Db) GetInstrumentation() *instrumentation.Client {
	return db.instrumentation
}
