package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	stripeClient "github.com/gosom/google-maps-scraper/stripe"
	"github.com/stripe/stripe-go/v81"
)

// ServiceInterface defines the subscription service interface
type ServiceInterface interface {
	CreateSubscription(ctx context.Context, userID, planID string) (*models.UserSubscription, error)
	GetUserSubscription(ctx context.Context, userID string) (*SubscriptionWithPlan, error)
	GetUserSubscriptionStatus(ctx context.Context, userID string) (*UnifiedSubscriptionStatus, error)
	CancelSubscription(ctx context.Context, userID string) error
	GetPlans(ctx context.Context) ([]models.SubscriptionPlan, error)
	CreateBillingPortalSession(ctx context.Context, userID, returnURL string) (string, error)
	ProcessWebhookEvent(ctx context.Context, event *stripe.Event) error
}

// Service handles subscription business logic
type Service struct {
	stripeClient stripeClient.Client
	subRepo      models.SubscriptionRepository
	userRepo     models.UserRepository
	webhookRepo  models.WebhookRepository
	logger       *log.Logger
}

// NewService creates a new subscription service
func NewService(
	stripeClient stripeClient.Client,
	subRepo models.SubscriptionRepository,
	userRepo models.UserRepository,
	webhookRepo models.WebhookRepository,
	logger *log.Logger,
) *Service {
	return &Service{
		stripeClient: stripeClient,
		subRepo:      subRepo,
		userRepo:     userRepo,
		webhookRepo:  webhookRepo,
		logger:       logger,
	}
}

// CreateSubscription creates a new subscription for a user
func (s *Service) CreateSubscription(ctx context.Context, userID, planID string) (*models.UserSubscription, error) {
	// Get user
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Get plan
	plan, err := s.subRepo.GetPlanByID(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("failed to get plan: %w", err)
	}

	// Check if user already has a subscription
	existingSub, existingSubErr := s.subRepo.GetUserSubscription(ctx, userID)
	if existingSubErr == nil {
		// User already has a subscription
		if existingSub.Status == "active" {
			return nil, errors.New("user already has an active subscription")
		}
	}

	// Create or get Stripe customer
	var customerID string
	if existingSubErr != nil || existingSub.StripeCustomerID == "" {
		// Create new customer
		stripeCustomer, err := s.stripeClient.CreateCustomer(ctx, &user)
		if err != nil {
			return nil, fmt.Errorf("failed to create Stripe customer: %w", err)
		}
		customerID = stripeCustomer.ID
	} else {
		customerID = existingSub.StripeCustomerID
	}

	// Create Stripe subscription
	stripeSubscription, err := s.stripeClient.CreateSubscription(ctx, customerID, plan.StripePriceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create Stripe subscription: %w", err)
	}

	// Extract client_secret if subscription is incomplete
	var clientSecret string
	if stripeSubscription.Status == "incomplete" {
		if stripeSubscription.LatestInvoice != nil && 
		   stripeSubscription.LatestInvoice.PaymentIntent != nil {
			clientSecret = stripeSubscription.LatestInvoice.PaymentIntent.ClientSecret
		}
	}

	// Create or update user subscription
	userSub := &models.UserSubscription{
		UserID:               userID,
		StripeCustomerID:     customerID,
		StripeSubscriptionID: stripeSubscription.ID,
		PlanID:               planID,
		Status:               string(stripeSubscription.Status),
		CurrentPeriodStart:   time.Unix(stripeSubscription.CurrentPeriodStart, 0),
		CurrentPeriodEnd:     time.Unix(stripeSubscription.CurrentPeriodEnd, 0),
		CancelAtPeriodEnd:    stripeSubscription.CancelAtPeriodEnd,
		ClientSecret:         clientSecret,
	}

	if existingSubErr != nil {
		// Create new subscription
		err = s.subRepo.CreateUserSubscription(ctx, userSub)
		if err != nil {
			return nil, fmt.Errorf("failed to create user subscription: %w", err)
		}
	} else {
		// Update existing subscription
		userSub.ID = existingSub.ID
		err = s.subRepo.UpdateUserSubscription(ctx, userSub)
		if err != nil {
			return nil, fmt.Errorf("failed to update user subscription: %w", err)
		}
	}

	s.logger.Printf("Created subscription for user %s with plan %s", userID, planID)
	return userSub, nil
}

