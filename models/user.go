package models

import (
	"context"
	"time"
)

// User represents a registered user in the system
type User struct {
	ID                 string
	Email              string
	SubscriptionPlanID string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// UserRepository manages user operations
type UserRepository interface {
	GetByID(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	Create(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
	UpdateUserSubscriptionPlan(ctx context.Context, userID, planID string) error
}

// UserUsage represents a user's usage of the system
type UserUsage struct {
	ID          int
	UserID      string
	JobCount    int
	LastJobDate time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UsageLimiter manages usage limits for users
type UsageLimiter interface {
	// CheckLimit verifies if a user has reached their usage limit
	CheckLimit(ctx context.Context, userID string) (bool, error)

	// IncrementUsage increases a user's usage count
	IncrementUsage(ctx context.Context, userID string) error

	// GetUsage retrieves a user's current usage
	GetUsage(ctx context.Context, userID string) (UserUsage, error)
}

// SubscriptionPlan represents a subscription plan
type SubscriptionPlan struct {
	ID            string
	Name          string
	StripePriceID string
	PriceCents    int
	Interval      string
	DailyJobLimit int
	Features      map[string]interface{}
	Active        bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UserSubscription represents a user's subscription
type UserSubscription struct {
	ID                   int
	UserID               string
	StripeCustomerID     string
	StripeSubscriptionID string
	PlanID               string
	Status               string
	CurrentPeriodStart   time.Time
	CurrentPeriodEnd     time.Time
	CancelAtPeriodEnd    bool
	ClientSecret         string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// WebhookEvent represents a processed webhook event
type WebhookEvent struct {
	ID            int
	StripeEventID string
	EventType     string
	ProcessedAt   time.Time
	Data          map[string]interface{}
}

// SubscriptionRepository manages subscription operations
type SubscriptionRepository interface {
	GetPlanByID(ctx context.Context, planID string) (SubscriptionPlan, error)
	GetPlanByStripeID(ctx context.Context, stripePriceID string) (SubscriptionPlan, error)
	GetUserSubscription(ctx context.Context, userID string) (UserSubscription, error)
	CreateUserSubscription(ctx context.Context, sub *UserSubscription) error
	UpdateUserSubscription(ctx context.Context, sub *UserSubscription) error
	UpdateSubscriptionStatus(ctx context.Context, stripeSubID, status string) error
	GetPlans(ctx context.Context) ([]SubscriptionPlan, error)
}

// WebhookRepository manages webhook event operations
type WebhookRepository interface {
	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	SaveEvent(ctx context.Context, event *WebhookEvent) error
}