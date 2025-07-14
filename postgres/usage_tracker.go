package postgres

import (
	"context"
	"database/sql"
	"encoding/csv"
	"log"
	"os"
	"time"
)

// JobUsageDetails represents detailed tracking for each job
type JobUsageDetails struct {
	ID                     int
	JobID                  string
	UserID                 string
	TotalLocationsFound    int
	TotalEmailsFound       int
	JobDurationSeconds     int
	JobStatus              string
	EmailExtractionEnabled bool
	FastModeEnabled        bool
	StartedAt              time.Time
	CompletedAt            *time.Time
	CreatedAt              time.Time
}

// UsageTracker handles all usage tracking operations
type UsageTracker struct {
	db *sql.DB
}

// NewUsageTracker creates a new UsageTracker instance
func NewUsageTracker(db *sql.DB) *UsageTracker {
	return &UsageTracker{db: db}
}

// StartJobTracking creates initial job usage record when job starts
func (u *UsageTracker) StartJobTracking(ctx context.Context, jobID, userID string, emailEnabled, fastMode bool) error {
	const q = `
		INSERT INTO job_usage_details 
		(job_id, user_id, email_extraction_enabled, fast_mode_enabled, job_status, started_at)
		VALUES ($1, $2, $3, $4, 'running', $5)
		ON CONFLICT (job_id) DO NOTHING`

	_, err := u.db.ExecContext(ctx, q, jobID, userID, emailEnabled, fastMode, time.Now())
	if err != nil {
		log.Printf("Failed to start job tracking for job %s: %v", jobID, err)
	}
	return err
}

