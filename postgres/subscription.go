package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// SubscriptionPlan, UserSubscription, and WebhookEvent are aliases to models
type SubscriptionPlan = models.SubscriptionPlan
type UserSubscription = models.UserSubscription
type WebhookEvent = models.WebhookEvent
type SubscriptionRepository = models.SubscriptionRepository
type WebhookRepository = models.WebhookRepository

// subscriptionRepository implements SubscriptionRepository
type subscriptionRepository struct {
	db *sql.DB
}

// NewSubscriptionRepository creates a new SubscriptionRepository
func NewSubscriptionRepository(db *sql.DB) SubscriptionRepository {
	return &subscriptionRepository{db: db}
}

// GetPlanByID retrieves a subscription plan by ID
func (repo *subscriptionRepository) GetPlanByID(ctx context.Context, planID string) (SubscriptionPlan, error) {
	const q = `SELECT id, name, stripe_price_id, price_cents, interval, daily_job_limit, features, active, created_at, updated_at 
	           FROM subscription_plans WHERE id = $1`

	row := repo.db.QueryRowContext(ctx, q, planID)

	var plan SubscriptionPlan
	var featuresJSON []byte
	err := row.Scan(&plan.ID, &plan.Name, &plan.StripePriceID, &plan.PriceCents, &plan.Interval, 
		&plan.DailyJobLimit, &featuresJSON, &plan.Active, &plan.CreatedAt, &plan.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubscriptionPlan{}, errors.New("plan not found")
		}
		return SubscriptionPlan{}, err
	}

	// Parse features JSON
	if featuresJSON != nil {
		err = json.Unmarshal(featuresJSON, &plan.Features)
		if err != nil {
			return SubscriptionPlan{}, err
		}
	}

	return plan, nil
}

// GetPlanByStripeID retrieves a subscription plan by Stripe price ID
func (repo *subscriptionRepository) GetPlanByStripeID(ctx context.Context, stripePriceID string) (SubscriptionPlan, error) {
	const q = `SELECT id, name, stripe_price_id, price_cents, interval, daily_job_limit, features, active, created_at, updated_at 
	           FROM subscription_plans WHERE stripe_price_id = $1`

	row := repo.db.QueryRowContext(ctx, q, stripePriceID)

	var plan SubscriptionPlan
	var featuresJSON []byte
	err := row.Scan(&plan.ID, &plan.Name, &plan.StripePriceID, &plan.PriceCents, &plan.Interval, 
		&plan.DailyJobLimit, &featuresJSON, &plan.Active, &plan.CreatedAt, &plan.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubscriptionPlan{}, errors.New("plan not found")
		}
		return SubscriptionPlan{}, err
	}

	// Parse features JSON
	if featuresJSON != nil {
		err = json.Unmarshal(featuresJSON, &plan.Features)
		if err != nil {
			return SubscriptionPlan{}, err
		}
	}

	return plan, nil
}

// GetUserSubscription retrieves a user's subscription
func (repo *subscriptionRepository) GetUserSubscription(ctx context.Context, userID string) (UserSubscription, error) {
	const q = `SELECT id, user_id, stripe_customer_id, COALESCE(stripe_subscription_id, ''), plan_id, status, 
	           COALESCE(current_period_start, NOW()), COALESCE(current_period_end, NOW()), 
	           cancel_at_period_end, COALESCE(client_secret, ''), created_at, updated_at 
	           FROM user_subscriptions WHERE user_id = $1`

	row := repo.db.QueryRowContext(ctx, q, userID)

	var sub UserSubscription
	err := row.Scan(&sub.ID, &sub.UserID, &sub.StripeCustomerID, &sub.StripeSubscriptionID, &sub.PlanID, 
		&sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CancelAtPeriodEnd, &sub.ClientSecret,
		&sub.CreatedAt, &sub.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserSubscription{}, errors.New("subscription not found")
		}
		return UserSubscription{}, err
	}

	return sub, nil
}

