package driveCmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/major0/proton-cli/api/drive"
	"github.com/major0/proton-cli/api/drive/client"
	"github.com/major0/proton-cli/api/pool"
)

// RunPipeline transfers files using the provided worker pool. Each
// worker claims a block from the current job, reads it from Src,
// writes it to Dst, then claims the next. When the current job has
// no unclaimed blocks, the worker advances to the next job.
//
// If Src or Dst implement CloneableReader/CloneableWriter, each worker
// gets its own clone (and thus its own file descriptor). Clones are
// closed when the worker is done.
//
// The pool's concurrency limit controls how many workers run in
// parallel. The pipeline submits nWorkers tasks and waits for all
// of them to complete.
func RunPipeline(_ context.Context, p *pool.Pool, jobs []CopyJob, opts TransferOpts) error {
	if len(jobs) == 0 {
		return nil
	}

	nWorkers := p.Limit()

	// Build block maps for all jobs upfront.
	maps := make([]*blockMap, len(jobs))
	totalBlocks := 0
	for i := range jobs {
		maps[i] = newBlockMap(&jobs[i])
		totalBlocks += jobs[i].Src.BlockCount()
	}

	// Shared state: current job index.
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
			jobIdx++
		}
		return -1, nil, 0, 0
	}

	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		p.Go(&wg, func(ctx context.Context) error {
			buf := alignedAlloc(drive.BlockSize)

			// Per-worker clones, keyed by job index. Opened on
			// first use, closed when the worker exits.
			srcClones := make(map[int]client.BlockReader)
			dstClones := make(map[int]client.BlockWriter)
			defer func() {
				for _, r := range srcClones {
					_ = r.Close()
				}
				for _, w := range dstClones {
					_ = w.Close()
				}
			}()

			for {
				if ctx.Err() != nil {
					return nil
				}
				ji, job, idx, sz := claim()
				if job == nil {
					return nil
				}

				// Resolve per-worker reader for this job.
				src, ok := srcClones[ji]
				if !ok {
					src = job.Src
					if cr, canClone := job.Src.(CloneableReader); canClone {
						cloned, err := cr.CloneReader()
						if err != nil {
							addErr(fmt.Errorf("clone reader %s: %w", job.Src.Describe(), err))
							continue
						}
						src = cloned
					}
					srcClones[ji] = src
				}

				// Resolve per-worker writer for this job.
				dst, ok := dstClones[ji]
				if !ok {
					dst = job.Dst
					if cw, canClone := job.Dst.(CloneableWriter); canClone {
						cloned, err := cw.CloneWriter()
						if err != nil {
							addErr(fmt.Errorf("clone writer %s: %w", job.Dst.Describe(), err))
							continue
						}
						dst = cloned
					}
					dstClones[ji] = dst
				}

				n, err := src.ReadBlock(ctx, idx, buf[:sz])
				if err != nil {
					addErr(fmt.Errorf("read %s block %d: %w", job.Src.Describe(), idx, err))
					continue
				}
				if err := dst.WriteBlock(ctx, idx, buf[:n]); err != nil {
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

	wg.Wait()

	// Close all template readers and writers (non-cloned resources).
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