// GetUserSubscription retrieves a user's subscription with plan details
func (s *Service) GetUserSubscription(ctx context.Context, userID string) (*SubscriptionWithPlan, error) {
	sub, err := s.subRepo.GetUserSubscription(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user subscription: %w", err)
	}

	plan, err := s.subRepo.GetPlanByID(ctx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("failed to get plan: %w", err)
	}

	return &SubscriptionWithPlan{
		Subscription: sub,
		Plan:         plan,
	}, nil
}

// GetUserSubscriptionStatus retrieves a unified subscription status for both free and paid users
func (s *Service) GetUserSubscriptionStatus(ctx context.Context, userID string) (*UnifiedSubscriptionStatus, error) {
	// Get user information to determine their subscription plan
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Get plan details from the user's subscription_plan_id
	plan, err := s.subRepo.GetPlanByID(ctx, user.SubscriptionPlanID)
	if err != nil {
		return nil, fmt.Errorf("failed to get plan: %w", err)
	}

	// Create the unified response
	status := &UnifiedSubscriptionStatus{
		Plan:   plan,
		IsPaid: user.SubscriptionPlanID != "free",
	}

	// If user is not on free plan, try to get their subscription details
	if user.SubscriptionPlanID != "free" {
		sub, err := s.subRepo.GetUserSubscription(ctx, userID)
		if err == nil {
			status.Subscription = &sub
		}
		// If error getting subscription, we still return the plan info
		// This handles edge cases where user.subscription_plan_id is updated but subscription record doesn't exist
	}

	return status, nil
}

// CancelSubscription cancels a user's subscription
func (s *Service) CancelSubscription(ctx context.Context, userID string) error {
	sub, err := s.subRepo.GetUserSubscription(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user subscription: %w", err)
	}

	if sub.StripeSubscriptionID == "" {
		return errors.New("no active subscription found")
	}

	_, err = s.stripeClient.CancelSubscription(ctx, sub.StripeSubscriptionID)
	if err != nil {
		return fmt.Errorf("failed to cancel Stripe subscription: %w", err)
	}

	s.logger.Printf("Canceled subscription for user %s", userID)
	return nil
}

// GetPlans retrieves all available subscription plans
func (s *Service) GetPlans(ctx context.Context) ([]models.SubscriptionPlan, error) {
	return s.subRepo.GetPlans(ctx)
}

// CreateBillingPortalSession creates a Stripe billing portal session
func (s *Service) CreateBillingPortalSession(ctx context.Context, userID, returnURL string) (string, error) {
	sub, err := s.subRepo.GetUserSubscription(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get user subscription: %w", err)
	}

	if sub.StripeCustomerID == "" {
		return "", errors.New("no customer found")
	}

	session, err := s.stripeClient.CreateBillingPortalSession(ctx, sub.StripeCustomerID, returnURL)
	if err != nil {
		return "", fmt.Errorf("failed to create billing portal session: %w", err)
	}

	return session.URL, nil
}

// ProcessWebhookEvent processes a Stripe webhook event
func (s *Service) ProcessWebhookEvent(ctx context.Context, event *stripe.Event) error {
	// Check if event has been processed
	processed, err := s.webhookRepo.IsEventProcessed(ctx, event.ID)
	if err != nil {
		return fmt.Errorf("failed to check if event is processed: %w", err)
	}

	if processed {
		s.logger.Printf("Event %s already processed", event.ID)
		return nil
	}

	// Log event for debugging
	s.logger.Printf("Processing webhook event: %s (ID: %s)", event.Type, event.ID)

	// Process with retry logic
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		err = s.processEventWithRetry(ctx, event)
		if err == nil {
			break
		}

		if i == maxRetries-1 {
			s.logger.Printf("Failed to process event %s after %d retries: %v", event.ID, maxRetries, err)
			return fmt.Errorf("failed to process event after %d retries: %w", maxRetries, err)
		}

		// Exponential backoff
		backoffDuration := time.Duration(math.Pow(2, float64(i))) * time.Second
		s.logger.Printf("Retrying event %s in %v (attempt %d/%d)", event.ID, backoffDuration, i+1, maxRetries)
		time.Sleep(backoffDuration)
	}

	if err != nil {
		return fmt.Errorf("failed to process event %s: %w", event.Type, err)
	}

	// Save event as processed
	eventData := make(map[string]interface{})
	if event.Data != nil && event.Data.Raw != nil {
		err = json.Unmarshal(event.Data.Raw, &eventData)
		if err != nil {
			s.logger.Printf("Failed to unmarshal event data: %v", err)
		}
	}

	webhookEvent := &models.WebhookEvent{
		StripeEventID: event.ID,
		EventType:     string(event.Type),
		Data:          eventData,
	}

	err = s.webhookRepo.SaveEvent(ctx, webhookEvent)
	if err != nil {
		return fmt.Errorf("failed to save webhook event: %w", err)
	}

	return nil
}

