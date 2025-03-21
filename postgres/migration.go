package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// MigrationRunner handles database migrations
type MigrationRunner struct {
	dsn           string
	migrationsDir string
}

// NewMigrationRunner creates a new migration manager
func NewMigrationRunner(dsn string) *MigrationRunner {
	return &MigrationRunner{
		dsn: dsn,
	}
}

// SetMigrationsDir sets the migrations directory
func (m *MigrationRunner) SetMigrationsDir(dir string) {
	m.migrationsDir = dir
}

// RunMigrations runs all migrations
func (m *MigrationRunner) RunMigrations() error {
	// Find migrations directory
	migrationsDir, err := m.findMigrationsDir()
	if err != nil {
		return fmt.Errorf("failed to find migrations directory: %w", err)
	}

	log.Printf("Using migrations from: %s", migrationsDir)

	// Format DSN for golang-migrate (needs postgres:// prefix)
	migrateDSN := m.dsn
	if !strings.HasPrefix(migrateDSN, "postgres://") {
		migrateDSN = "postgres://" + migrateDSN
	}

	// Connect to database
	db, err := sql.Open("pgx", migrateDSN)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	// Create driver instance
	dbInstance, err := postgres.WithInstance(db, &postgres.Config{
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	// Create migrate instance
	sourceURL := fmt.Sprintf("file://%s", migrationsDir)
	m2, err := migrate.NewWithDatabaseInstance(
		sourceURL,
		"postgres",
		dbInstance,
	)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	// Run migrations
	err = m2.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	if errors.Is(err, migrate.ErrNoChange) {
		log.Println("No migrations to apply - database is up to date")
	} else {
		log.Println("Successfully applied migrations")
	}

	return nil
}

// findMigrationsDir looks for migrations in various locations
func (m *MigrationRunner) findMigrationsDir() (string, error) {
	// Use explicit directory if set
	if m.migrationsDir != "" {
		if _, err := os.Stat(m.migrationsDir); err == nil {
			return m.migrationsDir, nil
		}
		return "", fmt.Errorf("specified migrations directory not found: %s", m.migrationsDir)
	}

	// Try standard locations
	searchPaths := []string{
		"scripts/migrations",                // Relative to working directory
		filepath.Join("scripts", "migrations"), // Alternative relative path
	}

	// Add absolute paths
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		searchPaths = append(searchPaths, filepath.Join(execDir, "scripts", "migrations"))
	}

	workingDir, err := os.Getwd()
	if err == nil {
		searchPaths = append(searchPaths, filepath.Join(workingDir, "scripts", "migrations"))
	}

	// Log all search paths
	log.Println("Searching for migrations in the following locations:")
	for _, path := range searchPaths {
		log.Printf("  - %s", path)
	}

	// Check each location
	var validPaths []string
	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			validPaths = append(validPaths, path)
		}
	}

	if len(validPaths) == 0 {
		return "", fmt.Errorf("no valid migrations directory found")
	}

	// Log found paths
	log.Printf("Found %d valid migrations directories:", len(validPaths))
	for _, path := range validPaths {
		log.Printf("  - %s", path)
	}

	// Use the first valid path
	return validPaths[0], nil
}