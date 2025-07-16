package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// User is now an alias to the models.User struct
type User = models.User

// UserUsage is now an alias to the models.UserUsage struct
type UserUsage = models.UserUsage

// UserRepository is now an alias to the models.UserRepository interface
type UserRepository = models.UserRepository

// UsageLimiter is now an alias to the models.UsageLimiter interface
type UsageLimiter = models.UsageLimiter

// userRepository implements the UserRepository interface
type userRepository struct {
	db *sql.DB
}

// NewUserRepository creates a new UserRepository
func NewUserRepository(db *sql.DB) UserRepository {
	return &userRepository{db: db}
}

// GetByID retrieves a user by ID
func (repo *userRepository) GetByID(ctx context.Context, id string) (User, error) {
	const q = `SELECT id, email, COALESCE(subscription_plan_id, 'free'), created_at, updated_at FROM users WHERE id = $1`

	row := repo.db.QueryRowContext(ctx, q, id)

	var user User
	err := row.Scan(&user.ID, &user.Email, &user.SubscriptionPlanID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, errors.New("user not found")
		}
		return User{}, err
	}

	return user, nil
}

// GetByEmail retrieves a user by email
func (repo *userRepository) GetByEmail(ctx context.Context, email string) (User, error) {
	const q = `SELECT id, email, COALESCE(subscription_plan_id, 'free'), created_at, updated_at FROM users WHERE email = $1`

	row := repo.db.QueryRowContext(ctx, q, email)

	var user User
	err := row.Scan(&user.ID, &user.Email, &user.SubscriptionPlanID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, errors.New("user not found")
		}
		return User{}, err
	}

	return user, nil
}

// Create inserts a new user
func (repo *userRepository) Create(ctx context.Context, user *User) error {
	const q = `INSERT INTO users (id, email, subscription_plan_id, created_at, updated_at) 
	           VALUES ($1, $2, $3, $4, $5)`

	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}
	if user.SubscriptionPlanID == "" {
		user.SubscriptionPlanID = "free"
	}

	_, err := repo.db.ExecContext(ctx, q, user.ID, user.Email, user.SubscriptionPlanID, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		return err
	}

	// Initialize usage entry for the new user
	const usageQ = `INSERT INTO user_usage (user_id, created_at, updated_at) 
	                VALUES ($1, $2, $3)`

	_, err = repo.db.ExecContext(ctx, usageQ, user.ID, now, now)
	return err
}

// Delete removes a user
func (repo *userRepository) Delete(ctx context.Context, id string) error {
	// First delete usage records
	const usageQ = `DELETE FROM user_usage WHERE user_id = $1`
	_, err := repo.db.ExecContext(ctx, usageQ, id)
	if err != nil {
		return err
	}

	// Then delete user record
	const q = `DELETE FROM users WHERE id = $1`
	_, err = repo.db.ExecContext(ctx, q, id)
	return err
}

// usageLimiter implements the UsageLimiter interface
type usageLimiter struct {
	db            *sql.DB
	dailyJobLimit int
	subRepo       models.SubscriptionRepository
}

// NewUsageLimiter creates a new UsageLimiter
func NewUsageLimiter(db *sql.DB, dailyJobLimit int) UsageLimiter {
	return &usageLimiter{
		db:            db,
		dailyJobLimit: dailyJobLimit,
		subRepo:       NewSubscriptionRepository(db),
	}
}

// CheckLimit verifies if a user has reached their daily job limit
func (l *usageLimiter) CheckLimit(ctx context.Context, userID string) (bool, error) {
	usage, err := l.GetUsage(ctx, userID)
	if err != nil {
		return false, err
	}

	// Get user's subscription to determine their limit
	userSub, err := l.subRepo.GetUserSubscription(ctx, userID)
	var dailyLimit int
	if err != nil {
		// If no subscription found, use default limit (free plan)
		plan, err := l.subRepo.GetPlanByID(ctx, "free")
		if err != nil {
			dailyLimit = l.dailyJobLimit
		} else {
			dailyLimit = plan.DailyJobLimit
		}
	} else {
		// Get plan limits
		plan, err := l.subRepo.GetPlanByID(ctx, userSub.PlanID)
		if err != nil {
			dailyLimit = l.dailyJobLimit
		} else {
			dailyLimit = plan.DailyJobLimit
		}
	}

	// Check if last job was today
	today := time.Now().UTC().Truncate(24 * time.Hour)
	lastJobDate := usage.LastJobDate.UTC().Truncate(24 * time.Hour)

	// If last job was from a different day, user is under limit
	if !lastJobDate.Equal(today) {
		return true, nil
	}

	// Otherwise, check against the daily limit
	return usage.JobCount < dailyLimit, nil
}

// IncrementUsage increases a user's usage count
func (l *usageLimiter) IncrementUsage(ctx context.Context, userID string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get current usage
	var usage UserUsage
	const getQ = `SELECT id, user_id, job_count, last_job_date, created_at, updated_at 
	              FROM user_usage WHERE user_id = $1 FOR UPDATE`

	row := tx.QueryRowContext(ctx, getQ, userID)
	err = row.Scan(&usage.ID, &usage.UserID, &usage.JobCount, &usage.LastJobDate,
		&usage.CreatedAt, &usage.UpdatedAt)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	lastJobDate := usage.LastJobDate.UTC().Truncate(24 * time.Hour)

	// Reset count if it's a new day
	if !lastJobDate.Equal(today) {
		usage.JobCount = 1
	} else {
		usage.JobCount++
	}

	// Update usage record
	const updateQ = `UPDATE user_usage 
	                 SET job_count = $1, last_job_date = $2, updated_at = $3 
	                 WHERE id = $4`

	_, err = tx.ExecContext(ctx, updateQ, usage.JobCount, now, now, usage.ID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetUsage retrieves a user's current usage
func (l *usageLimiter) GetUsage(ctx context.Context, userID string) (UserUsage, error) {
	const q = `SELECT id, user_id, job_count, COALESCE(last_job_date, created_at), created_at, updated_at 
	           FROM user_usage WHERE user_id = $1`

	row := l.db.QueryRowContext(ctx, q, userID)

	var usage UserUsage
	err := row.Scan(&usage.ID, &usage.UserID, &usage.JobCount, &usage.LastJobDate,
		&usage.CreatedAt, &usage.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Create a new usage record if none exists
			now := time.Now().UTC()
			const insertQ = `INSERT INTO user_usage (user_id, job_count, created_at, updated_at) 
			                 VALUES ($1, 0, $2, $3) RETURNING id`

			err = l.db.QueryRowContext(ctx, insertQ, userID, now, now).Scan(&usage.ID)
			if err != nil {
				return UserUsage{}, err
			}

			usage.UserID = userID
			usage.JobCount = 0
			usage.LastJobDate = now
			usage.CreatedAt = now
			usage.UpdatedAt = now
		} else {
			return UserUsage{}, err
		}
	}

	return usage, nil
}

// UpdateUserSubscriptionPlan updates a user's subscription plan ID
func (repo *userRepository) UpdateUserSubscriptionPlan(ctx context.Context, userID, planID string) error {
	const q = `UPDATE users SET subscription_plan_id = $1, updated_at = $2 WHERE id = $3`
	
	_, err := repo.db.ExecContext(ctx, q, planID, time.Now().UTC(), userID)
	return err
}