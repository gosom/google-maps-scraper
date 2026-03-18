package postgres

import (
	"context"
	"database/sql"

	"github.com/gosom/google-maps-scraper/models"
)

type pricingRuleRepository struct {
	db *sql.DB
}

// NewPricingRuleRepository creates a new PricingRuleRepository backed by PostgreSQL.
func NewPricingRuleRepository(db *sql.DB) models.PricingRuleRepository {
	return &pricingRuleRepository{db: db}
}

// GetActiveDefaultPrices returns event_type_code -> price_credits for all
// currently active default pricing rules (ab_test_group IS NULL, valid_to IS NULL).
func (r *pricingRuleRepository) GetActiveDefaultPrices(ctx context.Context) (map[string]float64, error) {
	const q = `
		SELECT event_type_code, price_credits
		FROM pricing_rules
		WHERE ab_test_group IS NULL
		  AND valid_to IS NULL`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := make(map[string]float64)
	for rows.Next() {
		var code string
		var price float64
		if err := rows.Scan(&code, &price); err != nil {
			return nil, err
		}
		prices[code] = price
	}
	return prices, rows.Err()
}
