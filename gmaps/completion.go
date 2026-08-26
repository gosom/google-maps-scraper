package gmaps

// CompletionTracker receives per-input job completion signals.
type CompletionTracker interface {
	SeedDiscovered(inputID string, placesFound int) error
}
