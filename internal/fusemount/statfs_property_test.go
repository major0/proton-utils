//go:build linux

package fusemount

import (
	"context"
	"errors"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
	"pgregory.net/rapid"
)

// Feature: protonfs-statfs, Property 1: Block Arithmetic Consistency
// For any total >= 0 and used in [0, total], Statfs reports
// Blocks = total/4096, Bfree = Bavail = (total-used)/4096, Bsize = 4096.
// **Validates: Requirements 1.3**
func TestPropertyStatfsBlockArithmetic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		total := rapid.Int64Range(1, 1<<50).Draw(t, "total")
		used := rapid.Int64Range(0, total).Draw(t, "used")

		root := newStatfsRoot(func(_ context.Context) (int64, int64, error) {
			return total, used, nil
		})

		var out fuse.StatfsOut
		if errno := root.Statfs(context.Background(), &out); errno != 0 {
			t.Fatalf("Statfs errno %d", errno)
		}

		wantBlocks := uint64(total) / statfsBlockSize    //nolint:gosec // total >= 1 in this range
		wantFree := uint64(total-used) / statfsBlockSize //nolint:gosec // used <= total, so difference is non-negative
		if out.Blocks != wantBlocks {
			t.Errorf("Blocks = %d, want %d", out.Blocks, wantBlocks)
		}
		if out.Bfree != wantFree {
			t.Errorf("Bfree = %d, want %d", out.Bfree, wantFree)
		}
		if out.Bavail != out.Bfree {
			t.Errorf("Bavail = %d, want == Bfree %d", out.Bavail, out.Bfree)
		}
		if out.Bsize != statfsBlockSize {
			t.Errorf("Bsize = %d, want %d", out.Bsize, statfsBlockSize)
		}
		// Used never exceeds total here, so free blocks cannot exceed total blocks.
		if out.Bfree > out.Blocks {
			t.Errorf("Bfree %d exceeds Blocks %d", out.Bfree, out.Blocks)
		}
	})
}

// Feature: protonfs-statfs, Property 2: Constant Fields
// For any quota outcome (success, failure, nil callback), Statfs sets
// Bsize = 4096, NameLen = 255, Files = Ffree = sentinel, and returns errno 0.
// **Validates: Requirements 1.4, 1.5, 3.1**
func TestPropertyStatfsConstantFields(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		outcome := rapid.IntRange(0, 2).Draw(t, "outcome")

		var q QuotaFunc
		switch outcome {
		case 0:
			q = nil // nil callback
		case 1:
			total := rapid.Int64Range(0, 1<<50).Draw(t, "total")
			used := rapid.Int64Range(0, 1<<50).Draw(t, "used")
			q = func(_ context.Context) (int64, int64, error) { return total, used, nil }
		case 2:
			q = func(_ context.Context) (int64, int64, error) {
				return 0, 0, errors.New("boom")
			}
		}
		root := newStatfsRoot(q)

		var out fuse.StatfsOut
		errno := root.Statfs(context.Background(), &out)
		if errno != 0 {
			t.Fatalf("Statfs errno %d, want 0", errno)
		}
		if out.Bsize != statfsBlockSize {
			t.Errorf("Bsize = %d, want %d", out.Bsize, statfsBlockSize)
		}
		if out.NameLen != statfsNameLen {
			t.Errorf("NameLen = %d, want %d", out.NameLen, statfsNameLen)
		}
		if out.Files != statfsFilesSentinel || out.Ffree != statfsFilesSentinel {
			t.Errorf("Files/Ffree = (%d,%d), want sentinel %d", out.Files, out.Ffree, uint64(statfsFilesSentinel))
		}
	})
}
