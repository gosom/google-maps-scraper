package postgres_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pquerna/otp/totp"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/admin"
	"github.com/gosom/google-maps-scraper/admin/postgres"
	"github.com/gosom/google-maps-scraper/cryptoext"
	ratelimitpostgres "github.com/gosom/google-maps-scraper/ratelimit/postgres"
)

// Use an isolated schema so this test can run concurrently in a shared test database.
func sessionTestDB(t *testing.T) (*pgxpool.Pool, *sql.DB) {
	t.Helper()

	dsn := os.Getenv("GMAPS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GMAPS_TEST_POSTGRES_DSN to run the PostgreSQL session tests")
	}

	ctx := t.Context()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)

	control, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(control.Close)

	schema := pgx.Identifier{"test_sessions_" + uuid.NewString()}.Sanitize()
	_, err = control.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := control.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})

	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	db := stdlib.OpenDBFromPool(pool)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return pool, db
}

func sessionMigration(t *testing.T, db *sql.DB, file string, direction migrate.MigrationDirection) {
	t.Helper()

	f, err := os.OpenInRoot("../../migrations", file)
	require.NoError(t, err)

	defer f.Close()

	entry, err := migrate.ParseMigration(file, f)
	require.NoError(t, err)

	statements := entry.Up
	if direction == migrate.Down {
		statements = entry.Down
	}

	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	for _, statement := range statements {
		_, err := tx.ExecContext(t.Context(), statement)
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit())
}

