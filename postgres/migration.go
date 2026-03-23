package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
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
	logger        *slog.Logger
	timeout       time.Duration
}

func NewMigrationRunner(dsn string) *MigrationRunner {
	return &MigrationRunner{
		dsn:     dsn,
		logger:  pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "migration"),
		timeout: 120 * time.Second,
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

// migrationAdvisoryLockID is a PostgreSQL advisory lock ID derived from an FNV-32a
// hash of a domain-specific string, reducing collision risk with other lock users.
var migrationAdvisoryLockID = func() int64 {
	h := fnv.New32a()
	h.Write([]byte("google-maps-scraper:migrations"))
	return int64(h.Sum32())
}()

func (m *MigrationRunner) RunMigrations() error {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	migrationsDir, err := m.findMigrationsDir()
	if err != nil {
		return fmt.Errorf("failed to find migrations directory: %w", err)
	}

	m.logger.Info("using_migrations", slog.String("dir", migrationsDir))

	// Acquire an advisory lock to prevent concurrent migrations from multiple pods.
	// We pin to a single *sql.Conn so the lock stays on one connection for
	// the entire duration of the migration.
	lockPool, err := sql.Open("pgx", m.formatDSN())
	if err != nil {
		return fmt.Errorf("failed to open lock connection pool: %w", err)
	}
	defer lockPool.Close()

	lockConn, err := lockPool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to obtain single lock connection: %w", err)
	}
	defer lockConn.Close()

	if err := lockConn.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping lock connection: %w", err)
	}

	if err := m.acquireAdvisoryLock(ctx, lockConn); err != nil {
		return fmt.Errorf("failed to acquire migration lock: %w", err)
	}
	defer m.releaseAdvisoryLock(lockConn)

	migrator, err := m.createMigrator(ctx, migrationsDir)
	if err != nil {
		return err
	}
	defer func() {
		sourceErr, dbErr := migrator.Close()
		if sourceErr != nil {
			m.logger.Warn("migrator_source_close_error", slog.Any("error", sourceErr))
		}
		if dbErr != nil {
			m.logger.Warn("migrator_db_close_error", slog.Any("error", dbErr))
		}
	}()

	if err := migrator.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			m.logger.Info("no_migrations_to_apply")
			return nil
		}
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	m.logger.Info("migrations_applied_successfully")
	return nil
}

// acquireAdvisoryLock acquires a PostgreSQL advisory lock on a pinned
// connection. pg_advisory_lock blocks until the lock is available; the
// context timeout handles the deadline.
func (m *MigrationRunner) acquireAdvisoryLock(ctx context.Context, conn *sql.Conn) error {
	m.logger.Info("migration_acquiring_lock")
	_, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLockID)
	if err != nil {
		return fmt.Errorf("failed to acquire migration advisory lock: %w", err)
	}
	m.logger.Info("migration_lock_acquired")
	return nil
}

// releaseAdvisoryLock releases the PostgreSQL advisory lock on the pinned connection.
// Uses a background context since the original context may have been cancelled.
func (m *MigrationRunner) releaseAdvisoryLock(conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var released bool
	err := conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", migrationAdvisoryLockID).Scan(&released)
	if err != nil {
		m.logger.Warn("migration_lock_release_failed", slog.Any("error", err))
		return
	}

	if released {
		m.logger.Info("migration_lock_released")
	} else {
		m.logger.Warn("migration_lock_not_held", slog.String("detail", "pg_advisory_unlock returned false; lock was not held by this session"))
	}
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
	m.logger.Info("looking_for_migrations", slog.String("path", migrationsPath))

	if _, err := os.Stat(migrationsPath); err != nil {
		return "", fmt.Errorf("migrations directory not found at %s: %w", migrationsPath, err)
	}

	return migrationsPath, nil
}
