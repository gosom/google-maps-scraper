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

// UserRepository is now an alias to the models.UserRepository interface
type UserRepository = models.UserRepository

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
	const q = `SELECT id, email, created_at, updated_at FROM users WHERE id = $1`

	row := repo.db.QueryRowContext(ctx, q, id)

	var user User
	err := row.Scan(&user.ID, &user.Email, &user.CreatedAt, &user.UpdatedAt)
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
	const q = `SELECT id, email, created_at, updated_at FROM users WHERE email = $1`

	row := repo.db.QueryRowContext(ctx, q, email)

	var user User
	err := row.Scan(&user.ID, &user.Email, &user.CreatedAt, &user.UpdatedAt)
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
	const q = `INSERT INTO users (id, email, created_at, updated_at) 
	           VALUES ($1, $2, $3, $4)`

	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}

	_, err := repo.db.ExecContext(ctx, q, user.ID, user.Email, user.CreatedAt, user.UpdatedAt)
	return err
}

// Delete removes a user
func (repo *userRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id = $1`
	_, err := repo.db.ExecContext(ctx, q, id)
	return err
}
