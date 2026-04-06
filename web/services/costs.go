package services

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
)

// CostsService provides job cost breakdown queries.
type CostsService struct {
	db  *sql.DB
	log *slog.Logger
}

func NewCostsService(db *sql.DB) *CostsService {
	return &CostsService{
		db:  db,
		log: pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "costs"),
	}
}

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
		s.log.Error("job_costs_breakdown_query_failed", slog.String("job_id", jobID), slog.Any("error", err))
		return resp, fmt.Errorf("failed to query cost breakdown: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item models.JobCostBreakdownItem
		if err := rows.Scan(&item.EventType, &item.Quantity, &item.CostCredits); err != nil {
			s.log.Error("job_costs_breakdown_scan_failed", slog.String("job_id", jobID), slog.Any("error", err))
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
		s.log.Error("job_costs_totals_query_failed", slog.String("job_id", jobID), slog.Any("error", err))
		return resp, fmt.Errorf("failed to query job totals: %w", err)
	}

	s.log.Debug("job_costs_retrieved", slog.String("job_id", jobID), slog.String("total_credits", resp.TotalCredits), slog.Int("breakdown_items", len(resp.Items)))
	return resp, nil
}

// GetBatchJobCosts returns cost breakdowns and totals for multiple jobs in a
// single set of SQL queries, avoiding N+1 per-job round-trips.
// The caller must ensure that all jobIDs belong to the authenticated user.
func (s *CostsService) GetBatchJobCosts(ctx context.Context, jobIDs []string) (map[string]models.JobCostResponse, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if len(jobIDs) == 0 {
		return map[string]models.JobCostResponse{}, nil
	}

	result := make(map[string]models.JobCostResponse, len(jobIDs))

	// Pre-populate entries so jobs without breakdown rows still appear.
	for _, id := range jobIDs {
		result[id] = models.JobCostResponse{JobID: id}
	}

	// Build numbered parameter placeholders ($1, $2, ...) and args slice.
	args := make([]any, len(jobIDs))
	placeholders := make([]string, len(jobIDs))
	for i, id := range jobIDs {
		args[i] = id
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	inClause := strings.Join(placeholders, ", ")

	// Query 1: breakdown rows for all requested jobs.
	breakdownQ := fmt.Sprintf(`
		SELECT job_id, event_type_code, quantity_total, cost_total_credits::text
		FROM job_cost_breakdown
		WHERE job_id IN (%s)
		ORDER BY job_id, event_type_code`, inClause)

	rows, err := s.db.QueryContext(ctx, breakdownQ, args...)
	if err != nil {
		s.log.Error("batch_costs_breakdown_query_failed", slog.Any("error", err))
		return nil, fmt.Errorf("failed to query batch cost breakdown: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var jobID string
		var item models.JobCostBreakdownItem
		if err := rows.Scan(&jobID, &item.EventType, &item.Quantity, &item.CostCredits); err != nil {
			s.log.Error("batch_costs_breakdown_scan_failed", slog.Any("error", err))
			return nil, fmt.Errorf("failed to scan batch cost breakdown: %w", err)
		}
		entry := result[jobID]
		entry.Items = append(entry.Items, item)
		result[jobID] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("batch breakdown row iteration error: %w", err)
	}

	// Query 2: totals from jobs table.
	totalsQ := fmt.Sprintf(`
		SELECT id, COALESCE(actual_cost_precise, 0)::text, COALESCE(actual_cost, 0)
		FROM jobs
		WHERE id IN (%s)`, inClause)

	tRows, err := s.db.QueryContext(ctx, totalsQ, args...)
	if err != nil {
		s.log.Error("batch_costs_totals_query_failed", slog.Any("error", err))
		return nil, fmt.Errorf("failed to query batch job totals: %w", err)
	}
	defer tRows.Close()

	for tRows.Next() {
		var jobID, totalCredits string
		var totalRounded int
		if err := tRows.Scan(&jobID, &totalCredits, &totalRounded); err != nil {
			s.log.Error("batch_costs_totals_scan_failed", slog.Any("error", err))
			return nil, fmt.Errorf("failed to scan batch job totals: %w", err)
		}
		entry := result[jobID]
		entry.TotalCredits = totalCredits
		entry.TotalRounded = totalRounded
		result[jobID] = entry
	}
	if err := tRows.Err(); err != nil {
		return nil, fmt.Errorf("batch totals row iteration error: %w", err)
	}

	s.log.Debug("batch_costs_retrieved", slog.Int("job_count", len(result)))
	return result, nil
}
