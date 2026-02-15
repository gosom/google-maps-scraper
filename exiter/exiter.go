package exiter

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// max returns the maximum of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type Exiter interface {
	SetSeedCount(int)
	SetMaxResults(int)
	GetMaxResults() int
	GetSeedProgress() (completed int, total int)
	GetResultsWritten() int
	SetCancelFunc(context.CancelFunc)
	IncrSeedCompleted(int)
	IncrPlacesFound(int)
	IncrPlacesCompleted(int)
	IncrResultsWritten(int) // New method to count actual results written
	Run(context.Context)
}

type exiter struct {
	seedCount             int
	seedCompleted         int
	placesFound           int
	placesCompleted       int
	resultsWritten        int       // New field to track actual results written
	maxResults            int       // Maximum number of places to find (0 = unlimited)
	cancellationTriggered bool      // Track if cancellation has been triggered
	lastProgressTime      time.Time // Track last time we made progress

	mu         *sync.Mutex
	cancelFunc context.CancelFunc
}

func New() Exiter {
	return &exiter{
		mu:               &sync.Mutex{},
		lastProgressTime: time.Now(),
	}
}

func (e *exiter) SetSeedCount(val int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.seedCount = val
}

func (e *exiter) SetMaxResults(val int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.maxResults = val
	slog.Debug("set_max_results", slog.Int("max_results", val))
}

func (e *exiter) GetMaxResults() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.maxResults
}

// GetSeedProgress returns the number of completed seeds and the total seeds
func (e *exiter) GetSeedProgress() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.seedCompleted, e.seedCount
}

// GetResultsWritten returns the number of results written
func (e *exiter) GetResultsWritten() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.resultsWritten
}

func (e *exiter) SetCancelFunc(fn context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.cancelFunc = fn
}

func (e *exiter) IncrSeedCompleted(val int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.seedCompleted += val
}

func (e *exiter) IncrPlacesFound(val int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.placesFound += val
	e.lastProgressTime = time.Now() // Update progress time when new places are found

	// Note: We don't cancel here anymore - we wait for places to be completed
	// The cancellation logic is moved to IncrPlacesCompleted
}

func (e *exiter) IncrPlacesCompleted(val int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Increment the completed count for exit detection
	// Note: This counts ALL completed jobs (success + failed) for exit timing
	// Actual result counting happens separately in IncrResultsWritten
	e.placesCompleted += val
	e.lastProgressTime = time.Now() // Update progress time

	slog.Debug("places_completed_updated",
		slog.Int("places_completed", e.placesCompleted),
		slog.Int("results_written", e.resultsWritten),
		slog.Int("max_results", e.maxResults),
	)
}

func (e *exiter) IncrResultsWritten(val int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if we've already triggered cancellation
	if e.maxResults > 0 && e.resultsWritten >= e.maxResults && e.cancellationTriggered {
		slog.Debug("results_increment_skipped",
			slog.Int("results_written", e.resultsWritten),
			slog.Int("max_results", e.maxResults),
			slog.Bool("cancellation_triggered", e.cancellationTriggered),
		)
		return
	}

	// Increment even if we're at the limit (for accurate counting)
	e.resultsWritten += val
	e.lastProgressTime = time.Now() // Update progress time

	slog.Debug("results_written_updated",
		slog.Int("results_written", e.resultsWritten),
		slog.Int("max_results", e.maxResults),
		slog.Bool("cancellation_triggered", e.cancellationTriggered),
	)

	// Check if we've reached the max results limit and trigger early exit
	if e.maxResults > 0 && e.resultsWritten >= e.maxResults && !e.cancellationTriggered {
		e.cancellationTriggered = true
		slog.Info("max_results_reached",
			slog.Int("results_written", e.resultsWritten),
			slog.Int("max_results", e.maxResults),
		)
		if e.cancelFunc != nil {
			slog.Debug("triggering_cancel_func")
			// Trigger cancellation - we've written enough results
			go e.cancelFunc() // Keep it async to avoid potential deadlocks
		} else {
			slog.Warn("cancel_func_nil_on_max_results",
				slog.Int("results_written", e.resultsWritten),
				slog.Int("max_results", e.maxResults),
			)
		}
	} else {
		slog.Debug("cancellation_not_triggered",
			slog.Int("results_written", e.resultsWritten),
			slog.Int("max_results", e.maxResults),
			slog.Bool("cancellation_triggered", e.cancellationTriggered),
		)
	}
}

