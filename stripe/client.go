package stripe

import (
	"context"
	"fmt"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/billingportal/session"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/subscription"
	"github.com/stripe/stripe-go/v81/webhook"
)

// Client interface for Stripe operations
type Client interface {
	CreateCustomer(ctx context.Context, user *models.User) (*stripe.Customer, error)
	CreateSubscription(ctx context.Context, customerID, priceID string) (*stripe.Subscription, error)
	CancelSubscription(ctx context.Context, subscriptionID string) (*stripe.Subscription, error)
	CreateBillingPortalSession(ctx context.Context, customerID, returnURL string) (*stripe.BillingPortalSession, error)
	VerifyWebhook(payload []byte, signature, secret string) (*stripe.Event, error)
	GetSubscription(ctx context.Context, subscriptionID string) (*stripe.Subscription, error)
}

// client implements the Client interface
type client struct {
	apiKey string
}

// NewClient creates a new Stripe client
func NewClient(apiKey string) Client {
	stripe.Key = apiKey
	return &client{apiKey: apiKey}
}

// CreateCustomer creates a new Stripe customer
func (c *client) CreateCustomer(ctx context.Context, user *models.User) (*stripe.Customer, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(user.Email),
		Metadata: map[string]string{
			"user_id": user.ID,
		},
	}

	cust, err := customer.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create customer: %w", err)
	}

	return cust, nil
}

// CreateSubscription creates a new subscription for a customer
func (c *client) CreateSubscription(ctx context.Context, customerID, priceID string) (*stripe.Subscription, error) {
	params := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{
				Price: stripe.String(priceID),
			},
		},
		PaymentBehavior: stripe.String("default_incomplete"),
		PaymentSettings: &stripe.SubscriptionPaymentSettingsParams{
			SaveDefaultPaymentMethod: stripe.String("on_subscription"),
		},
		Expand: []*string{
			stripe.String("latest_invoice.payment_intent"),
			stripe.String("latest_invoice.payment_intent.payment_method"),
		},
	}

	sub, err := subscription.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscription: %w", err)
	}

	return sub, nil
}

// CancelSubscription cancels a subscription
func (c *client) CancelSubscription(ctx context.Context, subscriptionID string) (*stripe.Subscription, error) {
	params := &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(true),
	}

	sub, err := subscription.Update(subscriptionID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel subscription: %w", err)
	}

	return sub, nil
}

// CreateBillingPortalSession creates a billing portal session
func (c *client) CreateBillingPortalSession(ctx context.Context, customerID, returnURL string) (*stripe.BillingPortalSession, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}

	sess, err := session.New(params)
	if err != nil {
		return nil, fmt.Errorf("failed to create billing portal session: %w", err)
	}

	return sess, nil
}

// VerifyWebhook verifies a webhook signature
func (c *client) VerifyWebhook(payload []byte, signature, secret string) (*stripe.Event, error) {
	// Use ConstructEventWithOptions to ignore API version mismatch
	event, err := webhook.ConstructEventWithOptions(payload, signature, secret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to verify webhook: %w", err)
	}

	return &event, nil
}

// GetSubscription retrieves a subscription by ID
func (c *client) GetSubscription(ctx context.Context, subscriptionID string) (*stripe.Subscription, error) {
	sub, err := subscription.Get(subscriptionID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	return sub, nil
}