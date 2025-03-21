package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestUserRepository(t *testing.T) {
	// Skip if no PostgreSQL connection is available
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("Skipping PostgreSQL user repository test: PG_TEST_DSN not set")
	}

	// Connect to PostgreSQL
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	// Ensure database schema exists
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Create user repository
	userRepo := NewUserRepository(db)

	// Create a test user
	ctx := context.Background()
	user := createTestUser(t)

	// Test Create
	t.Run("Create", func(t *testing.T) {
		err := userRepo.Create(ctx, &user)
		if err != nil {
			t.Fatalf("Failed to create user: %v", err)
		}
	})

	// Test GetByID
	t.Run("GetByID", func(t *testing.T) {
		fetchedUser, err := userRepo.GetByID(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to get user by ID: %v", err)
		}

		if fetchedUser.ID != user.ID {
			t.Errorf("Expected user ID %s, got %s", user.ID, fetchedUser.ID)
		}

		if fetchedUser.Email != user.Email {
			t.Errorf("Expected user email %s, got %s", user.Email, fetchedUser.Email)
		}
	})

	// Test GetByEmail
	t.Run("GetByEmail", func(t *testing.T) {
		fetchedUser, err := userRepo.GetByEmail(ctx, user.Email)
		if err != nil {
			t.Fatalf("Failed to get user by email: %v", err)
		}

		if fetchedUser.ID != user.ID {
			t.Errorf("Expected user ID %s, got %s", user.ID, fetchedUser.ID)
		}
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		err := userRepo.Delete(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to delete user: %v", err)
		}

		// Verify deletion
		_, err = userRepo.GetByID(ctx, user.ID)
		if err == nil {
			t.Errorf("Expected error when getting deleted user")
		}
	})
}

func TestUsageLimiter(t *testing.T) {
	// Skip if no PostgreSQL connection is available
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("Skipping PostgreSQL usage limiter test: PG_TEST_DSN not set")
	}

	// Connect to PostgreSQL
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	// Ensure database schema exists
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Create user repository and usage limiter
	userRepo := NewUserRepository(db)
	limiter := NewUsageLimiter(db, 5) // 5 jobs per day limit

	// Create a test user
	ctx := context.Background()
	user := createTestUser(t)

	// Create the user
	if err := userRepo.Create(ctx, &user); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	defer userRepo.Delete(ctx, user.ID)

	// Test GetUsage
	t.Run("GetUsage", func(t *testing.T) {
		usage, err := limiter.GetUsage(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to get usage: %v", err)
		}

		if usage.UserID != user.ID {
			t.Errorf("Expected usage for user ID %s, got %s", user.ID, usage.UserID)
		}

		if usage.JobCount != 0 {
			t.Errorf("Expected initial job count 0, got %d", usage.JobCount)
		}
	})

	// Test IncrementUsage
	t.Run("IncrementUsage", func(t *testing.T) {
		err := limiter.IncrementUsage(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to increment usage: %v", err)
		}

		usage, err := limiter.GetUsage(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to get usage after increment: %v", err)
		}

		if usage.JobCount != 1 {
			t.Errorf("Expected job count 1 after increment, got %d", usage.JobCount)
		}
	})

	// Test CheckLimit
	t.Run("CheckLimit", func(t *testing.T) {
		// Should be under limit with 1 job
		allowed, err := limiter.CheckLimit(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to check limit: %v", err)
		}

		if !allowed {
			t.Errorf("Expected user to be under limit with 1 job")
		}

		// Increment to reach limit (5 jobs)
		for i := 0; i < 4; i++ {
			err := limiter.IncrementUsage(ctx, user.ID)
			if err != nil {
				t.Fatalf("Failed to increment usage: %v", err)
			}
		}

		// Should be at limit (5 jobs)
		allowed, err = limiter.CheckLimit(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to check limit: %v", err)
		}

		if !allowed {
			t.Errorf("Expected user to be under limit with 5 jobs")
		}

		// Increment once more to exceed limit
		err = limiter.IncrementUsage(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to increment usage: %v", err)
		}

		// Should be over limit (6 jobs)
		allowed, err = limiter.CheckLimit(ctx, user.ID)
		if err != nil {
			t.Fatalf("Failed to check limit: %v", err)
		}

		if allowed {
			t.Errorf("Expected user to be over limit with 6 jobs")
		}
	})
}

func createTestUser(t *testing.T) User {
	userID := uuid.New().String()
	email := userID + "@example.com"

	return User{
		ID:        userID,
		Email:     email,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}