package postgres

import (
	"context"
	"database/sql"
	"math"
	"strconv"

	"github.com/gosom/google-maps-scraper/models"
)

type pricingRuleRepository struct {
	db *sql.DB
}

// NewPricingRuleRepository creates a new PricingRuleRepository backed by PostgreSQL.
func NewPricingRuleRepository(db *sql.DB) models.PricingRuleRepository {
	return &pricingRuleRepository{db: db}
}

// GetActiveDefaultPrices returns event_type_code -> price in micro-credits for all
// currently active default pricing rules (ab_test_group IS NULL, valid_to IS NULL).
// Prices are scanned as text from the database and converted to integer
// micro-credits to avoid IEEE 754 float rounding errors.
func (r *pricingRuleRepository) GetActiveDefaultPrices(ctx context.Context) (map[string]int64, error) {
	const q = `
		SELECT event_type_code, price_credits::text
		FROM pricing_rules
		WHERE ab_test_group IS NULL
		  AND valid_to IS NULL`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := make(map[string]int64)
	for rows.Next() {
		var code string
		var priceStr string
		if err := rows.Scan(&code, &priceStr); err != nil {
			return nil, err
		}
		f, err := strconv.ParseFloat(priceStr, 64)
		if err != nil {
			return nil, err
		}
		prices[code] = int64(math.Round(f * models.MicroUnit))
	}
	return prices, rows.Err()
}
