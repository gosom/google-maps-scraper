package resume

import "sync"

// InputMarker persists fully completed input IDs.
type InputMarker interface {
	MarkInputCompleted(inputID string) error
}

type inputProgress struct {
	seedDone        bool
	placesFound     int
	placesCompleted int
	marked          bool
}

// ProgressTracker tracks per-input discovery and child job completion.
type ProgressTracker struct {
	mu       sync.Mutex
	marker   InputMarker
	progress map[string]*inputProgress
}

// NewProgressTracker creates a per-input completion tracker.
func NewProgressTracker(marker InputMarker) *ProgressTracker {
	return &ProgressTracker{
		marker:   marker,
		progress: make(map[string]*inputProgress),
	}
}

// SeedDiscovered records that an input's search job finished discovery.
func (t *ProgressTracker) SeedDiscovered(inputID string, placesFound int) error {
	if inputID == "" {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	progress := t.getLocked(inputID)
	progress.seedDone = true
	progress.placesFound += placesFound

	return t.maybeMarkLocked(inputID, progress)
}

// ResultPersisted records that one output-producing child was durably handled.
func (t *ProgressTracker) ResultPersisted(inputID string, syncOutput func() error) error {
	if inputID == "" {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	progress := t.getLocked(inputID)
	progress.placesCompleted++

	return t.maybeMarkLocked(inputID, progress, syncOutput)
}

func (t *ProgressTracker) getLocked(inputID string) *inputProgress {
	progress, ok := t.progress[inputID]
	if ok {
		return progress
	}

	progress = &inputProgress{}
	t.progress[inputID] = progress

	return progress
}

func (t *ProgressTracker) maybeMarkLocked(inputID string, progress *inputProgress, syncOutput ...func() error) error {
	if progress.marked || !progress.seedDone || progress.placesCompleted < progress.placesFound {
		return nil
	}

	if len(syncOutput) > 0 && syncOutput[0] != nil {
		if err := syncOutput[0](); err != nil {
			return err
		}
	}

	if t.marker == nil {
		progress.marked = true
		return nil
	}

	if err := t.marker.MarkInputCompleted(inputID); err != nil {
		return err
	}

	progress.marked = true

	return nil
}
