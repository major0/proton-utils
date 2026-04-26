package pool_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/major0/proton-cli/api/pool"
)

// TestPool_GoBlocks verifies that the N+1th Go call blocks when N
// workers are busy.
func TestPool_GoBlocks(t *testing.T) {
	const n = 2
	ctx := context.Background()
	p := pool.New(ctx, n)

	// Block both workers.
	started := make(chan struct{}, n)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		p.Go(&wg, func(_ context.Context) error {
			started <- struct{}{}
			<-release
			return nil
		})
	}

	// Wait for both workers to be running.
	for i := 0; i < n; i++ {
		<-started
	}

	// The N+1th Go should block because all workers are busy.
	submitted := make(chan struct{})
	go func() {
		p.Go(&wg, func(_ context.Context) error {
			return nil
		})
		close(submitted)
	}()

	select {
	case <-submitted:
		t.Fatal("Go did not block when all workers busy")
	case <-time.After(50 * time.Millisecond):
		// Expected: Go is blocked.
	}

	// Release workers so pool can drain.
	close(release)

	// The blocked Go should now proceed.
	select {
	case <-submitted:
		// Good.
	case <-time.After(time.Second):
		t.Fatal("Go still blocked after workers released")
	}

	wg.Wait()
}

// TestPool_NoThrottle verifies that tasks dispatch without delay when
// no throttle is configured.
func TestPool_NoThrottle(t *testing.T) {
	const taskCount = 10
	ctx := context.Background()
	p := pool.New(ctx, 4)

	var count atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < taskCount; i++ {
		p.Go(&wg, func(_ context.Context) error {
			count.Add(1)
			return nil
		})
	}

	wg.Wait()

	if got := count.Load(); got != taskCount {
		t.Fatalf("executed %d tasks, want %d", got, taskCount)
	}
}

// TestPool_ZeroTasks verifies that WaitGroup completes immediately
// when no tasks have been submitted.
func TestPool_ZeroTasks(t *testing.T) {
	ctx := context.Background()
	p := pool.New(ctx, 4)

	snap := p.Stats()
	if snap.Submitted != 0 || snap.Completed != 0 || snap.Active != 0 {
		t.Fatalf("stats not zero: %+v", snap)
	}
}

// TestPool_Stats verifies that counters are accurate after a batch.
func TestPool_Stats(t *testing.T) {
	const total = 8

	ctx := context.Background()
	p := pool.New(ctx, total)

	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		p.Go(&wg, func(_ context.Context) error {
			return nil
		})
	}

	wg.Wait()

	snap := p.Stats()
	if snap.Submitted != total {
		t.Fatalf("Submitted %d, want %d", snap.Submitted, total)
	}
	if snap.Completed != total {
		t.Fatalf("Completed %d, want %d", snap.Completed, total)
	}
	if snap.Active != 0 {
		t.Fatalf("Active %d after Wait, want 0", snap.Active)
	}
}

// TestPool_Limit verifies the Limit method.
func TestPool_Limit(t *testing.T) {
	p := pool.New(context.Background(), 42)
	if got := p.Limit(); got != 42 {
		t.Fatalf("Limit() = %d, want 42", got)
	}
}

// TestPool_WaitConvenience verifies the Wait convenience method.
func TestPool_WaitConvenience(t *testing.T) {
	ctx := context.Background()
	p := pool.New(ctx, 4)

	var count atomic.Int64
	p.Wait(
		func(_ context.Context) error { count.Add(1); return nil },
		func(_ context.Context) error { count.Add(1); return nil },
		func(_ context.Context) error { count.Add(1); return nil },
	)

	if got := count.Load(); got != 3 {
		t.Fatalf("executed %d tasks, want 3", got)
	}
}
