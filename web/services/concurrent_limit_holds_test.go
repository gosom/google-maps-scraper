package services

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// pqArrayStrings is a thin alias keeping pq.Array's intent obvious at the
// reaper-CTE call site, where the WHERE id = ANY($1) takes a TEXT[].
func pqArrayStrings(s []string) any { return pq.Array(s) }

// TestCreateJobWithLimit_ReservesAndReleasesHold pins the contract that
// CreateJobWithLimit increments credit_held_precise by EstimatedCost and
// that ReleaseHold decrements it by the same amount, leaving the user
// row in its original state. Skipped without TEST_DSN.
func TestCreateJobWithLimit_ReservesAndReleasesHold(t *testing.T) {
	db, userID := openOrSkip(t)
	defer cleanupUser(t, db, userID)

	const balance = "10.000000"
	seedUser(t, db, userID, balance)

	svc := NewConcurrentLimitService(db)

	job := &models.Job{
		ID:     uuid.Must(uuid.NewV7()).String(),
		UserID: userID,
		Name:   "hold-test",
		Status: models.StatusPending,
		Date:   time.Now().UTC(),
	}
	err := svc.CreateJobWithLimit(context.Background(), job, &JobLimitOpts{
		EstimatedCost:   3.5,
		EstimatedPlaces: 40,
	})
	require.NoError(t, err)

	// Hold should be exactly the estimate; balance unchanged.
	heldAfterCreate := readHold(t, db, userID)
	require.Equal(t, "3.500000", heldAfterCreate, "credit_held_precise must equal EstimatedCost after submission")
	require.Equal(t, balance, readBalance(t, db, userID), "credit_balance must NOT change at submission (charge happens at end-of-job)")

	// estimated_cost_precise must be persisted on the job row.
	require.Equal(t, "3.500000", readJobEstimate(t, db, job.ID), "jobs.estimated_cost_precise must equal the quote shown to the user")

	// Release the hold and confirm the row returns to clean state.
	require.NoError(t, svc.ReleaseHold(context.Background(), userID, 3.5))
	require.Equal(t, "0.000000", readHold(t, db, userID), "credit_held_precise must return to 0 after release")
	require.Equal(t, balance, readBalance(t, db, userID), "credit_balance still untouched after release (charge is independent)")
}

