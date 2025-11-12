package writers

import (
	"context"
	"errors"
	"fmt"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

// CancellationAwareCSVWriter wraps a CSV writer to handle context cancellation gracefully
// It relies on the exit monitor's cancellation mechanism rather than checking limits independently
// This avoids race conditions between CSV and PostgreSQL writers
type CancellationAwareCSVWriter struct {
	wrapped scrapemate.ResultWriter
}

// NewCancellationAwareCSVWriter creates a CSV writer that respects context cancellation
func NewCancellationAwareCSVWriter(wrapped scrapemate.ResultWriter) scrapemate.ResultWriter {
	return &CancellationAwareCSVWriter{
		wrapped: wrapped,
	}
}

func (w *CancellationAwareCSVWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	// Create a new channel to pass results to wrapped writer
	filteredChan := make(chan scrapemate.Result, 10)
	errChan := make(chan error, 1)

	// Start the wrapped writer with the filtered channel
	go func() {
		errChan <- w.wrapped.Run(ctx, filteredChan)
	}()

	// Pass results through, checking for cancellation
	for result := range in {
		// Check for cancellation from exit monitor
		select {
		case <-ctx.Done():
			fmt.Printf("DEBUG: CSV Writer - Context cancelled, stopping\n")
			close(filteredChan)
			return ctx.Err()
		default:
		}

		// Validate result type
		entry, ok := result.Data.(*gmaps.Entry)
		if !ok {
			close(filteredChan)
			return errors.New("invalid data type")
		}

		// Log for debugging (but don't filter)
		if entry.Title == "" {
			fmt.Printf("DEBUG: CSV Writer - Processing result with empty title\n")
		} else {
			fmt.Printf("DEBUG: CSV Writer - Processing result: %s\n", entry.Title)
		}

		// Pass result to wrapped writer
		filteredChan <- result
	}

	// Input channel closed, close filtered channel
	close(filteredChan)

	// Wait for wrapped writer to finish
	return <-errChan
}
