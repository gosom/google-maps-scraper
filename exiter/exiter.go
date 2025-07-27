package exiter

import (
	"context"
	"fmt"
	"log"
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
	resultsWritten        int  // New field to track actual results written
	maxResults            int  // Maximum number of places to find (0 = unlimited)
	cancellationTriggered bool // Track if cancellation has been triggered

	mu         *sync.Mutex
	cancelFunc context.CancelFunc
}

func New() Exiter {
	return &exiter{
		mu: &sync.Mutex{},
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
	fmt.Printf("DEBUG: SetMaxResults called with value: %d\n", val)
}

func (e *exiter) GetMaxResults() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.maxResults
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

	// DEBUG: Log the current state
	fmt.Printf("DEBUG: IncrPlacesCompleted - placesCompleted: %d, resultsWritten: %d, maxResults: %d\n",
		e.placesCompleted, e.resultsWritten, e.maxResults)
}

func (e *exiter) IncrResultsWritten(val int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if we've already triggered cancellation
	if e.maxResults > 0 && e.resultsWritten >= e.maxResults && e.cancellationTriggered {
		fmt.Printf("DEBUG: Skipping results increment - already at limit and cancellation triggered (written: %d, limit: %d)\n", e.resultsWritten, e.maxResults)
		return
	}

	// Increment even if we're at the limit (for accurate counting)
	e.resultsWritten += val

	// DEBUG: Log the current state
	fmt.Printf("DEBUG: IncrResultsWritten - resultsWritten: %d, maxResults: %d, cancellationTriggered: %v\n", e.resultsWritten, e.maxResults, e.cancellationTriggered)

	// Check if we've reached the max results limit and trigger early exit
	if e.maxResults > 0 && e.resultsWritten >= e.maxResults && !e.cancellationTriggered {
		e.cancellationTriggered = true
		fmt.Printf("DEBUG: MAX RESULTS LIMIT REACHED! Triggering cancellation - written: %d, limit: %d\n", e.resultsWritten, e.maxResults)
		if e.cancelFunc != nil {
			fmt.Printf("DEBUG: Calling cancel function to stop job execution\n")
			// Trigger cancellation - we've written enough results
			go e.cancelFunc() // Keep it async to avoid potential deadlocks
		} else {
			fmt.Printf("DEBUG: WARNING - cancelFunc is nil, cannot trigger cancellation\n")
		}
	} else {
		fmt.Printf("DEBUG: Not triggering cancellation - written: %d, limit: %d, triggered: %v\n", e.resultsWritten, e.maxResults, e.cancellationTriggered)
	}
}

func (e *exiter) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second * 1) // Check every second instead of 5 seconds
	defer ticker.Stop()

	log.Printf("DEBUG: Exit monitor started - seedCount: %d, maxResults: %d", e.seedCount, e.maxResults)

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("DEBUG: Exit monitor stopped due to context cancellation\n")
			return
		case <-ticker.C:
			e.mu.Lock()
			seedCompleted := e.seedCompleted
			seedCount := e.seedCount
			placesFound := e.placesFound
			placesCompleted := e.placesCompleted
			maxResults := e.maxResults
			cancellationTriggered := e.cancellationTriggered
			e.mu.Unlock()

			// Log current state every 5 seconds
			if time.Now().Second()%5 == 0 {
				fmt.Printf("DEBUG: Exit monitor state - seeds: %d/%d, places: %d/%d, resultsWritten: %d, maxResults: %d, cancelled: %v\n",
					seedCompleted, seedCount, placesCompleted, placesFound, e.resultsWritten, maxResults, cancellationTriggered)
			}

			if e.isDone() {
				fmt.Printf("DEBUG: Exit monitor detected completion, calling cancelFunc\n")
				fmt.Printf("DEBUG: Final state - seeds: %d/%d, places: %d/%d, results: %d/%d\n",
					seedCompleted, seedCount, placesCompleted, placesFound, e.resultsWritten, maxResults)
				if e.cancelFunc != nil {
					e.cancelFunc()
				} else {
					fmt.Printf("DEBUG: WARNING - cancelFunc is nil in Run()\n")
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
		fmt.Printf("DEBUG: isDone() - max results reached (written: %d >= limit: %d)\n", e.resultsWritten, e.maxResults)
		return true
	}

	// 2. Check if all seeds are complete - if not, keep going
	if e.seedCompleted < e.seedCount {
		fmt.Printf("DEBUG: isDone() - seeds not complete (%d/%d)\n", e.seedCompleted, e.seedCount)
		return false
	}

	// 3. For unlimited results (maxResults = 0), check if all places are processed
	if e.maxResults == 0 {
		if e.placesFound > 0 && e.placesCompleted >= e.placesFound {
			fmt.Printf("DEBUG: isDone() - unlimited mode, all places complete (%d/%d)\n", e.placesCompleted, e.placesFound)
			return true
		}
		fmt.Printf("DEBUG: isDone() - unlimited mode, waiting for places (%d/%d)\n", e.placesCompleted, e.placesFound)
		return false
	}

	// 4. For limited results, we're done if seeds are complete
	// (results will be capped by IncrResultsWritten)
	fmt.Printf("DEBUG: isDone() - limited mode, seeds complete, continuing to collect results\n")
	return false
}
