package drive

import (
	"container/list"
	"sync"
)

// slotState tracks the lifecycle of a buffer cache slot.
type slotState int

const (
	slotEmpty    slotState = iota
	slotFetching           // fetch in progress, waiters block on ready
	slotClean              // encrypted data available
)

// cacheKey identifies a block in the buffer cache.
type cacheKey struct {
	linkID string
	index  int
}

// cacheSlot holds one encrypted block in the buffer cache.
type cacheSlot struct {
	key   cacheKey
	data  []byte        // encrypted block data (nil until clean)
	ready chan struct{} // closed when state transitions to clean/error
	err   error         // fetch error (nil on success)
	elem  *list.Element // position in LRU list
	state slotState
}

// bufferCache is a fixed-size in-memory cache of encrypted block data.
// Slots are keyed by (linkID, blockIndex) for O(1) lookup. LRU eviction
// targets clean slots only — fetching slots are never evicted.
type bufferCache struct {
	mu    sync.Mutex
	slots map[cacheKey]*cacheSlot
	lru   *list.List // *cacheSlot elements, most-recent at front
	cap   int        // max slots
}

// newBufferCache creates a buffer cache with the given capacity.
// Capacity is the maximum number of slots (each holds one 4 MB block).
func newBufferCache(capacity int) *bufferCache {
	return &bufferCache{
		slots: make(map[cacheKey]*cacheSlot, capacity),
		lru:   list.New(),
		cap:   capacity,
	}
}

// Get returns cached data for the given block. If the slot is clean,
// returns the data immediately and touches the LRU entry. If the slot
// is fetching, releases the lock and blocks on the ready channel. Returns
// nil, nil on cache miss (slot doesn't exist).
func (bc *bufferCache) Get(linkID string, index int) ([]byte, error) {
	k := cacheKey{linkID: linkID, index: index}

	bc.mu.Lock()
	slot, ok := bc.slots[k]
	if !ok {
		bc.mu.Unlock()
		return nil, nil
	}

	switch slot.state {
	case slotClean:
		// Touch: move to front of LRU.
		bc.lru.MoveToFront(slot.elem)
		data, err := slot.data, slot.err
		bc.mu.Unlock()
		return data, err

	case slotFetching:
		// Release lock before blocking — other goroutines need access.
		ready := slot.ready
		bc.mu.Unlock()
		<-ready
		// Slot is now clean or errored. Re-read under lock for the
		// LRU touch.
		bc.mu.Lock()
		// Slot may have been evicted/invalidated while we waited.
		slot, ok = bc.slots[k]
		if !ok {
			bc.mu.Unlock()
			return nil, nil
		}
		if slot.state == slotClean {
			bc.lru.MoveToFront(slot.elem)
		}
		data, err := slot.data, slot.err
		bc.mu.Unlock()
		return data, err

	default:
		// slotEmpty — shouldn't be in the map, treat as miss.
		bc.mu.Unlock()
		return nil, nil
	}
}

// Put stores encrypted block data in the cache. If the slot already
// exists (from Reserve), it transitions to clean and wakes waiters.
// If the slot doesn't exist, creates it directly as clean.
func (bc *bufferCache) Put(linkID string, index int, data []byte) {
	k := cacheKey{linkID: linkID, index: index}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	slot, ok := bc.slots[k]
	if ok {
		// Existing slot — transition to clean.
		slot.data = data
		slot.err = nil
		slot.state = slotClean
		if slot.elem == nil {
			slot.elem = bc.lru.PushFront(slot)
		} else {
			bc.lru.MoveToFront(slot.elem)
		}
		// Wake all waiters.
		select {
		case <-slot.ready:
			// Already closed.
		default:
			close(slot.ready)
		}
		return
	}

	// New slot — evict if needed, then insert as clean.
	if len(bc.slots) >= bc.cap {
		bc.evictLocked()
	}

	slot = &cacheSlot{
		key:   k,
		data:  data,
		ready: make(chan struct{}),
		state: slotClean,
	}
	close(slot.ready) // immediately available
	slot.elem = bc.lru.PushFront(slot)
	bc.slots[k] = slot
}

// PutError records a fetch error on a fetching slot and wakes waiters.
// Subsequent Get calls will return (nil, err). The slot is removed from
// the cache so a future Reserve can retry.
func (bc *bufferCache) PutError(linkID string, index int, err error) {
	k := cacheKey{linkID: linkID, index: index}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	slot, ok := bc.slots[k]
	if !ok {
		return
	}

	slot.data = nil
	slot.err = err
	slot.state = slotClean
	// Remove the failed slot so future requests can retry.
	delete(bc.slots, k)
	// Wake all waiters.
	select {
	case <-slot.ready:
	default:
		close(slot.ready)
	}
}

// Reserve creates a fetching slot if absent. Returns true if a new slot
// was created (caller should fetch the block). Returns false if the slot
// already exists (fetching or clean — don't fetch again) or if the cache
// is full and all slots are fetching.
func (bc *bufferCache) Reserve(linkID string, index int) bool {
	k := cacheKey{linkID: linkID, index: index}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	if _, ok := bc.slots[k]; ok {
		return false
	}

	// Need a free slot. If at capacity, try to evict a clean slot.
	if len(bc.slots) >= bc.cap {
		if !bc.evictLocked() {
			// All slots are fetching — back off.
			return false
		}
	}

	slot := &cacheSlot{
		key:   k,
		ready: make(chan struct{}),
		state: slotFetching,
	}
	// Fetching slots are NOT in the LRU list — they can't be evicted.
	bc.slots[k] = slot
	return true
}

// Invalidate removes all slots for a given linkID. Used when a file is
// overwritten with a new revision.
func (bc *bufferCache) Invalidate(linkID string) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	for k, slot := range bc.slots {
		if k.linkID != linkID {
			continue
		}
		if slot.elem != nil {
			bc.lru.Remove(slot.elem)
		}
		delete(bc.slots, k)
		// Wake any waiters so they don't block forever.
		select {
		case <-slot.ready:
		default:
			close(slot.ready)
		}
	}
}

// evictLocked removes the least-recently-used slot. Returns true if a
// slot was evicted, false if the LRU list is empty. Caller must hold
// bc.mu.
//
// Only clean slots are in the LRU list — fetching slots have elem==nil
// and are never added to the list (see Reserve). So the tail is always
// safe to evict without a state check.
func (bc *bufferCache) evictLocked() bool {
	e := bc.lru.Back()
	if e == nil {
		return false
	}
	slot := e.Value.(*cacheSlot)
	bc.lru.Remove(e)
	delete(bc.slots, slot.key)
	return true
}
