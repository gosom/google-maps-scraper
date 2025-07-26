package exiter

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type Exiter interface {
	SetSeedCount(int)
	SetMaxResults(int)
	SetCancelFunc(context.CancelFunc)
	IncrSeedCompleted(int)
	IncrPlacesFound(int)
	IncrPlacesCompleted(int)
	Run(context.Context)
}

type exiter struct {
	seedCount             int
	seedCompleted         int
	placesFound           int
	placesCompleted       int
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

	// Check if we've already triggered cancellation
	if e.maxResults > 0 && e.placesCompleted >= e.maxResults && e.cancellationTriggered {
		fmt.Printf("DEBUG: Skipping increment - already at limit and cancellation triggered (completed: %d, limit: %d)\n", e.placesCompleted, e.maxResults)
		return
	}

	// Increment even if we're at the limit (for accurate counting)
	e.placesCompleted += val

	// DEBUG: Log the current state
	fmt.Printf("DEBUG: IncrPlacesCompleted - placesCompleted: %d, maxResults: %d, cancellationTriggered: %v\n", e.placesCompleted, e.maxResults, e.cancellationTriggered)

	// Check if we've reached the max results limit and trigger early exit
	if e.maxResults > 0 && e.placesCompleted >= e.maxResults && !e.cancellationTriggered {
		e.cancellationTriggered = true
		fmt.Printf("DEBUG: Max results reached! Triggering cancellation - completed: %d, limit: %d\n", e.placesCompleted, e.maxResults)
		if e.cancelFunc != nil {
			fmt.Printf("DEBUG: Calling cancel function to stop job execution\n")
			// Trigger cancellation - we've completed enough results
			go e.cancelFunc() // Keep it async to avoid potential deadlocks
		} else {
			fmt.Printf("DEBUG: WARNING - cancelFunc is nil, cannot trigger cancellation\n")
		}
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
				fmt.Printf("DEBUG: Exit monitor state - seeds: %d/%d, places: %d/%d, maxResults: %d, cancelled: %v\n",
					seedCompleted, seedCount, placesCompleted, placesFound, maxResults, cancellationTriggered)
			}

			if e.isDone() {
				fmt.Printf("DEBUG: Exit monitor detected completion, calling cancelFunc\n")
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

	// If we have a max results limit and reached it, we're done immediately
	if e.maxResults > 0 && e.placesCompleted >= e.maxResults {
		fmt.Printf("DEBUG: isDone() returning true - max results reached (completed: %d, limit: %d, cancellationTriggered: %v)\n", e.placesCompleted, e.maxResults, e.cancellationTriggered)
		return true
	}

	// Normal completion: all seeds done and all places processed
	if e.seedCompleted != e.seedCount {
		return false
	}

	if e.placesFound != e.placesCompleted {
		return false
	}

	return true
}
