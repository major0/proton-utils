package driveCmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/major0/proton-cli/api/drive"
	"github.com/major0/proton-cli/api/pool"
)

// RunPipeline transfers files using a pool of workers. Each worker
// claims a block from the current job, reads it from Src, writes it
// to Dst, then claims the next. When the current job has no unclaimed
// blocks, the worker advances to the next job in the queue.
//
// Multiple jobs may have in-flight blocks simultaneously at job
// boundaries, but new blocks are always claimed from the frontmost
// incomplete job. This gives natural breadth-first serialization with
// concurrent overlap during transitions.
func RunPipeline(ctx context.Context, jobs []CopyJob, opts TransferOpts) error {
	if len(jobs) == 0 {
		return nil
	}

	nWorkers := opts.workers()

	// Build block maps for all jobs upfront.
	maps := make([]*blockMap, len(jobs))
	totalBlocks := 0
	for i := range jobs {
		maps[i] = newBlockMap(&jobs[i])
		totalBlocks += jobs[i].Src.BlockCount()
	}

	// Shared state: current job index and its block map.
	var mu sync.Mutex
	jobIdx := 0

	// Progress tracking.
	var blocksDone int
	var bytesDone int64
	startTime := time.Now()

	// Error collection.
	var errMu sync.Mutex
	var errs []error
	addErr := func(err error) {
		errMu.Lock()
		errs = append(errs, err)
		errMu.Unlock()
	}

	// blockDone is called after each successful block write.
	blockDone := func(_ *CopyJob, blockBytes int64) {
		mu.Lock()
		blocksDone++
		bytesDone += blockBytes
		bd := blocksDone
		byd := bytesDone
		mu.Unlock()

		if opts.Progress != nil {
			elapsed := time.Since(startTime).Seconds()
			var rate float64
			if elapsed > 0 {
				rate = float64(byd) / elapsed
			}
			// completed/total here is blocks, not files.
			opts.Progress(bd, totalBlocks, byd, rate)
		}
	}

	// jobDone tracks per-job block completion for verbose output.
	jobDoneCount := make([]int32, len(jobs))
	jobComplete := func(_ int, job *CopyJob) {
		if opts.Verbose != nil {
			opts.Verbose(job.Src.Describe(), job.Dst.Describe())
		}
	}

	// claim returns the next block to process: the job index, CopyJob,
	// block index, and block size. Returns -1 job index when exhausted.
	claim := func() (int, *CopyJob, int, int64) {
		mu.Lock()
		defer mu.Unlock()
		for jobIdx < len(maps) {
			idx := maps[jobIdx].claim()
			if idx >= 0 {
				ji := jobIdx
				job := maps[jobIdx].job
				return ji, job, idx, job.Src.BlockSize(idx)
			}
			// Current job exhausted — advance.
			jobIdx++
		}
		return -1, nil, 0, 0
	}

	p := pool.New(ctx, nWorkers)
	for i := 0; i < nWorkers; i++ {
		p.Go(func(ctx context.Context) error {
			buf := make([]byte, drive.BlockSize)
			for {
				if ctx.Err() != nil {
					return nil
				}
				ji, job, idx, sz := claim()
				if job == nil {
					return nil
				}
				n, err := job.Src.ReadBlock(ctx, idx, buf[:sz])
				if err != nil {
					addErr(fmt.Errorf("read %s block %d: %w", job.Src.Describe(), idx, err))
					continue
				}
				if err := job.Dst.WriteBlock(ctx, idx, buf[:n]); err != nil {
					addErr(fmt.Errorf("write %s block %d: %w", job.Dst.Describe(), idx, err))
				} else {
					blockDone(job, int64(n))
					if int(atomic.AddInt32(&jobDoneCount[ji], 1)) == job.Src.BlockCount() {
						jobComplete(ji, job)
					}
				}
				clear(buf[:n])
			}
		})
	}

	// Wait for all workers. Pool tasks return nil — errors are
	// collected via addErr, not through the pool.
	_ = p.Wait()

	// Close all readers and writers.
	for i := range jobs {
		if err := jobs[i].Src.Close(); err != nil {
			addErr(fmt.Errorf("close reader %s: %w", jobs[i].Src.Describe(), err))
		}
		if err := jobs[i].Dst.Close(); err != nil {
			addErr(fmt.Errorf("close writer %s: %w", jobs[i].Dst.Describe(), err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
