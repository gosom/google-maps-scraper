//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		dsn = "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("failed to connect to test DB: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("failed to ping test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedUser inserts a minimal user row and schedules cleanup. Returns the user ID.
func seedUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	userID := "user_whtest_" + uuid.New().String()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, credit_balance, created_at, updated_at)
		 VALUES ($1, $2, 0, NOW(), NOW())`,
		userID, userID+"@test.invalid",
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return userID
}

// seedWebhookConfig inserts a webhook_configs row and schedules cleanup.
func seedWebhookConfig(t *testing.T, db *sql.DB, userID string, resolvedIP *string) string {
	t.Helper()
	ctx := context.Background()
	cfgID := uuid.New().String()
	_, err := db.ExecContext(ctx,
		`INSERT INTO webhook_configs
			(id, user_id, name, url, encrypted_secret, resolved_ip, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		cfgID, userID, "test-wh-"+cfgID[:8], "https://example.com/wh", "enc_secret_test", resolvedIP,
	)
	if err != nil {
		t.Fatalf("seed webhook config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM webhook_configs WHERE id = $1`, cfgID)
	})
	return cfgID
}

// seedJob inserts a minimal jobs row and schedules cleanup. Returns the job ID.
func seedJob(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	ctx := context.Background()
	jobID := uuid.New().String()
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (id, name, status, data, created_at, updated_at, user_id, source)
		 VALUES ($1, $2, $3, $4, NOW(), NOW(), $5, 'web')`,
		jobID, "test-job-"+jobID[:8], "pending", `{"keywords":["test"]}`, userID,
	)
	if err != nil {
		t.Fatalf("seed job: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM jobs WHERE id = $1`, jobID)
	})
	return jobID
}

// seedDelivery inserts a single delivery row via raw SQL so tests can control
// every field. Schedules cleanup.
func seedDelivery(t *testing.T, db *sql.DB, jobID, cfgID, status string, nextRetryAt *time.Time, lastAttemptAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO job_webhook_deliveries
			(job_id, webhook_config_id, status, next_retry_at, last_attempt_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		jobID, cfgID, status, nextRetryAt, lastAttemptAt,
	)
	if err != nil {
		t.Fatalf("seed delivery: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx,
			`DELETE FROM job_webhook_deliveries WHERE job_id = $1 AND webhook_config_id = $2`,
			jobID, cfgID,
		)
	})
}

// countDeliveriesByStatus counts rows matching (job_id, status).
func countDeliveriesByStatus(t *testing.T, db *sql.DB, jobID, status string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM job_webhook_deliveries WHERE job_id = $1 AND status = $2`,
		jobID, status,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

// countDeliveriesByJobID counts all delivery rows for a job.
func countDeliveriesByJobID(t *testing.T, db *sql.DB, jobID string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM job_webhook_deliveries WHERE job_id = $1`,
		jobID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count deliveries by job: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestWebhookDelivery_ListPendingGlobal_ClaimsAndMarksDelivering(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)

	jobIDs := make([]string, 3)
	for i := range jobIDs {
		jobIDs[i] = seedJob(t, db, userID)
		seedDelivery(t, db, jobIDs[i], cfgID, models.DeliveryStatusPending, nil, nil)
	}

	repo := NewJobWebhookDeliveryRepository(db)
	claimed, err := repo.ListPendingGlobal(ctx, 2)
	if err != nil {
		t.Fatalf("ListPendingGlobal: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("expected 2 claimed, got %d", len(claimed))
	}

	for _, d := range claimed {
		if d.Status != models.DeliveryStatusDelivering {
			t.Errorf("claimed delivery status = %q, want %q", d.Status, models.DeliveryStatusDelivering)
		}
		if d.Attempts != 1 {
			t.Errorf("claimed delivery attempts = %d, want 1", d.Attempts)
		}
	}

	// Exactly 1 row should still be pending.
	pendingTotal := 0
	for _, jid := range jobIDs {
		pendingTotal += countDeliveriesByStatus(t, db, jid, models.DeliveryStatusPending)
	}
	if pendingTotal != 1 {
		t.Errorf("expected 1 remaining pending delivery, got %d", pendingTotal)
	}
}

func TestWebhookDelivery_ListPendingGlobal_SkipsNonPending(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	cfgID1 := seedWebhookConfig(t, db, userID, nil)
	cfgID2 := seedWebhookConfig(t, db, userID, nil)
	cfgID3 := seedWebhookConfig(t, db, userID, nil)

	jobID := seedJob(t, db, userID)
	seedDelivery(t, db, jobID, cfgID1, models.DeliveryStatusDelivering, nil, nil)
	seedDelivery(t, db, jobID, cfgID2, models.DeliveryStatusDelivered, nil, nil)
	seedDelivery(t, db, jobID, cfgID3, models.DeliveryStatusFailed, nil, nil)

	repo := NewJobWebhookDeliveryRepository(db)
	claimed, err := repo.ListPendingGlobal(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingGlobal: %v", err)
	}

	// Filter to only our test job in case other pending rows exist in the DB.
	var ours []*models.JobWebhookDelivery
	cfgSet := map[string]bool{cfgID1: true, cfgID2: true, cfgID3: true}
	for _, d := range claimed {
		if d.JobID == jobID && cfgSet[d.WebhookConfigID] {
			ours = append(ours, d)
		}
	}
	if len(ours) != 0 {
		t.Errorf("expected 0 claimed for our non-pending rows, got %d", len(ours))
	}
}

func TestWebhookDelivery_ListPendingGlobal_RespectsNextRetryAt(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)
	jobID := seedJob(t, db, userID)

	future := time.Now().UTC().Add(1 * time.Hour)
	seedDelivery(t, db, jobID, cfgID, models.DeliveryStatusPending, &future, nil)

	repo := NewJobWebhookDeliveryRepository(db)

	// Should not be claimed — next_retry_at is in the future.
	claimed, err := repo.ListPendingGlobal(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingGlobal (future): %v", err)
	}
	for _, d := range claimed {
		if d.JobID == jobID && d.WebhookConfigID == cfgID {
			t.Fatal("delivery with future next_retry_at should not be claimed")
		}
	}

	// Update next_retry_at to the past.
	past := time.Now().UTC().Add(-1 * time.Hour)
	_, err = db.ExecContext(ctx,
		`UPDATE job_webhook_deliveries SET next_retry_at = $1 WHERE job_id = $2 AND webhook_config_id = $3`,
		past, jobID, cfgID,
	)
	if err != nil {
		t.Fatalf("update next_retry_at: %v", err)
	}

	claimed, err = repo.ListPendingGlobal(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingGlobal (past): %v", err)
	}
	var found bool
	for _, d := range claimed {
		if d.JobID == jobID && d.WebhookConfigID == cfgID {
			found = true
			break
		}
	}
	if !found {
		t.Error("delivery with past next_retry_at should have been claimed")
	}
}

