package postgres

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestMigrationRunner_LoggerDI asserts that NewMigrationRunner accepts a
// *slog.Logger and tags it with module=migration (NOT component=migration —
// `component` is owned by main.go for the runner identity, see web/web.go
// for the same convention).
func TestMigrationRunner_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mr := NewMigrationRunner("postgres://user:pass@localhost/db", logger)
	if mr == nil {
		t.Fatal("NewMigrationRunner returned nil")
	}

	mr.logger.Info("test_migration_logger")
	out := buf.String()
	if !strings.Contains(out, "module=migration") {
		t.Errorf("expected 'module=migration' in logger output, got: %s", out)
	}
	if strings.Contains(out, "component=migration") {
		t.Errorf("migration must not tag itself as component=… (reserved for runner level), got: %s", out)
	}
}

// TestMigrationRunner_AcceptsBothSchemes is a regression test for a bug where
// the migration runner double-prefixed DSNs that already used the canonical
// "postgresql://" scheme (DigitalOcean's default), producing
// "postgres://postgresql://..." which pgx parsed with host="postgresql" and
// the entire user:pass@host:port/db tail shoved into the Database field.
//
// After the fix, the DSN is passed straight through to pgx, which accepts
// both "postgres://" and "postgresql://" natively per libpq's URI spec.
// We assert the runner stores the DSN verbatim (no string mangling).
func TestMigrationRunner_AcceptsBothSchemes(t *testing.T) {
	cases := []string{
		"postgres://user:pass@localhost:5432/db",
		"postgresql://user:pass@localhost:5432/db",
		"postgresql://user:pass@host:25060/db?sslmode=verify-full&sslrootcert=/etc/x.crt",
		"host=localhost user=u password=p dbname=d", // libpq key-value form
	}
	for _, dsn := range cases {
		mr := NewMigrationRunner(dsn, slog.Default())
		if mr == nil {
			t.Fatalf("NewMigrationRunner returned nil for %q", dsn)
		}
		if mr.dsn != dsn {
			t.Errorf("DSN mangled: input=%q stored=%q", dsn, mr.dsn)
		}
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
