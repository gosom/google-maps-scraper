package database

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	postgresdb "github.com/SolomonAIEngineering/backend-core-library/database/postgres"
	"github.com/SolomonAIEngineering/backend-core-library/instrumentation"
	"github.com/SolomonAIEngineering/backend-monorepo/src/core/api-definitions/pkg/generated/user_service/dal"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
)

// dbName is the name of the test database file
const dbName = "gen_test.db"

// SAVE_POINT is the identifier used for database transaction savepoints
const SAVE_POINT = "test_save_point"

// testdb is the global test database instance
var testdb *gorm.DB

// once ensures the database is initialized only once
var once sync.Once

// conn is the global database connection instance
var conn *Db

var testContext *TestContext

// TestContext represents a complete test context for database tests
type TestContext struct {
	// Base test data
	Account          *lead_scraper_servicev1.Account
	Workspace        *lead_scraper_servicev1.Workspace
	AccountSettings  *lead_scraper_servicev1.AccountSettings

	// Database connection
	DB              *Db
}


type UserAccountTestContext struct {
	// The org ID tied to the user account
	OrgId uint64
	// The tenant ID tied to the user account
	TenantId uint64
	// The user account ID
	UserAccountId uint64
	// The user account
	UserAccount *lead_scraper_servicev1.Account
}


type WorkspaceTestContext struct {
	// The org ID tied to the team
	OrgId uint64
	// The tenant ID tied to the team
	TenantId uint64
	// The team ID
	AccountId uint64
	// The team
	Account *lead_scraper_servicev1.Account
	// The workspace
	Workspace *lead_scraper_servicev1.Workspace
}

// init initializes the test database connection
func init() {
	conn = NewTestDatabase()

	// create the test database
	testContext = NewTestContext(conn)
}

// NewTestDatabase creates a new in-memory test database instance with the user service schema.
// It panics if the database creation fails.
// Returns a configured Db instance ready for testing.
func NewTestDatabase() *Db {
	client, err := postgresdb.NewInMemoryTestDbClient(lead_scraper_servicev1.GetDatabaseSchemas()...)
	if err != nil {
		panic(fmt.Errorf("failed to create in memory test db client: %w", err))
	}

	return &Db{
		Client:          client,
		QueryOperator:   dal.Use(client.Engine),
		Logger:          zap.NewNop(),
		instrumentation: &instrumentation.Client{},
	}
}

// TxCleanupHandler manages transaction lifecycle and cleanup in tests.
// It provides functionality to handle transaction contexts, savepoints,
// and rollback operations.
type TxCleanupHandler struct {
	// cancelFunc cancels the transaction context
	cancelFunc context.CancelFunc
	// Tx is the active database transaction
	Tx *gorm.DB
	// savePointRollbackHandler is a function that handles rolling back to a savepoint
	savePointRollbackHandler func(tx *gorm.DB)
}

// txCleanupHandler creates a new TxCleanupHandler with a timeout context and initialized
// transaction. It:
// - Creates a context with a 10-second timeout
// - Begins a new transaction
// - Sets a savepoint for potential rollbacks
// Returns a configured TxCleanupHandler ready for use in tests.
func txCleanupHandler() *TxCleanupHandler {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	db := conn.Client.Engine
	tx := db.WithContext(ctx).Begin()
	tx.SavePoint(SAVE_POINT)

	return &TxCleanupHandler{
		cancelFunc: cancel,
		Tx:         tx,
		savePointRollbackHandler: func(tx *gorm.DB) {
			tx.RollbackTo(SAVE_POINT)
			tx.Commit()
		},
	}
}

// NewTestContext creates a new test context with all necessary test data
func NewTestContext(db *Db) *TestContext {
	// Generate test data using our utility
	config := testutils.DefaultGenerateConfig()
	testData := testutils.GenerateTestContext(config)

	// Create the test context
	ctx := &TestContext{
		Account:          testData.Account,
		Workspace:        testData.Workspace,
		DB:              db,
	}

	// Initialize the database with test data
	if err := ctx.initializeTestData(); err != nil {
		panic(fmt.Sprintf("failed to initialize test data: %v", err))
	}

	return ctx
}

