// Package pool provides a bounded worker pool with throttle integration.
//
// The pool enforces a global concurrency limit via a semaphore and
// optionally gates each task through a Waiter (e.g., rate-limit
// throttle). A single pool is shared across the session lifetime;
// callers submit work and wait on individual batches without draining
// the pool.
package pool

import (
	"context"
	"sync"
	"sync/atomic"
)

// Waiter blocks until ready or the context is cancelled.
// *api.Throttle satisfies this interface without changes.
type Waiter interface {
	Wait(ctx context.Context) error
}

// Option configures a Pool at construction time.
type Option func(*Pool)

// WithThrottle attaches a Waiter that gates each task dispatch.
// When set, Waiter.Wait is called before every task body executes.
func WithThrottle(w Waiter) Option {
	return func(p *Pool) { p.throttle = w }
}

// Stats holds atomic counters for pool utilization.
type Stats struct {
	active    atomic.Int64
	submitted atomic.Int64
	completed atomic.Int64
}

// StatsSnapshot is a point-in-time copy of pool counters.
type StatsSnapshot struct {
	Active    int64
	Submitted int64
	Completed int64
}

// Snapshot returns a point-in-time copy of the counters.
func (s *Stats) Snapshot() StatsSnapshot {
	return StatsSnapshot{
		Active:    s.active.Load(),
		Submitted: s.submitted.Load(),
		Completed: s.completed.Load(),
	}
}

// Pool is a long-lived bounded worker pool. It uses a semaphore to
// enforce a concurrency limit and an optional throttle to gate task
// dispatch. Unlike errgroup, the pool is reusable — callers submit
// work via Go and track completion with their own sync primitives.
type Pool struct {
	sem      chan struct{}
	ctx      context.Context
	throttle Waiter
	stats    Stats
}

// New creates a pool with n concurrent workers. The context governs
// lifetime — when cancelled, throttle waits and new tasks abort early.
func New(ctx context.Context, n int, opts ...Option) *Pool {
	p := &Pool{
		sem: make(chan struct{}, n),
		ctx: ctx,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Limit returns the pool's concurrency limit.
func (p *Pool) Limit() int { return cap(p.sem) }

// Go submits a task to the pool. It acquires a semaphore slot (blocking
// if all workers are busy), runs the task in a new goroutine, and
// releases the slot when done. The caller's WaitGroup (if non-nil) is
// used to track completion of a batch of tasks.
func (p *Pool) Go(wg *sync.WaitGroup, task func(context.Context) error) {
	if wg != nil {
		wg.Add(1)
	}
	p.stats.submitted.Add(1)

	// Acquire semaphore slot — blocks when pool is at capacity.
	p.sem <- struct{}{}

	go func() {
		defer func() {
			<-p.sem // release slot
			p.stats.completed.Add(1)
			if wg != nil {
				wg.Done()
			}
		}()

		if p.throttle != nil {
			if err := p.throttle.Wait(p.ctx); err != nil {
				return
			}
		}

		p.stats.active.Add(1)
		defer p.stats.active.Add(-1)

		_ = task(p.ctx)
	}()
}

// Wait is a convenience that creates a WaitGroup, submits all tasks,
// and waits for them to complete. For one-shot batch work.
func (p *Pool) Wait(tasks ...func(context.Context) error) {
	var wg sync.WaitGroup
	for _, t := range tasks {
		p.Go(&wg, t)
	}
	wg.Wait()
}

// Stats returns a point-in-time snapshot of pool counters.
func (p *Pool) Stats() StatsSnapshot {
	return p.stats.Snapshot()
}
