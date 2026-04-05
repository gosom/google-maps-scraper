package services

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

type DashboardService struct {
	db *sql.DB
}

func NewDashboardService(db *sql.DB) *DashboardService {
	return &DashboardService{db: db}
}

func (s *DashboardService) GetDashboard(ctx context.Context, userID string, limit int) (*models.DashboardResponse, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	// Query 1: KPI aggregates
	var kpi models.DashboardKPI
	kpiQuery := `
		SELECT
			COUNT(*) FILTER (WHERE created_at >= $2)                              AS jobs_today,
			COALESCE(SUM(result_count), 0)                                        AS places_scraped_total,
			COUNT(*) FILTER (WHERE status IN ('working', 'pending', 'aborting'))   AS active_jobs
		FROM jobs
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	today := time.Now().Truncate(24 * time.Hour)
	err := s.db.QueryRowContext(ctx, kpiQuery, userID, today).Scan(
		&kpi.JobsToday, &kpi.PlacesScrapedTotal, &kpi.ActiveJobs,
	)
	if err != nil {
		return nil, fmt.Errorf("dashboard kpi query: %w", err)
	}

	// Query 2: Recent jobs with inline cost + result_count
	jobsQuery := `
		SELECT id, name, status,
			   created_at,
			   COALESCE(failure_reason, ''),
			   COALESCE(source, 'web'),
			   COALESCE(result_count, 0),
			   COALESCE(actual_cost_precise, 0)::text
		FROM jobs
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := s.db.QueryContext(ctx, jobsQuery, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("dashboard jobs query: %w", err)
	}
	defer rows.Close()

	recentJobs := make([]models.DashboardJob, 0, limit)
	for rows.Next() {
		var j models.DashboardJob
		if err := rows.Scan(
			&j.ID, &j.Name, &j.Status,
			&j.CreatedAt,
			&j.FailureReason, &j.Source,
			&j.ResultCount, &j.TotalCost,
		); err != nil {
			return nil, fmt.Errorf("dashboard job scan: %w", err)
		}
		recentJobs = append(recentJobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dashboard jobs iteration: %w", err)
	}

	return &models.DashboardResponse{
		KPI:        kpi,
		RecentJobs: recentJobs,
	}, nil
}