// processEventWithRetry processes event with error handling
func (s *Service) processEventWithRetry(ctx context.Context, event *stripe.Event) error {
	// Process event based on type
	switch event.Type {
	case "customer.subscription.created":
		return s.handleSubscriptionCreated(ctx, event)
	case "customer.subscription.updated":
		return s.handleSubscriptionUpdated(ctx, event)
	case "customer.subscription.deleted":
		return s.handleSubscriptionDeleted(ctx, event)
	case "invoice.payment_succeeded":
		return s.handleInvoicePaymentSucceeded(ctx, event)
	case "invoice.paid":
		return s.handleInvoicePaymentSucceeded(ctx, event) // Handle both events the same way
	case "invoice.payment_failed":
		return s.handleInvoicePaymentFailed(ctx, event)
	case "customer.subscription.trial_will_end":
		return s.handleTrialWillEnd(ctx, event)
	case "invoice.finalized":
		return s.handleInvoiceFinalized(ctx, event)
	default:
		s.logger.Printf("Unhandled event type: %s", event.Type)
		return nil // Don't return error for unhandled events
	}
}

// handleSubscriptionCreated processes subscription.created events
func (s *Service) handleSubscriptionCreated(ctx context.Context, event *stripe.Event) error {
	var subscription stripe.Subscription
	err := json.Unmarshal(event.Data.Raw, &subscription)
	if err != nil {
		return fmt.Errorf("failed to parse subscription object: %w", err)
	}

	return s.updateSubscriptionFromStripe(ctx, &subscription)
}

// handleSubscriptionUpdated processes subscription.updated events
func (s *Service) handleSubscriptionUpdated(ctx context.Context, event *stripe.Event) error {
	var subscription stripe.Subscription
	err := json.Unmarshal(event.Data.Raw, &subscription)
	if err != nil {
		return fmt.Errorf("failed to parse subscription object: %w", err)
	}

	return s.updateSubscriptionFromStripe(ctx, &subscription)
}

// handleSubscriptionDeleted processes subscription.deleted events
func (s *Service) handleSubscriptionDeleted(ctx context.Context, event *stripe.Event) error {
	var subscription stripe.Subscription
	err := json.Unmarshal(event.Data.Raw, &subscription)
	if err != nil {
		return fmt.Errorf("failed to parse subscription object: %w", err)
	}

	err = s.subRepo.UpdateSubscriptionStatus(ctx, subscription.ID, "canceled")
	if err != nil {
		return fmt.Errorf("failed to update subscription status: %w", err)
	}

	s.logger.Printf("Subscription %s deleted", subscription.ID)
	return nil
}

// handleInvoicePaymentSucceeded processes invoice.payment_succeeded events
func (s *Service) handleInvoicePaymentSucceeded(ctx context.Context, event *stripe.Event) error {
	var invoice stripe.Invoice
	err := json.Unmarshal(event.Data.Raw, &invoice)
	if err != nil {
		return fmt.Errorf("failed to parse invoice object: %w", err)
	}

	if invoice.Subscription != nil {
		err = s.subRepo.UpdateSubscriptionStatus(ctx, invoice.Subscription.ID, "active")
		if err != nil {
			return fmt.Errorf("failed to update subscription status: %w", err)
		}
	}

	s.logger.Printf("Invoice payment succeeded for subscription %s", invoice.Subscription.ID)
	return nil
}

// handleInvoicePaymentFailed processes invoice.payment_failed events
func (s *Service) handleInvoicePaymentFailed(ctx context.Context, event *stripe.Event) error {
	var invoice stripe.Invoice
	err := json.Unmarshal(event.Data.Raw, &invoice)
	if err != nil {
		return fmt.Errorf("failed to parse invoice object: %w", err)
	}

	if invoice.Subscription != nil {
		err = s.subRepo.UpdateSubscriptionStatus(ctx, invoice.Subscription.ID, "past_due")
		if err != nil {
			return fmt.Errorf("failed to update subscription status: %w", err)
		}
	}

	s.logger.Printf("Invoice payment failed for subscription %s", invoice.Subscription.ID)
	return nil
}