func TestWebhookDelivery_MarkDelivered_RequiresDeliveringStatus(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)
	jobID := seedJob(t, db, userID)

	seedDelivery(t, db, jobID, cfgID, models.DeliveryStatusPending, nil, nil)

	repo := NewJobWebhookDeliveryRepository(db)

	// Should fail: row is pending, not delivering.
	err := repo.MarkDelivered(ctx, jobID, cfgID)
	if err != models.ErrDeliveryNotFound {
		t.Fatalf("MarkDelivered on pending row: expected ErrDeliveryNotFound, got %v", err)
	}

	// Move to delivering.
	_, err = db.ExecContext(ctx,
		`UPDATE job_webhook_deliveries SET status = 'delivering' WHERE job_id = $1 AND webhook_config_id = $2`,
		jobID, cfgID,
	)
	if err != nil {
		t.Fatalf("update to delivering: %v", err)
	}

	if err := repo.MarkDelivered(ctx, jobID, cfgID); err != nil {
		t.Fatalf("MarkDelivered on delivering row: %v", err)
	}

	// Verify delivered_at is set.
	var deliveredAt sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT delivered_at FROM job_webhook_deliveries WHERE job_id = $1 AND webhook_config_id = $2`,
		jobID, cfgID,
	).Scan(&deliveredAt)
	if err != nil {
		t.Fatalf("verify delivered_at: %v", err)
	}
	if !deliveredAt.Valid {
		t.Error("delivered_at should be non-NULL after MarkDelivered")
	}
}

func TestWebhookDelivery_MarkFailed_RequiresDeliveringStatus(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)
	jobID := seedJob(t, db, userID)

	seedDelivery(t, db, jobID, cfgID, models.DeliveryStatusPending, nil, nil)

	repo := NewJobWebhookDeliveryRepository(db)

	// Should fail: row is pending, not delivering.
	err := repo.MarkFailed(ctx, jobID, cfgID)
	if err != models.ErrDeliveryNotFound {
		t.Fatalf("MarkFailed on pending row: expected ErrDeliveryNotFound, got %v", err)
	}

	// Move to delivering.
	_, err = db.ExecContext(ctx,
		`UPDATE job_webhook_deliveries SET status = 'delivering' WHERE job_id = $1 AND webhook_config_id = $2`,
		jobID, cfgID,
	)
	if err != nil {
		t.Fatalf("update to delivering: %v", err)
	}

	if err := repo.MarkFailed(ctx, jobID, cfgID); err != nil {
		t.Fatalf("MarkFailed on delivering row: %v", err)
	}

	// Verify status is now 'failed'.
	var status string
	err = db.QueryRowContext(ctx,
		`SELECT status FROM job_webhook_deliveries WHERE job_id = $1 AND webhook_config_id = $2`,
		jobID, cfgID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("verify status: %v", err)
	}
	if status != models.DeliveryStatusFailed {
		t.Errorf("status = %q, want %q", status, models.DeliveryStatusFailed)
	}
}

func TestWebhookDelivery_SetNextRetry_RequiresDeliveringStatus(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)
	jobID := seedJob(t, db, userID)

	seedDelivery(t, db, jobID, cfgID, models.DeliveryStatusPending, nil, nil)

	repo := NewJobWebhookDeliveryRepository(db)
	retryAt := time.Now().UTC().Add(30 * time.Minute)

	// Should fail: row is pending, not delivering.
	err := repo.SetNextRetry(ctx, jobID, cfgID, retryAt)
	if err != models.ErrDeliveryNotFound {
		t.Fatalf("SetNextRetry on pending row: expected ErrDeliveryNotFound, got %v", err)
	}

	// Move to delivering.
	_, err = db.ExecContext(ctx,
		`UPDATE job_webhook_deliveries SET status = 'delivering' WHERE job_id = $1 AND webhook_config_id = $2`,
		jobID, cfgID,
	)
	if err != nil {
		t.Fatalf("update to delivering: %v", err)
	}

	if err := repo.SetNextRetry(ctx, jobID, cfgID, retryAt); err != nil {
		t.Fatalf("SetNextRetry on delivering row: %v", err)
	}

	// Verify: status back to pending, next_retry_at set.
	var status string
	var nextRetry sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT status, next_retry_at FROM job_webhook_deliveries WHERE job_id = $1 AND webhook_config_id = $2`,
		jobID, cfgID,
	).Scan(&status, &nextRetry)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if status != models.DeliveryStatusPending {
		t.Errorf("status = %q, want %q", status, models.DeliveryStatusPending)
	}
	if !nextRetry.Valid {
		t.Fatal("next_retry_at should be non-NULL after SetNextRetry")
	}
	// Allow 2s clock skew.
	if diff := nextRetry.Time.Sub(retryAt).Abs(); diff > 2*time.Second {
		t.Errorf("next_retry_at drift = %v (want within 2s)", diff)
	}
}

