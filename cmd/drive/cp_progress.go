package driveCmd

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// transferOpts builds TransferOpts from the resolved copy options.
func transferOpts(opts cpOptions) TransferOpts {
	topts := TransferOpts{}
	if opts.progress {
		topts.Progress = makeProgressFunc()
	}
	if opts.verbose {
		topts.Verbose = func(src, dst string) {
			fmt.Fprintf(os.Stderr, "'%s' -> '%s'\n", src, dst)
		}
	}
	return topts
}

// makeProgressFunc returns a Progress callback that rate-limits output
// to stderr at 10Hz.
func makeProgressFunc() func(completed, total int, bytes int64, rate float64) {
	var mu sync.Mutex
	var lastPrint time.Time
	return func(completed, total int, bytes int64, rate float64) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if now.Sub(lastPrint) < 100*time.Millisecond && completed < total {
			return // rate-limit to 10Hz, always print final
		}
		lastPrint = now
		fmt.Fprintf(os.Stderr, "\r%d/%d blocks, %s, %s/s",
			completed, total, formatBytes(bytes), formatBytes(int64(rate)))
		if completed == total {
			fmt.Fprintln(os.Stderr)
		}
	}
}

// formatBytes returns a human-readable byte count.
func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
