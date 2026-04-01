package models

import (
	"context"
	"time"
)

// MicroUnit is the multiplier used to convert credit values to integer
// micro-credits, eliminating IEEE 754 float rounding errors during
// arithmetic. 1 credit = 1,000,000 micro-credits.
const MicroUnit = 1_000_000

// PricingRule represents an active pricing rule from the pricing_rules table.
type PricingRule struct {
	ID            string
	EventTypeCode string
	PriceCredits  string // stored as string to avoid IEEE 754 float rounding
	ValidFrom     time.Time
	ValidTo       *time.Time
}

// PricingRuleRepository provides access to pricing rules.
type PricingRuleRepository interface {
	// GetActiveDefaultPrices returns a map of event_type_code -> price in
	// micro-credits (int64, 1 credit = 1,000,000 micro-credits) for all
	// currently active default (non-AB-test) pricing rules.
	GetActiveDefaultPrices(ctx context.Context) (map[string]int64, error)
}
