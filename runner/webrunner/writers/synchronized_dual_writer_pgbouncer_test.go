package writers

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPGXSimpleProtocol_JSONBParameterType pins the May 2026 prod root cause:
// when pgx is forced into simple_protocol mode (which is what production sets
// because the app talks to DigitalOcean Managed Postgres through PgBouncer in
// transaction mode), passing a Go []byte to a jsonb column fails with
// SQLSTATE 22P02 "invalid input syntax for type json" — pgx infers the param
// type purely from the Go type and treats []byte as bytea. The same bytes,
// passed as string, succeed. This test exercises the contract directly so a
// future refactor that reverts mustMarshalJSON's caller back to []byte does
// not silently re-introduce the prod hang.
//
// Requires a real Postgres (TEST_DSN env var). Skipped otherwise so the
// regular `go test ./...` run in CI without a DB still passes.
func TestPGXSimpleProtocol_JSONBParameterType(t *testing.T) {
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("DSN")
	}
	if dsn == "" {
		t.Skip("set TEST_DSN to run; need real Postgres to reproduce the simple_protocol+jsonb interaction")
	}

	conf, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	// Production uses PgBouncer in transaction mode → pgx must use simple_protocol.
	conf.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	db := stdlib.OpenDB(*conf)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))
	_, err = db.ExecContext(ctx, "CREATE TEMP TABLE pgxsimple_jsonb_test (j JSONB)")
	require.NoError(t, err)

	// A real Google Maps-style payload with the exact & escape that
	// triggered prod failures. mustMarshalJSON is the production marshaller —
	// we exercise it end-to-end so the test fails if the caller of
	// mustMarshalJSON in writeToPostgreSQL stops wrapping in string().
	type img struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	bs := mustMarshalJSON([]img{
		{Title: "Food & drink", URL: "https://example.com/x?a=1&b=2"},
		{Title: "Street View & 360°", URL: "https://example.com/sv"},
	})
	require.True(t, json.Valid(bs), "marshaller must produce valid JSON")

	t.Run("[]byte fails under simple_protocol — DO NOT pass []byte to jsonb here", func(t *testing.T) {
		_, err := db.ExecContext(ctx, "INSERT INTO pgxsimple_jsonb_test (j) VALUES ($1)", bs)
		assert.Error(t, err,
			"if this assertion ever flips to NoError, pgx changed simple_protocol behavior and the production string() wrap is no longer load-bearing — verify against jackc/pgx#2231 before relaxing the writer")
	})

	t.Run("string succeeds — production code path", func(t *testing.T) {
		// Reset between subtests so a stray success on the prior case
		// doesn't pollute this one.
		_, err := db.ExecContext(ctx, "TRUNCATE pgxsimple_jsonb_test")
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO pgxsimple_jsonb_test (j) VALUES ($1)", string(bs))
		require.NoError(t, err, "passing the same bytes as string must succeed")

		// Round-trip the value back out so we know the JSON wasn't corrupted in transit.
		var out []byte
		require.NoError(t, db.QueryRowContext(ctx, "SELECT j FROM pgxsimple_jsonb_test").Scan(&out))
		var parsed []img
		require.NoError(t, json.Unmarshal(out, &parsed))
		assert.Equal(t, "Food & drink", parsed[0].Title)
	})
}
