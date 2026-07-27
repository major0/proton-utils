//go:build unix

package account

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRefreshLockSerializesContendingGoroutines verifies that the unix
// advisory lock serializes contending holders: two goroutines repeatedly
// acquire the same per-UID lock, and their critical sections never overlap.
//
// flock advisory locks are associated with the open file description, and
// acquireRefreshLock opens a fresh descriptor per call, so two goroutines in
// the same process genuinely contend for LOCK_EX. Overlap detection uses
// atomic counters (rather than a lock-guarded plain int) because the Go race
// detector does not model the happens-before edge established by the flock
// syscall, so a plain guarded counter would produce spurious race reports
// under -race even when the lock is working correctly.
//
// Covers Requirement 1.1 (refresh performed under a cross-process advisory
// lock keyed by the account UID) — the file-lock leg of the testing strategy.
func TestRefreshLockSerializesContendingGoroutines(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid        = "contending-uid"
		goroutines = 2
		iterations = 50
	)

	var (
		active    int32 // holders currently inside the critical section
		maxActive int32 // high-water mark of active; must never exceed 1
		entries   int64 // total critical-section entries (correctness check)
		wg        sync.WaitGroup
	)

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				lock, err := acquireRefreshLock(uid)
				if err != nil {
					t.Errorf("acquireRefreshLock: %v", err)
					return
				}

				// Critical section. Record the peak number of concurrent
				// holders; if the lock fails to serialize, the small sleep
				// widens the window so an overlap is observed as maxActive > 1.
				n := atomic.AddInt32(&active, 1)
				for {
					m := atomic.LoadInt32(&maxActive)
					if n <= m || atomic.CompareAndSwapInt32(&maxActive, m, n) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt64(&entries, 1)
				atomic.AddInt32(&active, -1)

				lock.release()
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("critical sections overlapped: max concurrent holders = %d, want 1", got)
	}
	if got, want := atomic.LoadInt64(&entries), int64(goroutines*iterations); got != want {
		t.Fatalf("critical-section entries = %d, want %d", got, want)
	}
}
