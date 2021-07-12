package state

import (
	"sync"
)

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
