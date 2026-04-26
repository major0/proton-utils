package pool_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/major0/proton-cli/api/pool"
	"pgregory.net/rapid"
)

// TestPool_ConcurrencyLimit_Property verifies that for any limit N and
// batch of tasks, the number of concurrently active tasks never exceeds N.
func TestPool_ConcurrencyLimit_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 64).Draw(t, "workers")
		taskCount := rapid.IntRange(1, 200).Draw(t, "tasks")

		ctx := context.Background()
		p := pool.New(ctx, n)

		var peak atomic.Int64
		var active atomic.Int64
		var wg sync.WaitGroup

		for i := 0; i < taskCount; i++ {
			p.Go(&wg, func(_ context.Context) error {
				cur := active.Add(1)
				for {
					old := peak.Load()
					if cur <= old || peak.CompareAndSwap(old, cur) {
						break
					}
				}
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

// TestPool_CompletionInvariant_Property verifies that after all tasks
// complete, Completed == Submitted == len(tasks) for any batch size.
func TestPool_CompletionInvariant_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		taskCount := rapid.IntRange(0, 200).Draw(t, "tasks")
		n := rapid.IntRange(1, 64).Draw(t, "workers")

		ctx := context.Background()
		p := pool.New(ctx, n)

		var wg sync.WaitGroup
		for i := 0; i < taskCount; i++ {
			p.Go(&wg, func(_ context.Context) error {
				return nil
			})
		}

		wg.Wait()

		snap := p.Stats()
		if snap.Submitted != int64(taskCount) {
			t.Fatalf("Submitted %d, want %d", snap.Submitted, taskCount)
		}
		if snap.Completed != int64(taskCount) {
			t.Fatalf("Completed %d, want %d", snap.Completed, taskCount)
		}
		if snap.Active != 0 {
			t.Fatalf("Active %d after Wait, want 0", snap.Active)
		}
	})
}

// mockWaiter is a test Waiter that counts calls.
type mockWaiter struct {
	calls atomic.Int64
}

func (m *mockWaiter) Wait(_ context.Context) error {
	m.calls.Add(1)
	return nil
}

// TestPool_ThrottleIntegration_Property verifies that the Waiter is
// called once per task.
func TestPool_ThrottleIntegration_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		taskCount := rapid.IntRange(1, 100).Draw(t, "tasks")

		w := &mockWaiter{}
		ctx := context.Background()
		p := pool.New(ctx, 4, pool.WithThrottle(w))

		var bodyCalls atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < taskCount; i++ {
			p.Go(&wg, func(_ context.Context) error {
				bodyCalls.Add(1)
				return nil
			})
		}

		wg.Wait()

		if got := w.calls.Load(); got != int64(taskCount) {
			t.Fatalf("waiter calls %d, want %d", got, taskCount)
		}
		if got := bodyCalls.Load(); got != int64(taskCount) {
			t.Fatalf("body calls %d, want %d", got, taskCount)
		}
	})
}

// TestPool_ContextCancellation_Property verifies that when the pool
// context is cancelled, tasks see a cancelled context.
func TestPool_ContextCancellation_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		taskCount := rapid.IntRange(2, 50).Draw(t, "tasks")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		p := pool.New(ctx, 2)

		var sawCancelled atomic.Int64
		var wg sync.WaitGroup

		for i := 0; i < taskCount; i++ {
			if i == 0 {
				p.Go(&wg, func(_ context.Context) error {
					cancel()
					return nil
				})
			} else {
				p.Go(&wg, func(ctx context.Context) error {
					// Give the cancel a moment to propagate.
					time.Sleep(time.Millisecond)
					if ctx.Err() != nil {
						sawCancelled.Add(1)
					}
					return nil
				})
			}
		}

		wg.Wait()
		// At least some tasks should have seen the cancellation.
		if sawCancelled.Load() == 0 {
			t.Fatal("no tasks saw context cancellation")
		}
	})
}
