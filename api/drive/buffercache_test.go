package drive

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Property 10: Cache key correctness
// Get(k) returns exactly what was stored via Put(k, data).
// Get for a key not inserted returns nil, nil.
// **Validates: Requirements 3.2**
// ---------------------------------------------------------------------------

func TestPropertyCacheKeyCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bc := newBufferCache(64)

		type entry struct {
			linkID string
			index  int
			data   []byte
		}

		// Generate a random set of insertions.
		n := rapid.IntRange(1, 50).Draw(t, "numInsertions")
		inserted := make(map[cacheKey][]byte, n)
		for i := 0; i < n; i++ {
			linkID := rapid.StringMatching(`[a-zA-Z0-9]{4,16}`).Draw(t, fmt.Sprintf("linkID_%d", i))
			index := rapid.IntRange(0, 100).Draw(t, fmt.Sprintf("index_%d", i))
			data := rapid.SliceOfN(rapid.Byte(), 1, 128).Draw(t, fmt.Sprintf("data_%d", i))

			bc.Put(linkID, index, data)
			inserted[cacheKey{linkID: linkID, index: index}] = data
		}

		// Every inserted key must return the stored data.
		for k, want := range inserted {
			got, err := bc.Get(k.linkID, k.index)
			if err != nil {
				t.Fatalf("Get(%q, %d) error: %v", k.linkID, k.index, err)
			}
			if len(got) != len(want) {
				t.Fatalf("Get(%q, %d) len=%d, want %d", k.linkID, k.index, len(got), len(want))
			}
			for j := range want {
				if got[j] != want[j] {
					t.Fatalf("Get(%q, %d)[%d]=%d, want %d", k.linkID, k.index, j, got[j], want[j])
				}
			}
		}

		// A key that was never inserted must return nil, nil.
		missLinkID := "never-inserted-link"
		missIndex := 9999
		got, err := bc.Get(missLinkID, missIndex)
		if got != nil || err != nil {
			t.Fatalf("Get(miss) = (%v, %v), want (nil, nil)", got, err)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 11: Cache state transitions
// Slots only transition empty → fetching → clean (via Reserve then Put).
// **Validates: Requirements 3.4**
// ---------------------------------------------------------------------------

func TestPropertyCacheStateTransitions(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		capacity := rapid.IntRange(2, 32).Draw(t, "capacity")
		bc := newBufferCache(capacity)

		type slotInfo struct {
			reserved bool // slot has been reserved (fetching)
			filled   bool // slot has been filled (clean)
		}
		tracker := make(map[cacheKey]*slotInfo)

		// Helper: mark evicted slots as absent in the tracker. When a
		// new Put causes eviction, the LRU clean slot is removed. We
		// mirror this by checking which tracked keys are still in the
		// cache after each operation.
		syncTracker := func() {
			for k, info := range tracker {
				if !info.filled {
					continue // fetching slots aren't evicted
				}
				got, _ := bc.Get(k.linkID, k.index)
				if got == nil {
					// Slot was evicted — reset tracker.
					info.filled = false
					info.reserved = false
				}
			}
		}

		ops := rapid.IntRange(1, 100).Draw(t, "numOps")
		for i := 0; i < ops; i++ {
			linkID := rapid.StringMatching(`[a-z]{2,6}`).Draw(t, fmt.Sprintf("link_%d", i))
			index := rapid.IntRange(0, 20).Draw(t, fmt.Sprintf("idx_%d", i))
			k := cacheKey{linkID: linkID, index: index}

			// Randomly choose: Reserve or Put.
			doReserve := rapid.Bool().Draw(t, fmt.Sprintf("reserve_%d", i))

			info := tracker[k]
			if info == nil {
				info = &slotInfo{}
				tracker[k] = info
			}

			if doReserve {
				isNew := bc.Reserve(linkID, index)
				if info.reserved || info.filled {
					// Already reserved or filled — Reserve must return false.
					if isNew {
						t.Fatalf("Reserve(%q,%d) returned true but slot already exists", linkID, index)
					}
				} else if isNew {
					// Transition: empty → fetching.
					info.reserved = true

					// While fetching, Reserve returns false.
					if bc.Reserve(linkID, index) {
						t.Fatalf("double Reserve(%q,%d) returned true", linkID, index)
					}
				}
				// isNew==false when cache is full of fetching slots — valid.
			} else {
				data := []byte{byte(i)}
				bc.Put(linkID, index, data)
				info.filled = true
				info.reserved = true

				// Put may have evicted another clean slot — sync tracker.
				syncTracker()

				// After Put, Get must return the data (clean state).
				got, err := bc.Get(linkID, index)
				if err != nil {
					t.Fatalf("Get after Put(%q,%d): %v", linkID, index, err)
				}
				if len(got) != 1 || got[0] != byte(i) {
					t.Fatalf("Get after Put(%q,%d) = %v, want [%d]", linkID, index, got, byte(i))
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 12: LRU evicts least-recent clean
// When cache is full and a new Reserve is needed, the evicted slot is the
// least-recently-accessed clean slot. Fetching slots are never evicted.
// **Validates: Requirements 3.4, 3.5**
// ---------------------------------------------------------------------------

func TestPropertyLRUEvictsLeastRecentClean(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		capacity := rapid.IntRange(3, 10).Draw(t, "capacity")
		bc := newBufferCache(capacity)

		// Fill the cache with clean slots.
		for i := 0; i < capacity; i++ {
			bc.Put("fill", i, []byte{byte(i)})
		}

		// Touch all except slot 0 (the oldest) to make slot 0 LRU.
		for i := 1; i < capacity; i++ {
			_, _ = bc.Get("fill", i)
		}

		// Reserve a new slot — should evict slot 0 (least-recent clean).
		if !bc.Reserve("new", 0) {
			t.Fatal("Reserve(new,0) should succeed by evicting LRU clean slot")
		}

		// Slot 0 should be gone.
		got, err := bc.Get("fill", 0)
		if got != nil || err != nil {
			t.Fatalf("evicted slot still present: (%v, %v)", got, err)
		}

		// All other slots should still be present.
		for i := 1; i < capacity; i++ {
			got, err := bc.Get("fill", i)
			if got == nil {
				t.Fatalf("slot %d was evicted but shouldn't have been (err=%v)", i, err)
			}
		}
	})
}

func TestPropertyFetchingSlotsNeverEvicted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		capacity := rapid.IntRange(2, 8).Draw(t, "capacity")
		bc := newBufferCache(capacity)

		// Fill cache: first slot is fetching, rest are clean.
		bc.Reserve("fetching", 0) // fetching slot
		for i := 1; i < capacity; i++ {
			bc.Put("clean", i, []byte{byte(i)})
		}

		// Reserve a new slot — must evict a clean slot, not the fetching one.
		if !bc.Reserve("new", 99) {
			t.Fatal("Reserve should succeed — there are clean slots to evict")
		}

		// The fetching slot must still be present (Reserve returns false).
		if bc.Reserve("fetching", 0) {
			t.Fatal("fetching slot was evicted — fetching slots must never be evicted")
		}
	})
}

