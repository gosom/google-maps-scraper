package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

/*
MigrationRunner executes postgres database schema migrations using the golang-migrate library.
It locates migration files (.up.sql/.down.sql) in scripts/migrations by default, but this path
can be customized via SetMigrationsDir(). Migrations run sequentially by version number,
and applied migrations are tracked in the schema_migrations table. The runner handles the postgres connection,
transaction management, and provides logging of the migration process.

Migration files must follow the naming convention: {version}_{description}.up.sql and {version}_{description}.down.sql
where version is a numeric identifier (e.g., 000001) that determines execution order.
*/
type MigrationRunner struct {
	dsn           string
	migrationsDir string
	logger        *log.Logger
	timeout       time.Duration
}

func NewMigrationRunner(dsn string) *MigrationRunner {
	return &MigrationRunner{
		dsn:     dsn,
		logger:  log.New(os.Stdout, "[Migration] ", log.LstdFlags),
		timeout: 30 * time.Second,
	}
}

func (m *MigrationRunner) SetMigrationsDir(dir string) error {
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	fileInfo, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("directory not accessible: %w", err)
	}

	if !fileInfo.IsDir() {
		return fmt.Errorf("path is not a directory: %s", absPath)
	}
	m.migrationsDir = absPath
	return nil
}

func (m *MigrationRunner) SetTimeout(timeout time.Duration) {
	m.timeout = timeout
}

func (m *MigrationRunner) RunMigrations() error {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	migrationsDir, err := m.findMigrationsDir()
	if err != nil {
		return fmt.Errorf("failed to find migrations directory: %w", err)
	}

	m.logger.Printf("Using migrations from: %s", migrationsDir)

	migrator, err := m.createMigrator(ctx, migrationsDir)
	if err != nil {
		return err
	}

	if err := migrator.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			m.logger.Println("No migrations to apply - database is up to date")
			return nil
		}
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	m.logger.Println("Successfully applied migrations")
	return nil
}

func (m *MigrationRunner) createMigrator(ctx context.Context, migrationsDir string) (*migrate.Migrate, error) {
	migrateDSN := m.formatDSN()

	db, err := sql.Open("pgx", migrateDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Minute * 5)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	dbInstance, err := postgres.WithInstance(db, &postgres.Config{
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create migration driver: %w", err)
	}

	sourceURL := fmt.Sprintf("file://%s", migrationsDir)
	migrator, err := migrate.NewWithDatabaseInstance(
		sourceURL,
		"postgres",
		dbInstance,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create migration instance: %w", err)
	}

	return migrator, nil
}

func (m *MigrationRunner) formatDSN() string {
	if !strings.HasPrefix(m.dsn, "postgres://") {
		return "postgres://" + m.dsn
	}
	return m.dsn
}

func (m *MigrationRunner) findMigrationsDir() (string, error) {
	if m.migrationsDir != "" {
		if _, err := os.Stat(m.migrationsDir); err == nil {
			return m.migrationsDir, nil
		}
		return "", fmt.Errorf("specified migrations directory not found: %s", m.migrationsDir)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("unable to determine working directory: %w", err)
	}

	migrationsPath := filepath.Join(workingDir, "scripts", "migrations")
	m.logger.Printf("Looking for migrations in: %s", migrationsPath)

	if _, err := os.Stat(migrationsPath); err != nil {
		return "", fmt.Errorf("migrations directory not found at %s: %w", migrationsPath, err)
	}

	return migrationsPath, nil
}