// TestCreateJobWithLimit_ConcurrentSubmissionRespectsHold pins the
// adversarial scenario from the architectural review: balance=1.0, two
// concurrent jobs each estimated 0.7. With reservations, exactly one
// job must succeed and the other must get ErrInsufficientBalance —
// pre-2026-05-10 both would pass the gate because neither had charged
// at gate time, then the second's end-of-job ChargeAllJobEvents would
// race the first's and one user would silently get a free scrape.
//
// Skipped without TEST_DSN.
func TestCreateJobWithLimit_ConcurrentSubmissionRespectsHold(t *testing.T) {
	db, userID := openOrSkip(t)
	defer cleanupUser(t, db, userID)

	seedUser(t, db, userID, "1.000000")

	svc := NewConcurrentLimitService(db)
	const concurrentJobCost = 0.7
	// Bump the per-user concurrent-jobs cap so we can submit two from
	// the same user in this test without hitting the unrelated
	// ErrConcurrentJobLimitReached.
	_, err := db.Exec(`UPDATE users SET max_concurrent_jobs = 5 WHERE id = $1`, userID)
	require.NoError(t, err)

	type result struct {
		jobID string
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job := &models.Job{
				ID:     uuid.Must(uuid.NewV7()).String(),
				UserID: userID,
				Name:   "concurrent-hold-test",
				Status: models.StatusPending,
				Date:   time.Now().UTC(),
			}
			err := svc.CreateJobWithLimit(context.Background(), job, &JobLimitOpts{
				EstimatedCost:   concurrentJobCost,
				EstimatedPlaces: 40,
			})
			results <- result{jobID: job.ID, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var winners, losers int
	for r := range results {
		if r.err == nil {
			winners++
			continue
		}
		var insufficient ErrInsufficientBalance
		require.True(t, errors.As(r.err, &insufficient),
			"on contention the loser must fail with ErrInsufficientBalance, got %T: %v", r.err, r.err)
		losers++
	}
	require.Equal(t, 1, winners, "exactly one of two concurrent submissions must succeed")
	require.Equal(t, 1, losers, "the other must get ErrInsufficientBalance — this is the bug being fixed")

	// The single winner reserved 0.7. balance(1.0) - held(0.7) = 0.3
	// is the available figure the next submission would see.
	require.Equal(t, "0.700000", readHold(t, db, userID))
}

// TestCreditHolds_OverQuoteEndOfJobCharge pins the regression that the
// architectural-review code review caught:
//
//	The pre-fix migration shipped a `CHECK (credit_held_precise <=
//	credit_balance)` constraint. Postgres re-evaluates row-level CHECKs
//	on every UPDATE of any column in the row, regardless of which
//	columns changed. The end-of-job charge in ChargeAllJobEvents
//	decrements credit_balance BEFORE the deferred releaseHoldAndLogBilling
//	runs, so any actual cost large enough to drive
//	(balance - actual) < held would trip the CHECK, roll back the
//	charge transaction, leave the job in "failed" with results already
//	in the DB, and silently free the user from paying — exactly the
//	adversarial vector the migration was meant to close.
//
// This test simulates the prod end-of-job sequence directly:
//  1. seed user with balance B and zero held
//  2. submit a job with estimate E (B and E chosen so available = B-E
//     is intentionally LESS than the actual we will charge)
//  3. issue an UPDATE that decrements credit_balance by an actual
//     cost A > B-E (the over-quote case the CHECK was rejecting)
//  4. release the hold via the same path webrunner uses
//  5. assert end state: balance = B - A, held = 0, no errors
//
// If anyone ever reintroduces the (held <= balance) CHECK the step-3
// UPDATE will fail and this test will go red with a clear
// "check_violation" SQLSTATE 23514 in the error.
func TestCreditHolds_OverQuoteEndOfJobCharge(t *testing.T) {
	db, userID := openOrSkip(t)
	defer cleanupUser(t, db, userID)

	// B = 10, E = 3, A = 8. After the simulated charge the user
	// should be at balance=2, held=0, total spent=8.
	const (
		balance          = "10.000000"
		estimate         = 3.0
		actualCharge     = 8.0
		balanceAfter     = "2.000000"
		heldAfterRelease = "0.000000"
	)
	seedUser(t, db, userID, balance)

	svc := NewConcurrentLimitService(db)

	job := &models.Job{
		ID:     uuid.Must(uuid.NewV7()).String(),
		UserID: userID,
		Name:   "over-quote-regression",
		Status: models.StatusPending,
		Date:   time.Now().UTC(),
	}
	require.NoError(t, svc.CreateJobWithLimit(context.Background(), job, &JobLimitOpts{
		EstimatedCost:   estimate,
		EstimatedPlaces: 40,
	}))
	require.Equal(t, "3.000000", readHold(t, db, userID))

	// Step 3: simulate ChargeAllJobEvents' atomic predicate-decrement.
	// This is the EXACT shape billing/service.go uses so a future
	// reintroduction of the (held<=balance) CHECK would fail HERE,
	// where the CHECK is evaluated against the new row state.
	res, err := db.Exec(
		`UPDATE users SET credit_balance = credit_balance - $1::numeric
		 WHERE id = $2 AND credit_balance >= $1::numeric`,
		actualCharge, userID,
	)
	require.NoError(t, err, "the actual end-of-job charge MUST NOT trip a CHECK constraint when balance-after-charge < held")
	rows, _ := res.RowsAffected()
	require.Equal(t, int64(1), rows, "the predicate-decrement must succeed when the user has enough balance to cover the actual")

	// Step 4: release the hold via the same path webrunner uses.
	require.NoError(t, svc.ReleaseHold(context.Background(), userID, estimate))

	// Step 5: end state.
	require.Equal(t, balanceAfter, readBalance(t, db, userID),
		"user paid the actual (8) — the original 10 minus 8 = 2")
	require.Equal(t, heldAfterRelease, readHold(t, db, userID),
		"hold released regardless of whether actual exceeded estimate")
}

// TestCreditHolds_StuckJobReaperReleasesHolds pins postgres/stuck_jobs.go's
// updated behaviour: when a stuck working job is auto-failed by the reaper
// the user's credit_held_precise must drop by that job's
// estimated_cost_precise. Pre-fix the reaper updated jobs.status to
// "failed" but ignored credit_held_precise, leaking holds forever on
// every worker crash / OOM kill / container eviction.
//
// We don't actually run the reaper goroutine here — we simulate its CTE
// statement directly to keep the test deterministic and DB-pinned.
func TestCreditHolds_StuckJobReaperReleasesHolds(t *testing.T) {
	db, userID := openOrSkip(t)
	defer cleanupUser(t, db, userID)

	seedUser(t, db, userID, "10.000000")
	svc := NewConcurrentLimitService(db)

	// Submit two jobs, mark both "working" (the state the reaper looks
	// for) and back-date their updated_at so they're "stuck". After
	// the reap simulation:
	//   - both jobs must be in status=failed
	//   - held must be 0 (both holds released)
	type jobSpec struct {
		id    string
		quote float64
	}
	jobs := []jobSpec{{quote: 2.0}, {quote: 3.5}}
	for i := range jobs {
		j := &models.Job{
			ID:     uuid.Must(uuid.NewV7()).String(),
			UserID: userID,
			Name:   "reaper-test",
			Status: models.StatusPending,
			Date:   time.Now().UTC(),
		}
		require.NoError(t, svc.CreateJobWithLimit(context.Background(), j, &JobLimitOpts{
			EstimatedCost: jobs[i].quote, EstimatedPlaces: 40,
		}))
		jobs[i].id = j.ID
		// Move to working + age it past the threshold.
		_, err := db.Exec(
			`UPDATE jobs SET status='working', updated_at=NOW() - INTERVAL '2 hours' WHERE id=$1`,
			j.ID,
		)
		require.NoError(t, err)
	}
	// 5.5 = 2.0 + 3.5
	require.Equal(t, "5.500000", readHold(t, db, userID))

	// Run the same CTE the reaper uses (postgres/stuck_jobs.go).
	ids := []string{jobs[0].id, jobs[1].id}
	_, err := db.Exec(
		`WITH reaped AS (
		     UPDATE jobs SET status='failed', failure_reason='test', updated_at=NOW()
		     WHERE id = ANY($1) AND status='working' AND deleted_at IS NULL
		     RETURNING id, user_id, COALESCE(estimated_cost_precise, 0) AS reserved
		 ),
		 per_user AS (
		     SELECT user_id, SUM(reserved) AS total_reserved FROM reaped
		     WHERE reserved > 0 GROUP BY user_id
		 ),
		 released AS (
		     UPDATE users u SET credit_held_precise = u.credit_held_precise - p.total_reserved
		     FROM per_user p WHERE u.id = p.user_id RETURNING u.id
		 )
		 SELECT id FROM reaped`,
		pqArrayStrings(ids),
	)
	require.NoError(t, err, "the reaper CTE must release holds atomically with marking jobs failed")

	require.Equal(t, "0.000000", readHold(t, db, userID),
		"both holds (2.0 + 3.5) must have been released by the reap")
}

// ─── helpers ─────────────────────────────────────────────────────────

func openOrSkip(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("DSN")
	}
	if dsn == "" {
		t.Skip("set TEST_DSN to run; need real Postgres to verify the credit-holds flow")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	t.Cleanup(func() { _ = db.Close() })
	return db, "user_holdtest_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func seedUser(t *testing.T, db *sql.DB, userID, balance string) {
	t.Helper()
	// Insert a minimal users row. NULL elsewhere is fine — the table
	// has plenty of optional columns. If the schema gains required
	// fields without defaults, this seed will need to grow.
	_, err := db.Exec(`
		INSERT INTO users (id, credit_balance, credit_held_precise)
		VALUES ($1, $2::numeric, 0)
		ON CONFLICT (id) DO UPDATE SET credit_balance = EXCLUDED.credit_balance, credit_held_precise = 0`,
		userID, balance,
	)
	require.NoError(t, err)
}

func cleanupUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	// Order matters: jobs FK references users.
	_, _ = db.Exec(`DELETE FROM jobs WHERE user_id = $1`, userID)
	_, _ = db.Exec(`DELETE FROM users WHERE id = $1`, userID)
}

func readHold(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	var s string
	err := db.QueryRow(`SELECT credit_held_precise::text FROM users WHERE id=$1`, userID).Scan(&s)
	require.NoError(t, err)
	return s
}

func readBalance(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	var s string
	err := db.QueryRow(`SELECT credit_balance::text FROM users WHERE id=$1`, userID).Scan(&s)
	require.NoError(t, err)
	return s
}

func readJobEstimate(t *testing.T, db *sql.DB, jobID string) string {
	t.Helper()
	var s string
	err := db.QueryRow(`SELECT estimated_cost_precise::text FROM jobs WHERE id=$1`, jobID).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("job %s not found — was it inserted within the same transaction?", jobID)
	}
	require.NoError(t, err)
	return s
}
