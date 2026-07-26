package drive

import (
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

type invalidateCall struct {
	linkID     string
	blockCount int
}

// blockInvalidateSpy records blockStore.Invalidate calls while delegating
// every other blockStore method to the embedded testBlockStore.
type blockInvalidateSpy struct {
	*testBlockStore
	calls []invalidateCall
}

func (s *blockInvalidateSpy) Invalidate(linkID string, blockCount int) {
	s.calls = append(s.calls, invalidateCall{linkID, blockCount})
}

func newInvalidateTestClient(t *testing.T) (*Client, *blockInvalidateSpy, *api.ObjectCache) {
	t.Helper()
	oc := api.NewObjectCache(t.TempDir())
	spy := &blockInvalidateSpy{testBlockStore: &testBlockStore{blocks: map[int][]byte{}}}
	c := &Client{
		linkTable:      make(map[string]*Link),
		xattrFailCount: make(map[string]int),
		hydratedLinks:  make(map[string]proton.Link),
		objectCache:    oc,
		blockStore:     spy,
	}
	return c, spy, oc
}

// TestInvalidateLinkRemovesEverywhere validates Property 1: a delete/update
// invalidation removes the link from the Link Table and object cache, clears
// its xattrFailCount, invalidates its blocks (file links), and fires the
// parent hook.
//
// Validates: Requirements 2.1, 2.2, 3.1
func TestInvalidateLinkRemovesEverywhere(t *testing.T) {
	c, spy, oc := newInvalidateTestClient(t)
	const linkID, parentID = "link-1", "parent-1"

	c.putLink(linkID, &Link{})
	if err := oc.Write(SanitizeLinkID(linkID), []byte("cached")); err != nil {
		t.Fatalf("seed object cache: %v", err)
	}
	c.cacheCount.Store(1)
	c.tableMu.Lock()
	c.xattrFailCount[linkID] = 3
	c.tableMu.Unlock()

	var hookArg string
	c.SetInvalidationHook(func(p string) { hookArg = p })

	c.InvalidateLink(linkID, parentID, 4)

	if c.GetLink(linkID) != nil {
		t.Error("link still present in Link Table")
	}
	if oc.Has(SanitizeLinkID(linkID)) {
		t.Error("object-cache entry not erased")
	}
	c.tableMu.RLock()
	_, hasFail := c.xattrFailCount[linkID]
	c.tableMu.RUnlock()
	if hasFail {
		t.Error("xattrFailCount not cleared")
	}
	if got := c.cacheCount.Load(); got != 0 {
		t.Errorf("cacheCount = %d, want 0", got)
	}
	if len(spy.calls) != 1 || spy.calls[0] != (invalidateCall{linkID, 4}) {
		t.Errorf("block Invalidate calls = %v, want one {%s,4}", spy.calls, linkID)
	}
	if hookArg != parentID {
		t.Errorf("hook fired with %q, want %q", hookArg, parentID)
	}
}

// TestInvalidateLinkFolderSkipsBlockInvalidate verifies that a folder-type
// invalidation (blockCount == 0) does not touch the block cache.
func TestInvalidateLinkFolderSkipsBlockInvalidate(t *testing.T) {
	c, spy, _ := newInvalidateTestClient(t)
	c.putLink("dir-1", &Link{})

	c.InvalidateLink("dir-1", "", 0)

	if len(spy.calls) != 0 {
		t.Errorf("block Invalidate called for a folder link: %v", spy.calls)
	}
	if c.GetLink("dir-1") != nil {
		t.Error("link still present in Link Table")
	}
}

// TestInvalidateParent validates Property 2: a create invalidation removes
// the parent's Link Table and object-cache entries and fires the hook with
// the parent LinkID.
//
// Validates: Requirements 2.3, 4.1, 4.2
func TestInvalidateParent(t *testing.T) {
	c, _, oc := newInvalidateTestClient(t)
	const parentID = "parent-1"

	c.putLink(parentID, &Link{})
	if err := oc.Write(SanitizeLinkID(parentID), []byte("cached")); err != nil {
		t.Fatalf("seed object cache: %v", err)
	}
	c.cacheCount.Store(1)

	var hookArg string
	c.SetInvalidationHook(func(p string) { hookArg = p })

	c.InvalidateParent(parentID)

	if c.GetLink(parentID) != nil {
		t.Error("parent still present in Link Table")
	}
	if oc.Has(SanitizeLinkID(parentID)) {
		t.Error("parent object-cache entry not erased")
	}
	if hookArg != parentID {
		t.Errorf("hook fired with %q, want %q", hookArg, parentID)
	}
}

// TestInvalidateParentEmptyIsNoop verifies InvalidateParent("") is a no-op
// and does not fire the hook.
func TestInvalidateParentEmptyIsNoop(t *testing.T) {
	c, _, _ := newInvalidateTestClient(t)
	fired := false
	c.SetInvalidationHook(func(string) { fired = true })

	c.InvalidateParent("")

	if fired {
		t.Error("hook fired for empty parent LinkID")
	}
}

// TestInvalidateBestEffortNoObjectCache validates Property 5: invalidation is
// best-effort — with no object cache wired up, it neither panics nor fails,
// and Link Table removal still occurs.
//
// Validates: Requirements 6.4 (resilience)
func TestInvalidateBestEffortNoObjectCache(t *testing.T) {
	c := &Client{
		linkTable:      make(map[string]*Link),
		xattrFailCount: make(map[string]int),
		blockStore:     &blockInvalidateSpy{testBlockStore: &testBlockStore{blocks: map[int][]byte{}}},
		// objectCache intentionally nil (disk cache disabled).
	}
	c.putLink("l1", &Link{})

	c.InvalidateLink("l1", "", 0) // must not panic

	if c.GetLink("l1") != nil {
		t.Error("Link Table removal should still occur when object cache is nil")
	}
}

// TestInvalidateHookNoopWhenUnset validates Property 6: invalidation with no
// hook registered (or a nil hook) is a no-op for the hook and does not panic.
//
// Validates: Requirements 4.2
func TestInvalidateHookNoopWhenUnset(t *testing.T) {
	c, _, _ := newInvalidateTestClient(t)
	c.putLink("p", &Link{})

	c.InvalidateParent("p") // no hook registered — must not panic

	c.SetInvalidationHook(nil)
	c.putLink("x", &Link{})
	c.InvalidateLink("x", "y", 0) // nil hook — must not panic
}
