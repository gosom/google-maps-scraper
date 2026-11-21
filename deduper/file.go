package deduper

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"regexp"
	"strings"
	"sync"
)

// maxKeyLineBytes bounds a single line when loading a dedup file. Place URLs
// are a few hundred bytes; this leaves ample room for long data= segments.
const maxKeyLineBytes = 1024 * 1024

var (
	// placeDataIDRe finds the feature ID ("!1s0x...:0x...") inside a
	// /maps/place/ URL.
	placeDataIDRe = regexp.MustCompile(`!1s(0x[0-9a-fA-F]+:0x[0-9a-fA-F]+)`)
	// bareDataIDRe matches a feature ID on its own, e.g. a data_id column.
	bareDataIDRe = regexp.MustCompile(`^0x[0-9a-fA-F]+:0x[0-9a-fA-F]+$`)
)

// placeKey reduces a place URL to an identity that stays stable across days.
// Discovered URLs carry volatile query parameters (g_ep encodes the Maps
// build date), so the same place gets a different URL on a later run. The
// feature ID is used when present, otherwise the URL without its query.
func placeKey(key string) string {
	key = strings.TrimSpace(key)
	if bareDataIDRe.MatchString(key) {
		return strings.ToLower(key)
	}

	if m := placeDataIDRe.FindStringSubmatch(key); m != nil {
		return strings.ToLower(m[1])
	}

	if i := strings.IndexByte(key, '?'); i >= 0 {
		return key[:i]
	}

	return key
}

func hashKey(key string) uint64 {
	h := fnv.New64()
	h.Write([]byte(key))

	return h.Sum64()
}

// FileDeduper holds the places that completed jobs have already returned,
// backed by a file so the set survives restarts. Jobs dedup through a
// JobDeduper from NewJob; their places join the shared set only when the job
// commits, so an interrupted job is scraped in full when it runs again.
type FileDeduper struct {
	mux  sync.RWMutex
	seen map[uint64]struct{}
	file *os.File
}

// NewFile opens (or creates) path and loads the keys it lists, one per line.
// Lines may be place URLs or bare feature IDs.
func NewFile(path string) (*FileDeduper, error) {
	d := &FileDeduper{seen: make(map[uint64]struct{})}

	if err := d.load(path); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open dedup file: %w", err)
	}

	d.file = f

	return d, nil
}

func (d *FileDeduper) load(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("open dedup file: %w", err)
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxKeyLineBytes)

	for scanner.Scan() {
		if key := placeKey(scanner.Text()); key != "" {
			d.seen[hashKey(key)] = struct{}{}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dedup file: %w", err)
	}

	return nil
}

func (d *FileDeduper) has(key string) bool {
	d.mux.RLock()
	defer d.mux.RUnlock()

	_, ok := d.seen[hashKey(key)]

	return ok
}

func (d *FileDeduper) commit(keys []string) error {
	d.mux.Lock()
	defer d.mux.Unlock()

	var b strings.Builder

	for _, key := range keys {
		h := hashKey(key)
		if _, ok := d.seen[h]; ok {
			continue
		}

		d.seen[h] = struct{}{}

		b.WriteString(key)
		b.WriteByte('\n')
	}

	if b.Len() == 0 {
		return nil
	}

	if _, err := d.file.WriteString(b.String()); err != nil {
		return fmt.Errorf("append dedup file: %w", err)
	}

	return nil
}

// NewJob returns a Deduper for one job. It skips places already in the shared
// set and places seen earlier in the same job.
func (d *FileDeduper) NewJob() *JobDeduper {
	return &JobDeduper{
		parent: d,
		local:  New(),
	}
}

// Close closes the underlying file.
func (d *FileDeduper) Close() error {
	d.mux.Lock()
	defer d.mux.Unlock()

	return d.file.Close()
}

var _ Deduper = (*JobDeduper)(nil)

// JobDeduper dedups the places of a single job against the shared set of a
// FileDeduper. Its own places stay private until Commit.
type JobDeduper struct {
	parent *FileDeduper
	local  Deduper

	mux  sync.Mutex
	keys []string
}

// AddIfNotExists reports whether key is new to both the shared set and this job.
func (j *JobDeduper) AddIfNotExists(ctx context.Context, key string) bool {
	key = placeKey(key)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return j.local.AddIfNotExists(ctx, key)
	}

	if j.parent.has(key) || !j.local.AddIfNotExists(ctx, key) {
		return false
	}

	j.mux.Lock()
	j.keys = append(j.keys, key)
	j.mux.Unlock()

	return true
}

// Commit adds the job's places to the shared set and appends them to the
// file. Call it only once the job's results are safely stored.
func (j *JobDeduper) Commit() error {
	j.mux.Lock()
	keys := j.keys
	j.keys = nil
	j.mux.Unlock()

	return j.parent.commit(keys)
}
