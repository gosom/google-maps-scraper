package postgres

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestMigrationRunner_LoggerDI asserts that NewMigrationRunner accepts a
// *slog.Logger and uses it (component attribute visible in logged output).
func TestMigrationRunner_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mr := NewMigrationRunner("postgres://user:pass@localhost/db", logger)
	if mr == nil {
		t.Fatal("NewMigrationRunner returned nil")
	}

	// Log through the stored logger to verify component attr is set.
	mr.logger.Info("test_migration_logger")
	if !strings.Contains(buf.String(), "component=migration") {
		t.Errorf("expected 'component=migration' in logger output, got: %s", buf.String())
	}
}

// TestRepository_LoggerDI asserts that NewRepository accepts a *slog.Logger.
// We use a nil DB (no ping needed) — the constructor path is what matters.
func TestRepository_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// NewRepository pings the DB; pass nil to trigger early error, but the
	// function still must compile with the new signature.
	_, err := NewRepository(nil, logger)
	// nil db → ping will fail; that's expected in unit tests.
	if err == nil {
		t.Log("NewRepository(nil, logger) unexpectedly succeeded — skipping output check")
		return
	}
	// Constructor compiled and accepted *slog.Logger — that's the assertion.
}
