//go:build linux

package fusemount

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
)

// errQuotaTest is a sentinel error used by statfs quota-failure tests.
var errQuotaTest = errors.New("quota fetch failed")

// newStatfsRoot builds a RootNode with the given quota callback for
// statfs testing.
func newStatfsRoot(q QuotaFunc) *RootNode {
	root := NewRoot(NewRegistry(), testMountInfo{})
	root.quota = q
	return root
}

func TestRootNodeStatfs_KnownQuota(t *testing.T) {
	const total = 100 * statfsBlockSize
	const used = 40 * statfsBlockSize
	root := newStatfsRoot(func(_ context.Context) (int64, int64, error) {
		return total, used, nil
	})

	var out fuse.StatfsOut
	if errno := root.Statfs(context.Background(), &out); errno != 0 {
		t.Fatalf("Statfs returned errno %d", errno)
	}
	if out.Bsize != statfsBlockSize {
		t.Errorf("Bsize = %d, want %d", out.Bsize, statfsBlockSize)
	}
	if out.NameLen != statfsNameLen {
		t.Errorf("NameLen = %d, want %d", out.NameLen, statfsNameLen)
	}
	if out.Files != statfsFilesSentinel || out.Ffree != statfsFilesSentinel {
		t.Errorf("Files/Ffree = (%d,%d), want (%d,%d)", out.Files, out.Ffree, uint64(statfsFilesSentinel), uint64(statfsFilesSentinel))
	}
	if out.Blocks != 100 {
		t.Errorf("Blocks = %d, want 100", out.Blocks)
	}
	if out.Bfree != 60 || out.Bavail != 60 {
		t.Errorf("Bfree/Bavail = (%d,%d), want (60,60)", out.Bfree, out.Bavail)
	}
}

func TestRootNodeStatfs_NilQuotaReturnsZeros(t *testing.T) {
	root := newStatfsRoot(nil)

	var out fuse.StatfsOut
	if errno := root.Statfs(context.Background(), &out); errno != 0 {
		t.Fatalf("Statfs returned errno %d", errno)
	}
	if out.Blocks != 0 || out.Bfree != 0 || out.Bavail != 0 {
		t.Errorf("block counts = (%d,%d,%d), want all zero", out.Blocks, out.Bfree, out.Bavail)
	}
	// Constant fields still populated.
	if out.Bsize != statfsBlockSize || out.NameLen != statfsNameLen {
		t.Errorf("Bsize/NameLen = (%d,%d), want (%d,%d)", out.Bsize, out.NameLen, statfsBlockSize, statfsNameLen)
	}
	if out.Files != statfsFilesSentinel {
		t.Errorf("Files = %d, want sentinel", out.Files)
	}
}

func TestRootNodeStatfs_CachesWithinTTL(t *testing.T) {
	var calls int
	root := newStatfsRoot(func(_ context.Context) (int64, int64, error) {
		calls++
		return 10 * statfsBlockSize, 0, nil
	})

	var out fuse.StatfsOut
	_ = root.Statfs(context.Background(), &out)
	_ = root.Statfs(context.Background(), &out)

	if calls != 1 {
		t.Errorf("QuotaFunc called %d times, want 1 (cached within TTL)", calls)
	}
	if out.Blocks != 10 {
		t.Errorf("Blocks = %d, want 10", out.Blocks)
	}
}

func TestRootNodeStatfs_ErrorAfterSuccessReturnsCached(t *testing.T) {
	var calls int
	root := newStatfsRoot(func(_ context.Context) (int64, int64, error) {
		calls++
		if calls == 1 {
			return 80 * statfsBlockSize, 20 * statfsBlockSize, nil
		}
		return 0, 0, errQuotaTest
	})

	var out1 fuse.StatfsOut
	_ = root.Statfs(context.Background(), &out1)

	// Force cache expiry so the second call re-fetches (and fails).
	root.quotaAt = time.Now().Add(-2 * quotaCacheTTL)

	var out2 fuse.StatfsOut
	if errno := root.Statfs(context.Background(), &out2); errno != 0 {
		t.Fatalf("Statfs returned errno %d, want 0", errno)
	}
	if out2.Blocks != 80 || out2.Bfree != 60 {
		t.Errorf("after fetch error: Blocks/Bfree = (%d,%d), want (80,60) from cache", out2.Blocks, out2.Bfree)
	}
}

func TestRootNodeStatfs_UsedExceedsTotalClampsFree(t *testing.T) {
	root := newStatfsRoot(func(_ context.Context) (int64, int64, error) {
		return 10 * statfsBlockSize, 15 * statfsBlockSize, nil
	})

	var out fuse.StatfsOut
	_ = root.Statfs(context.Background(), &out)
	if out.Bfree != 0 || out.Bavail != 0 {
		t.Errorf("Bfree/Bavail = (%d,%d), want (0,0) when used > total", out.Bfree, out.Bavail)
	}
}
