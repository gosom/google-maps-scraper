package deduper

import (
	"context"
	"sync"
)

type Deduper interface {
	AddIfNotExists(context.Context, string) bool
}

func New() Deduper {
	return &hashmap{
		seen: make(map[uint64]struct{}),
		mux:  &sync.RWMutex{},
	}
}