func TestWebhookDelivery_CreateBatch_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	jobID := seedJob(t, db, userID)

	cfgIDs := make([]string, 3)
	for i := range cfgIDs {
		cfgIDs[i] = seedWebhookConfig(t, db, userID, nil)
	}

	// Build batch.
	batch := make([]*models.JobWebhookDelivery, len(cfgIDs))
	for i, cid := range cfgIDs {
		batch[i] = &models.JobWebhookDelivery{
			JobID:           jobID,
			WebhookConfigID: cid,
		}
	}

	// Schedule cleanup for the deliveries created by CreateBatch.
	t.Cleanup(func() {
		for _, cid := range cfgIDs {
			_, _ = db.ExecContext(ctx,
				`DELETE FROM job_webhook_deliveries WHERE job_id = $1 AND webhook_config_id = $2`,
				jobID, cid,
			)
		}
	})

	repo := NewJobWebhookDeliveryRepository(db)

	// First insert.
	if err := repo.CreateBatch(ctx, batch); err != nil {
		t.Fatalf("CreateBatch (first): %v", err)
	}
	if n := countDeliveriesByJobID(t, db, jobID); n != 3 {
		t.Fatalf("expected 3 rows after first batch, got %d", n)
	}

	// Second (idempotent) insert — should be a no-op.
	if err := repo.CreateBatch(ctx, batch); err != nil {
		t.Fatalf("CreateBatch (second): %v", err)
	}
	if n := countDeliveriesByJobID(t, db, jobID); n != 3 {
		t.Fatalf("expected 3 rows after idempotent batch, got %d", n)
	}
}

func TestWebhookDelivery_CountRecentByUserID(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)

	now := time.Now().UTC()
	recent := now.Add(-30 * time.Minute)
	old := now.Add(-2 * time.Hour)

	jobRecent := seedJob(t, db, userID)
	seedDelivery(t, db, jobRecent, cfgID, models.DeliveryStatusDelivered, nil, &recent)

	jobOld := seedJob(t, db, userID)
	seedDelivery(t, db, jobOld, cfgID, models.DeliveryStatusDelivered, nil, &old)

	repo := NewJobWebhookDeliveryRepository(db)
	since := now.Add(-1 * time.Hour)
	count, err := repo.CountRecentByUserID(ctx, userID, since)
	if err != nil {
		t.Fatalf("CountRecentByUserID: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count=1 (only the recent delivery), got %d", count)
	}
}

func TestWebhookDelivery_CountRecentByIP(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, db)

	ip := fmt.Sprintf("10.0.0.%d", time.Now().UnixNano()%250+1) // unique-ish test IP
	cfgID := seedWebhookConfig(t, db, userID, &ip)

	now := time.Now().UTC()
	recent := now.Add(-30 * time.Minute)
	old := now.Add(-2 * time.Hour)

	jobRecent := seedJob(t, db, userID)
	seedDelivery(t, db, jobRecent, cfgID, models.DeliveryStatusDelivered, nil, &recent)

	jobOld := seedJob(t, db, userID)
	seedDelivery(t, db, jobOld, cfgID, models.DeliveryStatusDelivered, nil, &old)

	repo := NewJobWebhookDeliveryRepository(db)
	since := now.Add(-1 * time.Hour)
	count, err := repo.CountRecentByIP(ctx, ip, since)
	if err != nil {
		t.Fatalf("CountRecentByIP: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count=1, got %d", count)
	}
}