// handleTrialWillEnd processes customer.subscription.trial_will_end events
func (s *Service) handleTrialWillEnd(ctx context.Context, event *stripe.Event) error {
	var subscription stripe.Subscription
	err := json.Unmarshal(event.Data.Raw, &subscription)
	if err != nil {
		return fmt.Errorf("failed to parse subscription object: %w", err)
	}

	s.logger.Printf("Trial will end for subscription %s", subscription.ID)
	
	// You can add logic here to notify the user about trial ending
	// For example, send an email or update user flags
	
	return nil
}

// handleInvoiceFinalized processes invoice.finalized events
func (s *Service) handleInvoiceFinalized(ctx context.Context, event *stripe.Event) error {
	var invoice stripe.Invoice
	err := json.Unmarshal(event.Data.Raw, &invoice)
	if err != nil {
		return fmt.Errorf("failed to parse invoice object: %w", err)
	}

	s.logger.Printf("Invoice finalized for subscription %s", invoice.Subscription.ID)
	
	// You can add logic here to handle invoice finalization
	// For example, update billing records or trigger notifications
	
	return nil
}

// updateSubscriptionFromStripe updates local subscription from Stripe data
func (s *Service) updateSubscriptionFromStripe(ctx context.Context, stripeSubscription *stripe.Subscription) error {
	// Check if subscription has valid items
	if stripeSubscription.Items == nil || len(stripeSubscription.Items.Data) == 0 || 
		stripeSubscription.Items.Data[0].Price == nil {
		return errors.New("invalid subscription data: missing items or price")
	}
	
	// Get plan by Stripe price ID
	priceID := stripeSubscription.Items.Data[0].Price.ID
	plan, err := s.subRepo.GetPlanByStripeID(ctx, priceID)
	if err != nil {
		return fmt.Errorf("failed to get plan by Stripe price ID: %w", err)
	}

	// Update subscription
	userSub := &models.UserSubscription{
		StripeSubscriptionID: stripeSubscription.ID,
		PlanID:               plan.ID,
		Status:               string(stripeSubscription.Status),
		CurrentPeriodStart:   time.Unix(stripeSubscription.CurrentPeriodStart, 0),
		CurrentPeriodEnd:     time.Unix(stripeSubscription.CurrentPeriodEnd, 0),
		CancelAtPeriodEnd:    stripeSubscription.CancelAtPeriodEnd,
	}

	// Try to find existing subscription by Stripe subscription ID
	existingSub, err := s.subRepo.GetUserSubscription(ctx, stripeSubscription.Customer.ID)
	if err == nil {
		userSub.ID = existingSub.ID
		userSub.UserID = existingSub.UserID
		userSub.StripeCustomerID = existingSub.StripeCustomerID
		err = s.subRepo.UpdateUserSubscription(ctx, userSub)
	}

	if err != nil {
		return fmt.Errorf("failed to update subscription: %w", err)
	}

	// Update user's subscription plan ID
	if userSub.UserID != "" {
		err = s.userRepo.UpdateUserSubscriptionPlan(ctx, userSub.UserID, plan.ID)
		if err != nil {
			s.logger.Printf("Failed to update user subscription plan: %v", err)
			// Continue anyway - subscription was updated successfully
		}
	}

	s.logger.Printf("Updated subscription %s to status %s", stripeSubscription.ID, stripeSubscription.Status)
	return nil
}

// SubscriptionWithPlan combines subscription and plan information
type SubscriptionWithPlan struct {
	Subscription models.UserSubscription `json:"subscription"`
	Plan         models.SubscriptionPlan  `json:"plan"`
}

// UnifiedSubscriptionStatus provides a consistent response for all users
type UnifiedSubscriptionStatus struct {
	Plan         models.SubscriptionPlan   `json:"plan"`
	Subscription *models.UserSubscription  `json:"subscription,omitempty"`
	IsPaid       bool                     `json:"is_paid"`
	Usage        *UserUsageInfo           `json:"usage,omitempty"`
}

// UserUsageInfo provides usage information for the current user
type UserUsageInfo struct {
	CurrentUsage int `json:"current_usage"`
	DailyLimit   int `json:"daily_limit"`
}