func (e *exiter) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second * 1) // Check every second instead of 5 seconds
	defer ticker.Stop()

	slog.Debug("exit_monitor_started", slog.Int("seed_count", e.seedCount), slog.Int("max_results", e.maxResults))

	for {
		select {
		case <-ctx.Done():
			slog.Debug("exit_monitor_stopped_context_cancelled")
			return
		case <-ticker.C:
			e.mu.Lock()
			seedCompleted := e.seedCompleted
			seedCount := e.seedCount
			placesFound := e.placesFound
			placesCompleted := e.placesCompleted
			resultsWritten := e.resultsWritten
			maxResults := e.maxResults
			cancellationTriggered := e.cancellationTriggered
			e.mu.Unlock()

			// Log current state every 5 seconds
			if time.Now().Second()%5 == 0 {
				slog.Debug("exit_monitor_state",
					slog.Int("seed_completed", seedCompleted),
					slog.Int("seed_count", seedCount),
					slog.Int("places_completed", placesCompleted),
					slog.Int("places_found", placesFound),
					slog.Int("results_written", resultsWritten),
					slog.Int("max_results", maxResults),
					slog.Bool("cancellation_triggered", cancellationTriggered),
				)
			}

			if e.isDone() {
				slog.Debug("exit_monitor_detected_completion")
				slog.Info("exit_monitor_final_state",
					slog.Int("seed_completed", seedCompleted),
					slog.Int("seed_count", seedCount),
					slog.Int("places_completed", placesCompleted),
					slog.Int("places_found", placesFound),
					slog.Int("results_written", resultsWritten),
					slog.Int("max_results", maxResults),
				)
				if e.cancelFunc != nil {
					slog.Debug("exit_monitor_calling_cancel_func")
					e.cancelFunc()
					slog.Debug("exit_monitor_cancel_func_called")
				} else {
					slog.Warn("exit_monitor_cancel_func_nil")
				}
				return
			}
		}
	}
}

func (e *exiter) isDone() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Simple and reliable exit logic:
	// 1. If max results is set and we've written enough results, we're done
	if e.maxResults > 0 && e.resultsWritten >= e.maxResults {
		slog.Debug("exit_done_max_results_reached",
			slog.Int("results_written", e.resultsWritten),
			slog.Int("max_results", e.maxResults),
		)
		return true
	}

	// 2. Check if all seeds are complete - if not, keep going
	if e.seedCompleted < e.seedCount {
		slog.Debug("exit_not_done_seeds_incomplete",
			slog.Int("seed_completed", e.seedCompleted),
			slog.Int("seed_count", e.seedCount),
		)
		return false
	}

	// 2b. Seeds are complete but no places were found. Without this, the exit
	// monitor can wait forever on an empty job graph (no place jobs to finish).
	if e.seedCount > 0 && e.seedCompleted >= e.seedCount && e.placesFound == 0 {
		slog.Debug("exit_done_zero_places_found",
			slog.Int("seed_completed", e.seedCompleted),
			slog.Int("seed_count", e.seedCount),
			slog.Int("places_found", e.placesFound),
		)
		return true
	}

	// 3. For unlimited results (maxResults = 0), check if all places are processed
	if e.maxResults == 0 {
		if e.placesFound > 0 && e.placesCompleted >= e.placesFound {
			slog.Debug("exit_done_unlimited_all_places_complete",
				slog.Int("places_completed", e.placesCompleted),
				slog.Int("places_found", e.placesFound),
			)
			return true
		}
		// Add timeout protection - if we've been waiting too long for the last place
		// and we have results, consider it done (handles stuck/failed jobs)
		if e.placesFound > 0 && e.resultsWritten > 0 {
			// Check for inactivity timeout (30 seconds without progress)
			inactivityDuration := time.Since(e.lastProgressTime)
			const maxInactivity = 30 * time.Second

			// If we're missing only 1-2 places and haven't made progress, exit
			missingPlaces := e.placesFound - e.placesCompleted
			if (missingPlaces <= 2 && missingPlaces > 0) && inactivityDuration > maxInactivity {
				slog.Debug("exit_done_unlimited_inactivity_timeout",
					slog.Duration("inactivity", inactivityDuration),
					slog.Int("missing_places", missingPlaces),
				)
				return true
			} else if missingPlaces <= 1 && missingPlaces > 0 {
				// Only exit for 1 missing place if we have a decent number of results
				// AND we haven't had recent activity
				if e.resultsWritten >= 10 && inactivityDuration > (10*time.Second) {
					slog.Debug("exit_done_unlimited_accept_missing_place",
						slog.Int("missing_places", missingPlaces),
						slog.Int("results_written", e.resultsWritten),
						slog.Duration("inactivity", inactivityDuration),
					)
					return true
				}
			}
		}
		inactivityDuration := time.Since(e.lastProgressTime)
		slog.Debug("exit_not_done_unlimited_waiting",
			slog.Int("places_completed", e.placesCompleted),
			slog.Int("places_found", e.placesFound),
			slog.Duration("inactivity", inactivityDuration),
		)
		return false
	}

	// 4. For limited results, we're done if seeds are complete
	// (results will be capped by IncrResultsWritten)
	if e.placesFound > 0 && e.placesCompleted >= e.placesFound {
		slog.Debug("exit_done_limited_all_places_complete",
			slog.Int("places_completed", e.placesCompleted),
			slog.Int("places_found", e.placesFound),
		)
		return true
	}
	slog.Debug("exit_not_done_limited_collecting_more_results")
	return false
}
