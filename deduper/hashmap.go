package deduper

import (
	"context"
	"hash/fnv"
	"sync"
)

var _ Deduper = (*hashmap)(nil)

type hashmap struct {
	mux  sync.Mutex
	seen map[uint64]struct{}
}

func (d *hashmap) AddIfNotExists(_ context.Context, key string) bool {
	h := d.hash(key)

	d.mux.Lock()
	defer d.mux.Unlock()

	if _, ok := d.seen[h]; ok {
		return false
	}

	d.seen[h] = struct{}{}

	return true
}

func (d *hashmap) hash(key string) uint64 {
	h := fnv.New64()
	h.Write([]byte(key))

	return h.Sum64()
}
