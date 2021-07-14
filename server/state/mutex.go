package state

import (
	"sync"
)

// RingMutex attempts to shard mutexes across a predefined number of mutexes, to
// allow for a disjoint sets of keys to to obtain locks without guarding the
// entire map. Note: this is best to be used with sync.Map, and the size
// is best to be a prime number to reduce collisions.
type RingMutex struct {
	locks []sync.RWMutex
	size  uint64
}

type Unlocker func()

func NewRing(size uint64) *RingMutex {
	return &RingMutex{
		size:  size,
		locks: make([]sync.RWMutex, size),
	}
}

func (r *RingMutex) Lock(key uint64) Unlocker {
	r.locks[key%r.size].Lock()
	return r.locks[key%r.size].Unlock
}

func (r *RingMutex) RLock(key uint64) Unlocker {
	r.locks[key%r.size].RLock()
	return r.locks[key%r.size].RUnlock
}
