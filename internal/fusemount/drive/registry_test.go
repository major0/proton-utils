//go:build linux

package drive

import (
	"testing"

	"github.com/major0/proton-utils/api/drive"
)

// fakeDir is a test dirInvalidator that counts invalidateChildren calls.
type fakeDir struct{ invalidated int }

func (f *fakeDir) invalidateChildren() { f.invalidated++ }

// TestRegistryOnInvalidateParent verifies that OnInvalidateParent clears the
// registered node's children, is a no-op for unregistered parents, and stops
// firing after unregisterDir (the go-fuse Forget path).
func TestRegistryOnInvalidateParent(t *testing.T) {
	h := NewDriveHandler(nil)
	fd := &fakeDir{}
	h.registerDir("link-1", fd)

	h.OnInvalidateParent("link-1")
	if fd.invalidated != 1 {
		t.Fatalf("invalidated = %d, want 1", fd.invalidated)
	}

	// No-op for an unregistered parent (must not panic).
	h.OnInvalidateParent("not-registered")
	if fd.invalidated != 1 {
		t.Fatalf("invalidated = %d after no-op, want 1", fd.invalidated)
	}

	// After unregister (Forget), the node is no longer invalidated.
	h.unregisterDir("link-1")
	h.OnInvalidateParent("link-1")
	if fd.invalidated != 1 {
		t.Fatalf("invalidated = %d after unregister, want 1", fd.invalidated)
	}
}

// TestRegistryInvalidateAll verifies InvalidateAll clears every registered
// node's children.
func TestRegistryInvalidateAll(t *testing.T) {
	h := NewDriveHandler(nil)
	a, b := &fakeDir{}, &fakeDir{}
	h.registerDir("l1", a)
	h.registerDir("l2", b)

	h.InvalidateAll()

	if a.invalidated != 1 || b.invalidated != 1 {
		t.Fatalf("invalidated a=%d b=%d, want 1 each", a.invalidated, b.invalidated)
	}
}

// TestRegistryNewestWins verifies that registering a second node under the
// same LinkID replaces the first (newest live node wins).
func TestRegistryNewestWins(t *testing.T) {
	h := NewDriveHandler(nil)
	old, current := &fakeDir{}, &fakeDir{}
	h.registerDir("link-1", old)
	h.registerDir("link-1", current)

	h.OnInvalidateParent("link-1")
	if old.invalidated != 0 {
		t.Fatalf("stale node invalidated = %d, want 0", old.invalidated)
	}
	if current.invalidated != 1 {
		t.Fatalf("current node invalidated = %d, want 1", current.invalidated)
	}
}

// TestRegistryBounded verifies the registry stays within maxDirNodes even when
// more distinct nodes are registered (bounding memory if Forget is missed).
func TestRegistryBounded(t *testing.T) {
	h := NewDriveHandler(nil)
	for i := 0; i < maxDirNodes+10; i++ {
		h.registerDir(string(rune(i))+"-key", &fakeDir{})
	}
	h.dirNodesMu.Lock()
	n := len(h.dirNodes)
	h.dirNodesMu.Unlock()
	if n > maxDirNodes {
		t.Fatalf("registry size = %d, want <= %d", n, maxDirNodes)
	}
}

// TestLinkDirNodeInvalidateChildren verifies the node-level children cache is
// cleared by invalidateChildren.
func TestLinkDirNodeInvalidateChildren(t *testing.T) {
	n := &LinkDirNode{}
	n.setChildren(map[string]*drive.Link{"a": {}})

	if _, ok := n.childByName("a"); !ok {
		t.Fatal("expected cached child 'a' before invalidation")
	}

	n.invalidateChildren()

	if _, ok := n.childByName("a"); ok {
		t.Fatal("child 'a' should be gone after invalidateChildren")
	}
}
