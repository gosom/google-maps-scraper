package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRepository(t *testing.T) {
	// Skip if no PostgreSQL connection is available
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("Skipping PostgreSQL repository test: PG_TEST_DSN not set")
	}

	// Connect to PostgreSQL
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	// Create repository
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Create a test job
	ctx := context.Background()
	job := createTestJob(t)

	// Test Create
	t.Run("Create", func(t *testing.T) {
		err := repo.Create(ctx, &job)
		if err != nil {
			t.Fatalf("Failed to create job: %v", err)
		}
	})

	// Test Get
	t.Run("Get", func(t *testing.T) {
		fetchedJob, err := repo.Get(ctx, job.ID)
		if err != nil {
			t.Fatalf("Failed to get job: %v", err)
		}

		if fetchedJob.ID != job.ID {
			t.Errorf("Expected job ID %s, got %s", job.ID, fetchedJob.ID)
		}

		if fetchedJob.Name != job.Name {
			t.Errorf("Expected job name %s, got %s", job.Name, fetchedJob.Name)
		}

		if fetchedJob.Status != job.Status {
			t.Errorf("Expected job status %s, got %s", job.Status, fetchedJob.Status)
		}
	})

	// Test Select
	t.Run("Select", func(t *testing.T) {
		// By status
		jobs, err := repo.Select(ctx, models.SelectParams{Status: job.Status})
		if err != nil {
			t.Fatalf("Failed to select jobs by status: %v", err)
		}

		if len(jobs) == 0 {
			t.Errorf("Expected at least one job with status %s", job.Status)
		}

		// By user ID
		jobs, err = repo.Select(ctx, models.SelectParams{UserID: job.UserID})
		if err != nil {
			t.Fatalf("Failed to select jobs by user ID: %v", err)
		}

		if len(jobs) == 0 {
			t.Errorf("Expected at least one job with user ID %s", job.UserID)
		}

		foundJob := false
		for _, j := range jobs {
			if j.ID == job.ID {
				foundJob = true
				break
			}
		}

		if !foundJob {
			t.Errorf("Expected to find job %s in results", job.ID)
		}
	})

	// Test Update
	t.Run("Update", func(t *testing.T) {
		// Update job status
		job.Status = models.StatusWorking
		job.Name = "Updated Test Job"

		err := repo.Update(ctx, &job)
		if err != nil {
			t.Fatalf("Failed to update job: %v", err)
		}

		// Verify update
		updatedJob, err := repo.Get(ctx, job.ID)
		if err != nil {
			t.Fatalf("Failed to get updated job: %v", err)
		}

		if updatedJob.Status != models.StatusWorking {
			t.Errorf("Expected job status %s, got %s", models.StatusWorking, updatedJob.Status)
		}

		if updatedJob.Name != "Updated Test Job" {
			t.Errorf("Expected job name %s, got %s", "Updated Test Job", updatedJob.Name)
		}
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		err := repo.Delete(ctx, job.ID)
		if err != nil {
			t.Fatalf("Failed to delete job: %v", err)
		}

		// Verify deletion
		_, err = repo.Get(ctx, job.ID)
		if err == nil {
			t.Errorf("Expected error when getting deleted job")
		}
	})
}

func createTestJob(t *testing.T) models.Job {
	jobID := uuid.New().String()
	userID := uuid.New().String()

	return models.Job{
		ID:     jobID,
		UserID: userID,
		Name:   "Test Job",
		Date:   time.Now().UTC(),
		Status: models.StatusPending,
		Data: models.JobData{
			Keywords: []string{"coffee", "shop"},
			Lang:     "en",
			Zoom:     15,
			Lat:      "40.712776",
			Lon:      "-74.005974",
			FastMode: true,
			Depth:    1,
			MaxTime:  time.Minute * 5,
		},
	}
}