func TestPropertyAllFetchingBlocksReserve(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		capacity := rapid.IntRange(2, 8).Draw(t, "capacity")
		bc := newBufferCache(capacity)

		// Fill entire cache with fetching slots.
		for i := 0; i < capacity; i++ {
			if !bc.Reserve("fetching", i) {
				t.Fatalf("Reserve(fetching,%d) failed during fill", i)
			}
		}

		// New Reserve must fail — no clean slots to evict.
		if bc.Reserve("overflow", 0) {
			t.Fatal("Reserve should fail when all slots are fetching")
		}
	})
}

// ---------------------------------------------------------------------------
// Unit tests for bufferCache (Task 3.4)
// ---------------------------------------------------------------------------

// TestBufferCacheConcurrentGetPut exercises concurrent Get/Put from multiple
// goroutines to verify thread safety.
func TestBufferCacheConcurrentGetPut(t *testing.T) {
	const goroutines = 16
	const opsPerGoroutine = 100
	// Each goroutine uses a unique linkID and indices 0..99, so up to
	// goroutines * opsPerGoroutine unique keys. Size the cache to fit.
	bc := newBufferCache(goroutines * opsPerGoroutine)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			linkID := fmt.Sprintf("link-%d", id)
			for i := 0; i < opsPerGoroutine; i++ {
				data := []byte{byte(id & 0xFF), byte(i & 0xFF)} //nolint:gosec // bounded by mask
				bc.Put(linkID, i, data)

				got, err := bc.Get(linkID, i)
				if err != nil {
					t.Errorf("goroutine %d: Get(%s,%d): %v", id, linkID, i, err)
					return
				}
				if got == nil {
					t.Errorf("goroutine %d: Get(%s,%d) = nil", id, linkID, i)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// TestBufferCacheReserveDuplicate verifies Reserve returns false for
// duplicate reservations.
func TestBufferCacheReserveDuplicate(t *testing.T) {
	bc := newBufferCache(8)

	if !bc.Reserve("link", 0) {
		t.Fatal("first Reserve should return true")
	}
	if bc.Reserve("link", 0) {
		t.Fatal("duplicate Reserve should return false")
	}

	// After Put, Reserve should still return false (slot is clean).
	bc.Put("link", 0, []byte("data"))
	if bc.Reserve("link", 0) {
		t.Fatal("Reserve on clean slot should return false")
	}
}

// TestBufferCacheInvalidate verifies Invalidate removes all slots for a linkID.
func TestBufferCacheInvalidate(t *testing.T) {
	bc := newBufferCache(64)

	// Insert multiple blocks for two linkIDs.
	for i := 0; i < 5; i++ {
		bc.Put("link-a", i, []byte{byte(i)})
		bc.Put("link-b", i, []byte{byte(i + 10)})
	}

	bc.Invalidate("link-a")

	// All link-a slots should be gone.
	for i := 0; i < 5; i++ {
		got, err := bc.Get("link-a", i)
		if got != nil || err != nil {
			t.Fatalf("link-a slot %d still present after Invalidate", i)
		}
	}

	// link-b slots should be untouched.
	for i := 0; i < 5; i++ {
		got, err := bc.Get("link-b", i)
		if got == nil {
			t.Fatalf("link-b slot %d missing (err=%v)", i, err)
		}
	}
}

// TestBufferCacheCapacityEviction verifies capacity enforcement and eviction.
func TestBufferCacheCapacityEviction(t *testing.T) {
	const capacity = 4
	bc := newBufferCache(capacity)

	// Fill to capacity.
	for i := 0; i < capacity; i++ {
		bc.Put("link", i, []byte{byte(i)})
	}

	// Insert one more — should evict the LRU slot (index 0).
	bc.Put("link", capacity, []byte{byte(capacity)})

	// Slot 0 should be evicted.
	got, _ := bc.Get("link", 0)
	if got != nil {
		t.Fatal("slot 0 should have been evicted")
	}

	// Slots 1..capacity should still be present.
	for i := 1; i <= capacity; i++ {
		got, err := bc.Get("link", i)
		if got == nil {
			t.Fatalf("slot %d missing after eviction (err=%v)", i, err)
		}
	}

	// Total slots should not exceed capacity.
	bc.mu.Lock()
	count := len(bc.slots)
	bc.mu.Unlock()
	if count > capacity {
		t.Fatalf("slot count %d exceeds capacity %d", count, capacity)
	}
}

// TestBufferCacheGetBlocksOnFetching verifies that Get blocks on a fetching
// slot until Put completes.
func TestBufferCacheGetBlocksOnFetching(t *testing.T) {
	bc := newBufferCache(8)

	if !bc.Reserve("link", 0) {
		t.Fatal("Reserve should succeed")
	}

	done := make(chan struct{})
	var got []byte
	var getErr error

	go func() {
		got, getErr = bc.Get("link", 0)
		close(done)
	}()

	// Give the goroutine time to block on the fetching slot.
	time.Sleep(50 * time.Millisecond)

	select {
	case <-done:
		t.Fatal("Get returned before Put — should have blocked")
	default:
		// Good — still blocking.
	}

	// Complete the fetch.
	want := []byte("fetched-data")
	bc.Put("link", 0, want)

	select {
	case <-done:
		// Good — unblocked.
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not unblock after Put")
	}

	if getErr != nil {
		t.Fatalf("Get error: %v", getErr)
	}
	if string(got) != string(want) {
		t.Fatalf("Get = %q, want %q", got, want)
	}
}

// TestBufferCacheReserveFailsWhenAllFetching verifies Reserve returns false
// when the cache is full and all slots are in fetching state.
func TestBufferCacheReserveFailsWhenAllFetching(t *testing.T) {
	const capacity = 4
	bc := newBufferCache(capacity)

	// Fill with fetching slots.
	for i := 0; i < capacity; i++ {
		if !bc.Reserve("link", i) {
			t.Fatalf("Reserve(%d) should succeed during fill", i)
		}
	}

	// New Reserve must fail.
	if bc.Reserve("other", 0) {
		t.Fatal("Reserve should fail when all slots are fetching")
	}
}