func TestSessionMigrationAndVerification(t *testing.T) {
	const migration = "20261124120000-add_admin_sessions_pending_2fa.sql"

	pool, db := sessionTestDB(t)
	ctx := t.Context()

	sessionMigration(t, db, "20260124164611-admin_and_provisioning.sql", migrate.Up)
	_, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash) VALUES ('fixture', 'unused')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO admin_sessions (id, user_id, created_at, expires_at, ip_address, user_agent)
		VALUES ('legacy-pending', 1, NOW(), NOW() + interval '5 minutes', '', ''),
		       ('already-expired', 1, NOW() - interval '6 minutes', NOW() - interval '1 minute', '', ''),
		       ('legacy-full', 1, NOW() - interval '23 hours 59 minutes', NOW() + interval '1 minute', '', '')`)
	require.NoError(t, err)

	sessionMigration(t, db, migration, migrate.Up)
	_, err = db.ExecContext(ctx, `SELECT id FROM admin_sessions`)

	var missingColumn *pgconn.PgError

	require.ErrorAs(t, err, &missingColumn)
	require.Equal(t, "42703", missingColumn.Code) // undefined_column

	store := postgres.NewWithPool(pool, nil)
	_, err = store.GetSession(ctx, "legacy-pending")
	require.ErrorIs(t, err, admin.ErrSessionNotFound)
	_, err = store.GetSession(ctx, "already-expired")
	require.ErrorIs(t, err, admin.ErrSessionExpired)
	_, err = store.GetSession(ctx, "legacy-full")
	require.NoError(t, err)

	pending, err := store.CreatePendingSession(ctx, 1, "", "", 5*time.Minute)
	require.NoError(t, err)
	_, err = store.GetSession(ctx, pending.ID)
	require.ErrorIs(t, err, admin.ErrSessionNotFound)
	_, err = store.GetPendingSession(ctx, pending.ID)
	require.NoError(t, err)

	type completion struct {
		session *admin.Session
		err     error
	}

	results := make(chan completion, 2)

	for range 2 {
		go func() {
			session, err := store.CompletePendingSession(ctx, pending.ID, "", "", 24*time.Hour)
			results <- completion{session, err}
		}()
	}

	var completed []*admin.Session

	for range 2 {
		result := <-results
		if result.err != nil {
			require.ErrorIs(t, result.err, admin.ErrSessionNotFound)
		} else {
			completed = append(completed, result.session)
		}
	}

	require.Len(t, completed, 1)
	full := completed[0]
	require.NotEqual(t, pending.ID, full.ID)
	_, err = store.GetSession(ctx, full.ID)
	require.NoError(t, err)
	_, err = store.GetPendingSession(ctx, full.ID)
	require.ErrorIs(t, err, admin.ErrSessionNotFound)
	_, err = store.CompletePendingSession(ctx, full.ID, "", "", 24*time.Hour)
	require.ErrorIs(t, err, admin.ErrSessionNotFound)
	_, err = store.GetSession(ctx, pending.ID)
	require.ErrorIs(t, err, admin.ErrSessionNotFound)

	expired, err := store.CreatePendingSession(ctx, 1, "", "", -time.Minute)
	require.NoError(t, err)
	_, err = store.CompletePendingSession(ctx, expired.ID, "", "", time.Hour)
	require.ErrorIs(t, err, admin.ErrSessionNotFound)

	t.Run("verification handler", func(t *testing.T) {
		sessionMigration(t, db, "20260124213201-add_totp_2fa.sql", migrate.Up)
		sessionMigration(t, db, "20260130100000-create_rate_limits.sql", migrate.Up)
		testVerificationHandler(t, pool)
	})

	unfinished, err := store.CreatePendingSession(ctx, 1, "", "", 5*time.Minute)
	require.NoError(t, err)
	sessionMigration(t, db, migration, migrate.Down)

	var count int

	err = db.QueryRowContext(ctx, `SELECT count(*) FROM admin_sessions WHERE id = $1`, unfinished.ID).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count)

	err = db.QueryRowContext(ctx, `SELECT count(*) FROM admin_sessions WHERE id = $1`, full.ID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func testVerificationHandler(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := t.Context()
	key, _, err := cryptoext.GenerateEncryptionKey()
	require.NoError(t, err)

	store := postgres.NewWithPool(pool, key)
	app, err := admin.NewAppState(store, ratelimitpostgres.New(pool), key)
	require.NoError(t, err)

	otp, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "fixture"})
	require.NoError(t, err)
	require.NoError(t, store.SetTOTPSecret(ctx, 1, otp.Secret()))
	require.NoError(t, store.EnableTOTP(ctx, 1))

	pending, err := store.CreatePendingSession(ctx, 1, "", "", 5*time.Minute)
	require.NoError(t, err)

	guard := admin.SessionAuth(store, app.CookieName)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	authorizedStatus := func(id string) int {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Cookie", app.CookieName+"="+id)

		rec := httptest.NewRecorder()
		guard.ServeHTTP(rec, req)

		return rec.Code
	}
	require.Equal(t, http.StatusSeeOther, authorizedStatus(pending.ID))

	verify := func(code string) *http.Response {
		form := url.Values{"code": {code}}
		req := httptest.NewRequest(http.MethodPost, "/admin/login/2fa", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", app.CookieName+"_pending="+pending.ID)

		rec := httptest.NewRecorder()
		admin.TwoFactorVerifySubmitHandler(app)(rec, req)

		return rec.Result()
	}

	invalid := verify("invalid")
	defer invalid.Body.Close()

	require.Contains(t, invalid.Header.Get("Location"), "Invalid+code")
	require.Empty(t, invalid.Cookies())

	code, err := totp.GenerateCode(otp.Secret(), time.Now())
	require.NoError(t, err)

	valid := verify(code)
	defer valid.Body.Close()

	require.Equal(t, "/admin", valid.Header.Get("Location"))

	var completedID string

	for _, cookie := range valid.Cookies() {
		if cookie.Name == app.CookieName {
			completedID = cookie.Value
		}
	}

	require.NotEmpty(t, completedID)
	require.Equal(t, http.StatusNoContent, authorizedStatus(completedID))
	require.Equal(t, http.StatusSeeOther, authorizedStatus(pending.ID))
}
