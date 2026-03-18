package postgres

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestRunReaperTick_NoStuckJobs verifies that when no jobs are stuck the
// reaper runs without error and emits no warnings.
func TestRunReaperTick_NoStuckJobs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id, user_id, created_at`).
		WithArgs(4).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "created_at"}))

	log := slog.Default()
	runReaperTick(context.Background(), db, log, 4)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestRunReaperTick_StuckJobsUpdated verifies that stuck jobs are detected and
// updated to 'failed' with the correct failure_reason.
func TestRunReaperTick_StuckJobsUpdated(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	stuckAt := time.Now().Add(-5 * time.Hour)
	timeoutHours := 4

	selectRows := sqlmock.NewRows([]string{"id", "user_id", "created_at"}).
		AddRow("job-abc", "user-xyz", stuckAt)

	mock.ExpectQuery(`SELECT id, user_id, created_at`).
		WithArgs(timeoutHours).
		WillReturnRows(selectRows)

	expectedReason := "job timed out after 4 hours"

	mock.ExpectExec(`UPDATE jobs`).
		WithArgs(expectedReason, "job-abc").
		WillReturnResult(sqlmock.NewResult(1, 1))

	log := slog.Default()
	runReaperTick(context.Background(), db, log, timeoutHours)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestRunReaperTick_UpdateRaceCondition verifies that if a job's status
// changes between SELECT and UPDATE (rowsAffected == 0), the reaper does not
// treat it as an error.
func TestRunReaperTick_UpdateRaceCondition(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	stuckAt := time.Now().Add(-5 * time.Hour)
	timeoutHours := 4

	selectRows := sqlmock.NewRows([]string{"id", "user_id", "created_at"}).
		AddRow("job-race", "user-race", stuckAt)

	mock.ExpectQuery(`SELECT id, user_id, created_at`).
		WithArgs(timeoutHours).
		WillReturnRows(selectRows)

	// 0 rows affected — job was updated by something else between SELECT and UPDATE
	mock.ExpectExec(`UPDATE jobs`).
		WithArgs("job timed out after 4 hours", "job-race").
		WillReturnResult(sqlmock.NewResult(0, 0))

	log := slog.Default()
	runReaperTick(context.Background(), db, log, timeoutHours)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// TestRunStuckJobReaper_StopsOnContextCancel verifies the reaper goroutine
// exits cleanly when its context is cancelled.
func TestRunStuckJobReaper_StopsOnContextCancel(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	// No queries expected — context is cancelled before first tick fires
	// (interval is very long).
	_ = mock

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStuckJobReaper(ctx, db, slog.Default(), 10*time.Minute, 4)
	}()

	select {
	case <-done:
		// good — reaper exited
	case <-time.After(2 * time.Second):
		t.Error("reaper did not stop after context cancellation")
	}
}
