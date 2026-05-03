package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestSemaphore_ConcurrencyBound_Property verifies that for any
// concurrency limit N and any number of concurrent task submissions,
// the number of simultaneously active tasks never exceeds N.
//
// **Validates: Requirements 2.1**
func TestSemaphore_ConcurrencyBound_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 128).Draw(t, "limit")
		taskCount := rapid.IntRange(1, 500).Draw(t, "tasks")

		ctx := context.Background()
		sem := NewSemaphore(ctx, n, nil)

		var peak atomic.Int64
		var active atomic.Int64
		var wg sync.WaitGroup

		for i := 0; i < taskCount; i++ {
			sem.Go(&wg, func(_ context.Context) error {
				cur := active.Add(1)
				// Atomically update peak using CAS loop.
				for {
					old := peak.Load()
					if cur <= old || peak.CompareAndSwap(old, cur) {
						break
					}
				}
				// Small sleep to increase overlap between tasks.
				time.Sleep(time.Microsecond)
				active.Add(-1)
				return nil
			})
		}

		wg.Wait()

		if got := peak.Load(); got > int64(n) {
			t.Fatalf("peak active %d exceeded limit %d", got, n)
		}
	})
}
