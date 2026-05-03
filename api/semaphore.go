package api

import (
	"context"
	"sync"
	"sync/atomic"
)

// StatsSnapshot is a point-in-time copy of utilization counters.
type StatsSnapshot struct {
	Active    int64
	Submitted int64
	Completed int64
}

// semaphoreStats holds atomic counters for semaphore utilization.
type semaphoreStats struct {
	active    atomic.Int64
	submitted atomic.Int64
	completed atomic.Int64
}

// snapshot returns a point-in-time copy of the counters.
func (s *semaphoreStats) snapshot() StatsSnapshot {
	return StatsSnapshot{
		Active:    s.active.Load(),
		Submitted: s.submitted.Load(),
		Completed: s.completed.Load(),
	}
}

// Semaphore provides bounded concurrency control for API calls.
// It gates goroutines through a channel-based semaphore and an
// optional Throttle for rate-limit backoff. The API is intentionally
// identical to the former pool.Pool to minimize migration diff.
type Semaphore struct {
	sem      chan struct{}
	ctx      context.Context
	throttle *Throttle
	stats    semaphoreStats
}

// NewSemaphore creates a semaphore with n concurrent slots. The context
// governs lifetime — when cancelled, throttle waits and new tasks abort
// early. If throttle is non-nil, Throttle.Wait is called before every
// task body executes.
func NewSemaphore(ctx context.Context, n int, throttle *Throttle) *Semaphore {
	return &Semaphore{
		sem:      make(chan struct{}, n),
		ctx:      ctx,
		throttle: throttle,
	}
}

// Limit returns the concurrency limit.
func (s *Semaphore) Limit() int { return cap(s.sem) }

// Go submits a task. It acquires a semaphore slot (blocking if all
// slots are busy), runs the task in a new goroutine, and releases the
// slot on completion. The caller's WaitGroup (if non-nil) tracks batch
// completion.
func (s *Semaphore) Go(wg *sync.WaitGroup, task func(context.Context) error) {
	if wg != nil {
		wg.Add(1)
	}
	s.stats.submitted.Add(1)

	// Acquire semaphore slot — blocks when at capacity.
	s.sem <- struct{}{}

	go func() {
		defer func() {
			<-s.sem // release slot
			s.stats.completed.Add(1)
			if wg != nil {
				wg.Done()
			}
		}()

		if s.throttle != nil {
			if err := s.throttle.Wait(s.ctx); err != nil {
				return
			}
		}

		s.stats.active.Add(1)
		defer s.stats.active.Add(-1)

		_ = task(s.ctx)
	}()
}

// Wait submits all tasks and waits for completion. Convenience method
// for one-shot batch work.
func (s *Semaphore) Wait(tasks ...func(context.Context) error) {
	var wg sync.WaitGroup
	for _, t := range tasks {
		s.Go(&wg, t)
	}
	wg.Wait()
}

// Stats returns a point-in-time snapshot of utilization counters.
func (s *Semaphore) Stats() StatsSnapshot {
	return s.stats.snapshot()
}
