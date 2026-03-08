package migrations

import (
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // Register pgx driver for database/sql
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed *.sql
var migrationsFS embed.FS

// Run executes all pending migrations on the given database
func Run(db *sql.DB) (int, error) {
	migrations := &migrate.EmbedFileSystemMigrationSource{
		FileSystem: migrationsFS,
		Root:       ".",
	}

	n, err := migrate.Exec(db, "postgres", migrations, migrate.Up)
	if err != nil {
		return 0, fmt.Errorf("failed to run migrations: %w", err)
	}

	return n, nil
}

// RunWithDSN executes all pending migrations using the given connection string
func RunWithDSN(dsn string) (int, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return 0, fmt.Errorf("failed to connect to database: %w", err)
	}

	defer func() { _ = db.Close() }()

	return Run(db)
}