// CreateUserSubscription creates a new user subscription
func (repo *subscriptionRepository) CreateUserSubscription(ctx context.Context, sub *UserSubscription) error {
	const q = `INSERT INTO user_subscriptions (user_id, stripe_customer_id, stripe_subscription_id, plan_id, status, 
	           current_period_start, current_period_end, cancel_at_period_end, client_secret, created_at, updated_at) 
	           VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`

	now := time.Now().UTC()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	if sub.UpdatedAt.IsZero() {
		sub.UpdatedAt = now
	}

	err := repo.db.QueryRowContext(ctx, q, sub.UserID, sub.StripeCustomerID, sub.StripeSubscriptionID, 
		sub.PlanID, sub.Status, sub.CurrentPeriodStart, sub.CurrentPeriodEnd, sub.CancelAtPeriodEnd, 
		sub.ClientSecret, sub.CreatedAt, sub.UpdatedAt).Scan(&sub.ID)
	return err
}

// UpdateUserSubscription updates an existing user subscription
func (repo *subscriptionRepository) UpdateUserSubscription(ctx context.Context, sub *UserSubscription) error {
	const q = `UPDATE user_subscriptions 
	           SET stripe_subscription_id = $2, plan_id = $3, status = $4, current_period_start = $5, 
	               current_period_end = $6, cancel_at_period_end = $7, client_secret = $8, updated_at = $9 
	           WHERE user_id = $1`

	sub.UpdatedAt = time.Now().UTC()

	_, err := repo.db.ExecContext(ctx, q, sub.UserID, sub.StripeSubscriptionID, sub.PlanID, sub.Status, 
		sub.CurrentPeriodStart, sub.CurrentPeriodEnd, sub.CancelAtPeriodEnd, sub.ClientSecret, sub.UpdatedAt)
	return err
}

// UpdateSubscriptionStatus updates subscription status by Stripe subscription ID
func (repo *subscriptionRepository) UpdateSubscriptionStatus(ctx context.Context, stripeSubID, status string) error {
	const q = `UPDATE user_subscriptions SET status = $2, updated_at = $3 WHERE stripe_subscription_id = $1`

	_, err := repo.db.ExecContext(ctx, q, stripeSubID, status, time.Now().UTC())
	return err
}

// GetPlans retrieves all active subscription plans
func (repo *subscriptionRepository) GetPlans(ctx context.Context) ([]SubscriptionPlan, error) {
	const q = `SELECT id, name, stripe_price_id, price_cents, interval, daily_job_limit, features, active, created_at, updated_at 
	           FROM subscription_plans WHERE active = true ORDER BY price_cents ASC`

	rows, err := repo.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []SubscriptionPlan
	for rows.Next() {
		var plan SubscriptionPlan
		var featuresJSON []byte
		err := rows.Scan(&plan.ID, &plan.Name, &plan.StripePriceID, &plan.PriceCents, &plan.Interval, 
			&plan.DailyJobLimit, &featuresJSON, &plan.Active, &plan.CreatedAt, &plan.UpdatedAt)
		if err != nil {
			return nil, err
		}

		// Parse features JSON
		if featuresJSON != nil {
			err = json.Unmarshal(featuresJSON, &plan.Features)
			if err != nil {
				return nil, err
			}
		}

		plans = append(plans, plan)
	}

	return plans, rows.Err()
}

// webhookRepository implements WebhookRepository
type webhookRepository struct {
	db *sql.DB
}

// NewWebhookRepository creates a new WebhookRepository
func NewWebhookRepository(db *sql.DB) WebhookRepository {
	return &webhookRepository{db: db}
}

// IsEventProcessed checks if a webhook event has been processed
func (repo *webhookRepository) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	const q = `SELECT COUNT(*) FROM webhook_events WHERE stripe_event_id = $1`

	var count int
	err := repo.db.QueryRowContext(ctx, q, eventID).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// SaveEvent saves a processed webhook event
func (repo *webhookRepository) SaveEvent(ctx context.Context, event *WebhookEvent) error {
	const q = `INSERT INTO webhook_events (stripe_event_id, event_type, processed_at, data) 
	           VALUES ($1, $2, $3, $4) ON CONFLICT (stripe_event_id) DO NOTHING`

	dataJSON, err := json.Marshal(event.Data)
	if err != nil {
		return err
	}

	if event.ProcessedAt.IsZero() {
		event.ProcessedAt = time.Now().UTC()
	}

	_, err = repo.db.ExecContext(ctx, q, event.StripeEventID, event.EventType, event.ProcessedAt, dataJSON)
	return err
}