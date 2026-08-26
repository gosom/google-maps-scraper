package resume

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

const stateVersion = 1

type stateFile struct {
	Version        int      `json:"version"`
	CompletedInput []string `json:"completed_inputs"`
}

// State tracks input queries that fully completed in a resume-capable run.
type State struct {
	path      string
	mu        sync.Mutex
	completed map[string]struct{}
}

// DefaultStatePath returns the sidecar path for a results file.
func DefaultStatePath(resultsPath string) string {
	return resultsPath + ".resume.json"
}

// LoadState reads a resume sidecar. Missing files produce an empty state.
func LoadState(path string) (*State, error) {
	state := &State{
		path:      path,
		completed: make(map[string]struct{}),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}

		return nil, err
	}

	if len(data) == 0 {
		return state, nil
	}

	var file stateFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	if file.Version != stateVersion {
		return nil, fmt.Errorf("unsupported resume state version: %d", file.Version)
	}

	for _, inputID := range file.CompletedInput {
		if inputID != "" {
			state.completed[inputID] = struct{}{}
		}
	}

	return state, nil
}

// IsInputCompleted reports whether inputID has been marked complete.
func (s *State) IsInputCompleted(inputID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.completed[inputID]

	return ok
}

// MarkInputCompleted records and persists a completed input ID.
func (s *State) MarkInputCompleted(inputID string) error {
	if inputID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.completed[inputID]; ok {
		return nil
	}

	completed := make(map[string]struct{}, len(s.completed)+1)
	for completedInputID := range s.completed {
		completed[completedInputID] = struct{}{}
	}

	completed[inputID] = struct{}{}

	if err := s.saveLocked(completed); err != nil {
		return err
	}

	s.completed = completed

	return nil
}

func (s *State) saveLocked(completedSet map[string]struct{}) error {
	completed := make([]string, 0, len(completedSet))
	for inputID := range completedSet {
		completed = append(completed, inputID)
	}

	slices.Sort(completed)

	data, err := json.MarshalIndent(stateFile{
		Version:        stateVersion,
		CompletedInput: completed,
	}, "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".resume-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()

	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}