// CompleteJobTracking updates job with final results and updates user totals
func (u *UsageTracker) CompleteJobTracking(ctx context.Context, jobID string, locationsFound, emailsFound int, duration time.Duration, success bool) error {
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Determine job status
	status := "completed"
	if !success {
		status = "failed"
	}

	// Get user ID from job usage details
	var userID string
	var emailEnabled bool
	err = tx.QueryRowContext(ctx,
		"SELECT user_id, email_extraction_enabled FROM job_usage_details WHERE job_id = $1",
		jobID).Scan(&userID, &emailEnabled)
	if err != nil {
		log.Printf("Failed to get user ID for job %s: %v", jobID, err)
		return err
	}

	// Only count emails if email extraction was actually enabled
	actualEmailsFound := emailsFound
	if !emailEnabled {
		actualEmailsFound = 0
	}

	// Update job usage details
	const updateJobQ = `
		UPDATE job_usage_details 
		SET total_locations_found = $1,
			total_emails_found = $2,
			job_duration_seconds = $3,
			job_status = $4,
			completed_at = $5
		WHERE job_id = $6`

	_, err = tx.ExecContext(ctx, updateJobQ,
		locationsFound, actualEmailsFound, int(duration.Seconds()),
		status, time.Now(), jobID)
	if err != nil {
		log.Printf("Failed to update job usage details for job %s: %v", jobID, err)
		return err
	}

	// Update user totals (only for successful jobs)
	if success {
		err = u.updateUserTotals(ctx, tx, userID, 1, locationsFound, actualEmailsFound)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// updateUserTotals increments user statistics
func (u *UsageTracker) updateUserTotals(ctx context.Context, tx *sql.Tx, userID string, jobs, locations, emails int) error {
	// Check if monthly reset is needed
	err := u.checkAndResetMonthlyCounters(ctx, tx, userID)
	if err != nil {
		log.Printf("Failed to check monthly reset for user %s: %v", userID, err)
		// Continue anyway, don't fail the transaction
	}

	const q = `
		UPDATE user_usage 
		SET total_jobs_run = total_jobs_run + $1,
			total_locations_scraped = total_locations_scraped + $2,
			total_emails_extracted = total_emails_extracted + $3,
			current_month_jobs = current_month_jobs + $1,
			current_month_locations = current_month_locations + $2,
			current_month_emails = current_month_emails + $3,
			job_count = job_count + $1,
			last_job_date = $4,
			updated_at = $4
		WHERE user_id = $5`

	_, err = tx.ExecContext(ctx, q, jobs, locations, emails, time.Now(), userID)
	if err != nil {
		log.Printf("Failed to update user totals for user %s: %v", userID, err)
	}
	return err
}

// checkAndResetMonthlyCounters resets monthly counters if we're in a new month
func (u *UsageTracker) checkAndResetMonthlyCounters(ctx context.Context, tx *sql.Tx, userID string) error {
	now := time.Now()
	currentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	const checkQ = `SELECT last_reset_date FROM user_usage WHERE user_id = $1`
	var lastReset sql.NullTime
	err := tx.QueryRowContext(ctx, checkQ, userID).Scan(&lastReset)
	if err != nil {
		return err
	}

	// If no last reset date or it's from a previous month, reset counters
	if !lastReset.Valid || lastReset.Time.Before(currentMonth) {
		const resetQ = `
			UPDATE user_usage 
			SET current_month_jobs = 0,
				current_month_locations = 0,
				current_month_emails = 0,
				last_reset_date = $1
			WHERE user_id = $2`

		_, err = tx.ExecContext(ctx, resetQ, currentMonth, userID)
		if err != nil {
			log.Printf("Failed to reset monthly counters for user %s: %v", userID, err)
		}
		return err
	}

	return nil
}

// CountResultsFromCSV counts locations and emails from a completed job's CSV file
func (u *UsageTracker) CountResultsFromCSV(csvPath string, emailEnabled bool) (locations int, emails int, err error) {
	file, err := os.Open(csvPath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return 0, 0, err
	}

	// Skip header row
	if len(records) <= 1 {
		return 0, 0, nil
	}

	locations = len(records) - 1

	// Count emails if email extraction was enabled
	if emailEnabled && len(records) > 1 {
		// Assuming email is in one of the columns (need to check CSV structure)
		for _, record := range records[1:] {
			for _, field := range record {
				if isValidEmail(field) {
					emails++
					break // Only count one email per business
				}
			}
		}
	}

	return locations, emails, nil
}

// isValidEmail does basic email validation
func isValidEmail(email string) bool {
	if len(email) < 5 {
		return false
	}
	// Very basic check - contains @ and .
	hasAt := false
	hasDot := false
	for _, char := range email {
		if char == '@' {
			hasAt = true
		}
		if char == '.' && hasAt {
			hasDot = true
		}
	}
	return hasAt && hasDot && email != "" && email != "N/A" && email != "null"
}

// GetUserUsageSummary returns current usage statistics for a user
func (u *UsageTracker) GetUserUsageSummary(ctx context.Context, userID string) (*UserUsageSummary, error) {
	// Ensure monthly reset is checked
	tx, err := u.db.BeginTx(ctx, nil)
	if err == nil {
		u.checkAndResetMonthlyCounters(ctx, tx, userID)
		tx.Commit()
	}

	const q = `
		SELECT 
			total_jobs_run,
			total_locations_scraped,
			total_emails_extracted,
			current_month_jobs,
			current_month_locations,
			current_month_emails,
			last_job_date,
			last_reset_date
		FROM user_usage 
		WHERE user_id = $1`

	var summary UserUsageSummary
	var lastJob, lastReset sql.NullTime

	err = u.db.QueryRowContext(ctx, q, userID).Scan(
		&summary.TotalJobs,
		&summary.TotalLocations,
		&summary.TotalEmails,
		&summary.CurrentMonthJobs,
		&summary.CurrentMonthLocations,
		&summary.CurrentMonthEmails,
		&lastJob,
		&lastReset,
	)

	if lastJob.Valid {
		summary.LastJobDate = &lastJob.Time
	}
	if lastReset.Valid {
		summary.LastResetDate = &lastReset.Time
	}

	return &summary, err
}

// UserUsageSummary contains user usage statistics
type UserUsageSummary struct {
	TotalJobs             int
	TotalLocations        int
	TotalEmails           int
	CurrentMonthJobs      int
	CurrentMonthLocations int
	CurrentMonthEmails    int
	LastJobDate           *time.Time
	LastResetDate         *time.Time
}
