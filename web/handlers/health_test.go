package handlers_test

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gosom/google-maps-scraper/web/handlers"
)

// errDriver is a minimal sql/driver.Driver whose connections always fail.
type errDriver struct{}

func (errDriver) Open(_ string) (driver.Conn, error) {
	return nil, errors.New("mock: connection refused")
}

// registerMockDriver registers a uniquely-named failing driver and returns a
// *sql.DB backed by it. The DB is never nil, but every operation (including
// PingContext / QueryRowContext) will return an error, which is exactly what
// the health handler must handle.
func failingDB(t *testing.T) *sql.DB {
	t.Helper()
	driverName := "failing-" + t.Name()
	sql.Register(driverName, errDriver{})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestHealthCheck_DBUnreachable_Returns503(t *testing.T) {
	db := failingDB(t)

	deps := handlers.Dependencies{
		DB:      db,
		Version: "abc1234",
	}
	h := &handlers.WebHandlers{Deps: deps}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	h.HealthCheck(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}

	body, _ := io.ReadAll(rr.Body)
	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("could not parse JSON body: %v\nbody: %s", err, body)
	}

	if got := resp["status"]; got != "unhealthy" {
		t.Errorf(`expected status "unhealthy", got %q`, got)
	}
	if got := resp["db"]; got != "unreachable" {
		t.Errorf(`expected db "unreachable", got %q`, got)
	}
}

func TestHealthCheck_NilDB_Returns503(t *testing.T) {
	deps := handlers.Dependencies{
		DB:      nil,
		Version: "abc1234",
	}
	h := &handlers.WebHandlers{Deps: deps}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	h.HealthCheck(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for nil DB, got %d", rr.Code)
	}

	body, _ := io.ReadAll(rr.Body)
	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("could not parse JSON body: %v\nbody: %s", err, body)
	}

	if got := resp["status"]; got != "unhealthy" {
		t.Errorf(`expected status "unhealthy", got %q`, got)
	}
	if got := resp["db"]; got != "unreachable" {
		t.Errorf(`expected db "unreachable", got %q`, got)
	}
}
