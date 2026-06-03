package postgres //nolint:testpackage // tests need unexported clock hooks on resultWriter.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func TestResultWriterResetsSaveIntervalAfterTimedFlush(t *testing.T) {
	db, execs := newCountingDB(t)
	defer db.Close()

	base := time.Date(2026, time.May, 30, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: base}
	writer := &resultWriter{
		db:           db,
		now:          clock.Now,
		saveInterval: time.Minute,
	}

	in := make(chan scrapemate.Result)
	done := make(chan error, 1)

	go func() {
		done <- writer.Run(context.Background(), in)
	}()

	in <- resultWithEntry("first")

	clock.Set(base.Add(time.Minute + time.Second))
	in <- resultWithEntry("second")

	require.Eventually(t, func() bool {
		return execs.Load() == 1
	}, time.Second, 10*time.Millisecond)

	clock.Set(base.Add(time.Minute + 2*time.Second))
	in <- resultWithEntry("third")

	require.Never(t, func() bool {
		return execs.Load() > 1
	}, 100*time.Millisecond, 10*time.Millisecond)

	close(in)
	require.NoError(t, <-done)
	require.Equal(t, int64(2), execs.Load())
}

func resultWithEntry(id string) scrapemate.Result {
	return scrapemate.Result{
		Data: &gmaps.Entry{
			ID:         id,
			Title:      id,
			Latitude:   1,
			Longtitude: 2,
		},
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = now
}

var countingDriverSeq atomic.Int64

func newCountingDB(t *testing.T) (*sql.DB, *atomic.Int64) {
	t.Helper()

	execs := &atomic.Int64{}
	driverName := fmt.Sprintf("counting-resultwriter-%d", countingDriverSeq.Add(1))
	sql.Register(driverName, &countingDriver{execs: execs})

	db, err := sql.Open(driverName, "")
	require.NoError(t, err)

	return db, execs
}

type countingDriver struct {
	execs *atomic.Int64
}

func (d *countingDriver) Open(string) (driver.Conn, error) {
	return &countingConn{execs: d.execs}, nil
}

type countingConn struct {
	execs *atomic.Int64
}

func (c *countingConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepare is not implemented")
}

func (c *countingConn) Close() error {
	return nil
}

func (c *countingConn) Begin() (driver.Tx, error) {
	return countingTx{}, nil
}

func (c *countingConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return countingTx{}, nil
}

func (c *countingConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.execs.Add(1)

	return driver.RowsAffected(1), nil
}

func (c *countingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, io.EOF
}

type countingTx struct{}

func (countingTx) Commit() error {
	return nil
}

func (countingTx) Rollback() error {
	return nil
}
