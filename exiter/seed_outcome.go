package exiter

// SeedOutcome is the typed event recorded once per terminal seed-job
// completion (success or final failure after retries). It carries enough
// signal for the exit monitor's isDone() to distinguish "every seed
// terminally failed → fail fast" from "every seed succeeded but produced
// no places → grace period for slow page renders".
//
// Callers (gmaps.GmapJob.Process) construct SeedOutcome inline — no
// constructor needed; zero-value is a valid "successful seed that found
// zero places" event.
type SeedOutcome struct {
	// Err is the seed-level error if the seed terminally failed (proxy
	// down, navigation error, retry-exhausted). Nil on success.
	Err error
	// RetriesLeft signals whether scrapemate may still retry this seed.
	// Always 0 in the current code (Process is only called after retries
	// are exhausted), but kept as a field so future callers can record
	// non-terminal failures without polluting LastSeedError.
	RetriesLeft int
	// PlacesFound is how many place-jobs this seed produced. 0 means the
	// seed succeeded but the search returned nothing — legit on rare
	// queries; signals to isDone() to grace.
	PlacesFound int
}

// IsTerminal reports whether this outcome means the seed is permanently
// done — either failed with no retries left, or succeeded.
func (s SeedOutcome) IsTerminal() bool {
	return s.RetriesLeft == 0
}

// IsTerminalFailure reports whether this seed terminally failed (no more
// retries) AND produced no usable result. Used by isDone() to fail-fast
// when every seed in the run has terminally failed.
func (s SeedOutcome) IsTerminalFailure() bool {
	return s.IsTerminal() && s.Err != nil && s.PlacesFound == 0
}
