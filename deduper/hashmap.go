package deduper

import (
	"context"
	"hash/fnv"
	"sync"
)

var _ Deduper = (*hashmap)(nil)

type hashmap struct {
	mux  *sync.RWMutex
	seen map[uint64]struct{}
}

func (d *hashmap) AddIfNotExists(_ context.Context, key string) bool {
	d.mux.RLock()
	if _, ok := d.seen[d.hash(key)]; ok {
		d.mux.RUnlock()
		return false
	}

	d.mux.RUnlock()

	d.mux.Lock()
	defer d.mux.Unlock()

	if _, ok := d.seen[d.hash(key)]; ok {
		return false
	}

	d.seen[d.hash(key)] = struct{}{}

	return true
}

func (d *hashmap) hash(key string) uint64 {
	h := fnv.New64()
	h.Write([]byte(key))

	return h.Sum64()
}