// initializeTestData saves all test data to the database in the correct order
func (tc *TestContext) initializeTestData() error {
	// Start a transaction
	tx := tc.DB.Client.Engine.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// convert tc account to orm type
	acctOrm, err := tc.Account.ToORM(context.Background())
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to convert account to orm: %w", err)
	}

	// Save account and workspace first
	if err := tx.Create(&acctOrm).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to create account: %w", err)
	}

	// convert tc workspace to orm type
	workspaceOrm, err := tc.Workspace.ToORM(context.Background())
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to convert workspace to orm: %w", err)
	}

	if err := tx.Create(&workspaceOrm).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	
	return tx.Commit().Error
}

// Cleanup cleans up the test data
func (tc *TestContext) Cleanup() error {
	// Start a transaction
	tx := tc.DB.Client.Engine.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Delete(tc.Workspace).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	if err := tx.Delete(tc.Account).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete account: %w", err)
	}

	// Commit the transaction
	return tx.Commit().Error
}

func TestNew(t *testing.T) {
	type args struct {
		client                *postgresdb.Client
		logger                *zap.Logger
		instrumentationClient *instrumentation.Client
	}
	tests := []struct {
		name    string
		args    args
		want    *Db
		wantErr bool
	}{
		{
			name: "success - create new database instance",
			args: args{
				client:                conn.Client,
				logger:                zap.NewNop(),
				instrumentationClient: &instrumentation.Client{},
			},
			want: &Db{
				Client:          conn.Client,
				Logger:          zap.NewNop(),
				instrumentation: &instrumentation.Client{},
			},
			wantErr: false,
		},
		{
			name: "failure - nil client",
			args: args{
				client:                nil,
				logger:                zap.NewNop(),
				instrumentationClient: &instrumentation.Client{},
			},
			wantErr: true,
		},
		{
			name: "failure - nil logger",
			args: args{
				client:                conn.Client,
				logger:                nil,
				instrumentationClient: &instrumentation.Client{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.args.client, tt.args.logger, tt.args.instrumentationClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got.Client, tt.want.Client) {
				t.Errorf("New() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_Validate(t *testing.T) {
	tests := []struct {
		name    string
		db      *Db
		wantErr bool
	}{
		{
			name: "success - valid database instance",
			db: &Db{
				Client:          conn.Client,
				QueryOperator:   dal.Use(conn.Client.Engine),
				Logger:          zap.NewNop(),
				instrumentation: &instrumentation.Client{},
			},
			wantErr: false,
		},
		{
			name: "failure - nil client",
			db: &Db{
				Client:          nil,
				Logger:          zap.NewNop(),
				instrumentation: &instrumentation.Client{},
			},
			wantErr: true,
		},
		{
			name: "failure - nil logger",
			db: &Db{
				Client:          conn.Client,
				Logger:          nil,
				instrumentation: &instrumentation.Client{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.db.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Db.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDb_performSchemaMigration(t *testing.T) {
	tests := []struct {
		name    string
		db      *Db
		wantErr bool
	}{
		{
			name:    "success - perform schema migration",
			db:      conn,
			wantErr: false,
		},
		{
			name:    "failure - nil database",
			db:      nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.db.performSchemaMigration(); (err != nil) != tt.wantErr {
				t.Errorf("Db.performSchemaMigration() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDb_GetQueryTimeout(t *testing.T) {
	tests := []struct {
		name string
		db   *Db
		want time.Duration
	}{
		{
			name: "success - get default query timeout",
			db:   conn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.db.GetQueryTimeout()
			assert.NotNil(t, got)
		})
	}
}

func TestDb_GetLogger(t *testing.T) {
	logger := zap.NewNop()
	tests := []struct {
		name string
		db   *Db
		want *zap.Logger
	}{
		{
			name: "success - get logger",
			db: &Db{
				Client:          conn.Client,
				Logger:          logger,
				instrumentation: &instrumentation.Client{},
			},
			want: logger,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.db.GetLogger(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.GetLogger() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_GetInstrumentation(t *testing.T) {
	instrumentationClient := &instrumentation.Client{}
	tests := []struct {
		name string
		db   *Db
		want *instrumentation.Client
	}{
		{
			name: "success - get instrumentation client",
			db: &Db{
				Client:          conn.Client,
				Logger:          zap.NewNop(),
				instrumentation: instrumentationClient,
			},
			want: instrumentationClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.db.GetInstrumentation(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.GetInstrumentation() = %v, want %v", got, tt.want)
			}
		})
	}
}
