package models

import (
	"context"
	"time"
)

// PricingRule represents an active pricing rule from the pricing_rules table.
type PricingRule struct {
	ID            string
	EventTypeCode string
	PriceCredits  float64
	ValidFrom     time.Time
	ValidTo       *time.Time
}

// PricingRuleRepository provides access to pricing rules.
type PricingRuleRepository interface {
	// GetActiveDefaultPrices returns a map of event_type_code -> price_credits
	// for all currently active default (non-AB-test) pricing rules.
	GetActiveDefaultPrices(ctx context.Context) (map[string]float64, error)
}
