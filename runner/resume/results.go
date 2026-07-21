package resume

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/gosom/google-maps-scraper/gmaps"
)

// IdentitySet tracks stable place identities already emitted to a result file.
type IdentitySet struct {
	mu   sync.RWMutex
	keys map[string]struct{}
}

// NewIdentitySet creates an empty result identity set.
func NewIdentitySet() *IdentitySet {
	return &IdentitySet{
		keys: make(map[string]struct{}),
	}
}

// Add records a stable identity key. Empty keys are ignored.
func (s *IdentitySet) Add(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.keys[key] = struct{}{}
}

// Has reports whether key has already been recorded.
func (s *IdentitySet) Has(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.keys[key]

	return ok
}

// Clone returns an independent copy of the identity set.
func (s *IdentitySet) Clone() *IdentitySet {
	clone := NewIdentitySet()

	s.mu.RLock()
	defer s.mu.RUnlock()

	for key := range s.keys {
		clone.keys[key] = struct{}{}
	}

	return clone
}

// HasEntry reports whether any stable identity for entry is already recorded.
func (s *IdentitySet) HasEntry(entry *gmaps.Entry) bool {
	if entry == nil {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, key := range entryIdentities(entry) {
		if _, ok := s.keys[key]; ok {
			return true
		}
	}

	return false
}

// AddIfNotExists implements deduper.Deduper for discovered place URLs.
func (s *IdentitySet) AddIfNotExists(_ context.Context, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.keys[key]; ok {
		return false
	}

	s.keys[key] = struct{}{}

	return true
}

// AddEntry records all stable identities for entry.
func (s *IdentitySet) AddEntry(entry *gmaps.Entry) {
	if entry == nil {
		return
	}

	for _, key := range entryIdentities(entry) {
		s.Add(key)
	}
}

func entryIdentities(entry *gmaps.Entry) []string {
	values := []string{entry.PlaceID, entry.Cid, entry.DataID, entry.Link}
	identities := make([]string, 0, len(values))

	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			identities = append(identities, value)
		}
	}

	return identities
}

// EntryIdentity returns the preferred stable identity for entry.
func EntryIdentity(entry *gmaps.Entry) string {
	if entry == nil {
		return ""
	}

	return firstNonEmpty(entry.PlaceID, entry.Cid, entry.DataID, entry.Link)
}

// LoadResultIdentities reads existing CSV or JSONL results into an IdentitySet.
func LoadResultIdentities(path string, jsonl bool) (*IdentitySet, error) {
	ids, err := loadResultIdentities(path, jsonl)
	if err == nil || !hasIncompleteTrailingRecord(path) {
		return ids, err
	}

	if err := truncateIncompleteTrailingRecord(path); err != nil {
		return nil, fmt.Errorf("repair incomplete result record: %w", err)
	}

	return loadResultIdentities(path, jsonl)
}

func loadResultIdentities(path string, jsonl bool) (*IdentitySet, error) {
	ids := NewIdentitySet()

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ids, nil
		}

		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if info.Size() == 0 {
		return ids, nil
	}

	if jsonl {
		return ids, loadJSONLIdentities(f, ids)
	}

	return ids, loadCSVIdentities(f, ids)
}

func hasIncompleteTrailingRecord(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return false
	}

	return data[len(data)-1] != '\n'
}

func truncateIncompleteTrailingRecord(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lastNewline := strings.LastIndexByte(string(data), '\n')
	if lastNewline < 0 {
		lastNewline = 0
	} else {
		lastNewline++
	}

	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}

	if err := file.Truncate(int64(lastNewline)); err != nil {
		_ = file.Close()
		return err
	}

	return file.Close()
}

func loadJSONLIdentities(r io.Reader, ids *IdentitySet) error {
	dec := json.NewDecoder(r)

	for {
		var entry gmaps.Entry
		if err := dec.Decode(&entry); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("parse JSONL result: %w", err)
		}

		ids.AddEntry(&entry)
	}

	return nil
}

func loadCSVIdentities(r io.Reader, ids *IdentitySet) error {
	reader := csv.NewReader(r)

	headers, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}

		return fmt.Errorf("read CSV header: %w", err)
	}

	indexes := map[string]int{}
	for i, header := range headers {
		indexes[strings.TrimSpace(header)] = i
	}

	for {
		row, err := reader.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("read CSV row: %w", err)
		}

		addCSVValues(ids, row, indexes, "place_id", "cid", "data_id", "link")
	}

	return nil
}

func addCSVValues(ids *IdentitySet, row []string, indexes map[string]int, names ...string) {
	for _, name := range names {
		index, ok := indexes[name]
		if !ok || index >= len(row) {
			continue
		}

		ids.Add(row[index])
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}

	return ""
}
