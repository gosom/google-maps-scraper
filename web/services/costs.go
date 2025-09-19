package services

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/gosom/google-maps-scraper/models"
)

// CostsService provides job cost breakdown queries.
type CostsService struct{ db *sql.DB }

func NewCostsService(db *sql.DB) *CostsService { return &CostsService{db: db} }

// GetJobCosts returns per-event breakdown and totals for a job.
func (s *CostsService) GetJobCosts(ctx context.Context, jobID string) (models.JobCostResponse, error) {
	var resp models.JobCostResponse
	if s.db == nil {
		return resp, fmt.Errorf("database not available")
	}
	resp.JobID = jobID

	// Fetch breakdown rows
	const breakdownQ = `
		SELECT event_type_code, quantity_total, cost_total_credits::text
		FROM job_cost_breakdown
		WHERE job_id = $1
		ORDER BY event_type_code`
	rows, err := s.db.QueryContext(ctx, breakdownQ, jobID)
	if err != nil {
		return resp, fmt.Errorf("failed to query cost breakdown: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item models.JobCostBreakdownItem
		if err := rows.Scan(&item.EventType, &item.Quantity, &item.CostCredits); err != nil {
			return resp, fmt.Errorf("failed to scan cost breakdown: %w", err)
		}
		resp.Items = append(resp.Items, item)
	}
	if err := rows.Err(); err != nil {
		return resp, fmt.Errorf("row iteration error: %w", err)
	}

	// Fetch totals from jobs table
	const totalsQ = `
		SELECT COALESCE(actual_cost_precise, 0)::text, COALESCE(actual_cost, 0)
		FROM jobs WHERE id = $1`
	if err := s.db.QueryRowContext(ctx, totalsQ, jobID).Scan(&resp.TotalCredits, &resp.TotalRounded); err != nil {
		return resp, fmt.Errorf("failed to query job totals: %w", err)
	}

	return resp, nil
}
