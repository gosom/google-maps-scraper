package exiter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSeedOutcome_Predicates pins the truth table for IsTerminal and
// IsTerminalFailure across every meaningful (Err, RetriesLeft, PlacesFound)
// combination. isDone()'s fail-fast logic in exiter.go reads
// IsTerminalFailure() to decide whether to short-circuit a doomed run, so
// any drift in these predicates silently changes production exit behaviour.
func TestSeedOutcome_Predicates(t *testing.T) {
	t.Parallel()

	scrapeErr := errors.New("playwright: net::ERR_PROXY_CONNECTION_FAILED")

	tests := []struct {
		name             string
		o                SeedOutcome
		wantTerminal     bool
		wantTerminalFail bool
	}{
		{
			name:             "zero value: succeeded with 0 places — terminal, not a failure",
			o:                SeedOutcome{},
			wantTerminal:     true,
			wantTerminalFail: false,
		},
		{
			name:             "success with results: terminal, not a failure",
			o:                SeedOutcome{PlacesFound: 5},
			wantTerminal:     true,
			wantTerminalFail: false,
		},
		{
			name:             "terminal failure: err set, no retries, no places",
			o:                SeedOutcome{Err: scrapeErr, RetriesLeft: 0, PlacesFound: 0},
			wantTerminal:     true,
			wantTerminalFail: true,
		},
		{
			name: "err with places found: NOT a terminal failure (partial work salvaged)",
			// A seed that errored out late but still produced places should
			// not contribute to fail-fast — those places will still flow
			// through PlaceJob processing.
			o:                SeedOutcome{Err: scrapeErr, RetriesLeft: 0, PlacesFound: 3},
			wantTerminal:     true,
			wantTerminalFail: false,
		},
		{
			name:             "retries remaining: NOT terminal (scrapemate may still retry)",
			o:                SeedOutcome{Err: scrapeErr, RetriesLeft: 2, PlacesFound: 0},
			wantTerminal:     false,
			wantTerminalFail: false,
		},
		{
			name:             "retries remaining with success: defensive — IsTerminal must respect RetriesLeft",
			o:                SeedOutcome{Err: nil, RetriesLeft: 1, PlacesFound: 0},
			wantTerminal:     false,
			wantTerminalFail: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantTerminal, tc.o.IsTerminal(), "IsTerminal mismatch")
			assert.Equal(t, tc.wantTerminalFail, tc.o.IsTerminalFailure(), "IsTerminalFailure mismatch")
		})
	}
}
