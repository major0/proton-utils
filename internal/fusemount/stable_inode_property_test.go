//go:build linux

package fusemount

import (
	"context"
	"hash/fnv"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
	"pgregory.net/rapid"
)

// testInodeFromLinkID is the test-side equivalent of the (not-yet-implemented)
// drive-package inodeFromLinkID helper described in the design. It derives a
// stable FUSE inode number from a LinkID using FNV-64a, remapping a hash of 1
// to ^uint64(0) so it never collides with the reserved root inode (Ino == 1).
// The fix will introduce the real inodeFromLinkID in drive/nodes.go; this local
// copy lets the exploration test reference the expected value before the fix
// exists.
func testInodeFromLinkID(linkID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(linkID))
	ino := h.Sum64()
	if ino == 1 {
		ino = ^uint64(0)
	}
	return ino
}

// stableInoNode is a mock Node that carries a settable LinkID and implements
// InodeNumber() uint64 — the same optional interface the fix will type-assert in
// the child-minting paths. It returns a stable, LinkID-derived inode number, so
// the expected (post-fix) behavior is that every mint of this node yields the
// same StableAttr().Ino.
type stableInoNode struct {
	attr   Attr
	linkID string
}

func (n *stableInoNode) Getattr(_ context.Context) (Attr, syscall.Errno) {
	return n.attr, 0
}

// InodeNumber returns the stable, LinkID-derived inode number.
func (n *stableInoNode) InodeNumber() uint64 {
	return testInodeFromLinkID(n.linkID)
}

// Feature: protonfs-stable-inodes, Property 1: Bug Condition — Stable inode
// numbers across repeated mints.
//
// For any child object that exposes a LinkID, minted through any child-minting
// path (wrapChild via LOOKUP or READDIRPLUS), the code SHALL set
// StableAttr.Ino to a deterministic value derived from the LinkID, such that:
//
//	(a) repeated mints of the same LinkID yield the identical inode number,
//	(b) that value equals inodeFromLinkID(linkID), and
//	(c) distinct LinkIDs yield distinct inode numbers.
//
// This is a BUG CONDITION exploration test: it MUST FAIL on the unfixed code,
// where wrapChild builds fs.StableAttr{Mode: mode} and leaves Ino == 0, so
// go-fuse auto-assigns a fresh, non-LinkID-derived inode number on every mint.
//
// **Validates: Requirements 1.1, 1.2, 1.3**
func TestPropertyStableInodeAcrossMints(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "linkID")
		// A second, guaranteed-distinct LinkID for the distinct-inode check.
		linkID2 := linkID + "-distinct"

		child := &stableInoNode{
			attr:   Attr{Mode: syscall.S_IFREG | 0644, Nlink: 1},
			linkID: linkID,
		}
		other := &stableInoNode{
			attr:   Attr{Mode: syscall.S_IFREG | 0644, Nlink: 1},
			linkID: linkID2,
		}
		h := &mockHandler{nodes: map[string]Node{"c": child, "d": other}}
		root := mountedRoot(h, 5, 6)

		// Mint 1: DispatchNode.Lookup (LOOKUP path → wrapChild).
		var out1 fuse.EntryOut
		inode1, errno1 := root.Lookup(context.Background(), "c", &out1)
		if errno1 != 0 {
			t.Fatalf("DispatchNode.Lookup(c) errno %d", errno1)
		}

		// Mint 2: readdirHandle.Lookup (READDIRPLUS path → wrapChild), same object.
		entries := []DirEntry{{Name: "c", Mode: syscall.S_IFREG | 0644, Link: &mockPrefetchLink{}}}
		handle := buildReaddirHandle(root, entries)
		var out2 fuse.EntryOut
		inode2, errno2 := handle.Lookup(context.Background(), "c", &out2)
		if errno2 != 0 {
			t.Fatalf("readdirHandle.Lookup(c) errno %d", errno2)
		}

		// Mint 3: a distinct object via DispatchNode.Lookup.
		var out3 fuse.EntryOut
		inode3, errno3 := root.Lookup(context.Background(), "d", &out3)
		if errno3 != 0 {
			t.Fatalf("DispatchNode.Lookup(d) errno %d", errno3)
		}

		want := testInodeFromLinkID(linkID)
		ino1 := inode1.StableAttr().Ino
		ino2 := inode2.StableAttr().Ino
		ino3 := inode3.StableAttr().Ino

		// (a) Two mints of the same object must share one inode number.
		if ino1 != ino2 {
			t.Fatalf("inode-number instability: same object minted twice got Ino=%d (Lookup) and Ino=%d (READDIRPLUS); want equal, LinkID-derived %d",
				ino1, ino2, want)
		}
		// (b) The shared value must be the LinkID-derived inode number.
		if ino1 != want {
			t.Fatalf("Ino=%d, want inodeFromLinkID(%q)=%d", ino1, linkID, want)
		}
		// (c) Distinct LinkIDs must produce distinct inode numbers.
		if ino3 == ino1 {
			t.Fatalf("distinct LinkIDs produced the same Ino=%d (linkID=%q vs %q)", ino1, linkID, linkID2)
		}
	})
}

// firstAutomaticIno is the start of go-fuse's automatically-assigned inode
// range. When a mint passes StableAttr.Ino == 0, the bridge allocates from a
// counter that defaults to 1<<63 (fs.Options.FirstAutomaticIno == 0 → 1<<63,
// per go-fuse bridge.go), incrementing on every mint. An inode number in this
// range is therefore the observable signature of "Ino left 0, auto-assigned".
const firstAutomaticIno = uint64(1) << 63

// Feature: protonfs-stable-inodes, Property 2: Preservation — Attribute
// population and non-LinkID minting unchanged.
//
// For any mint where the node exposes no stable LinkID, the code SHALL leave
// StableAttr.Ino unset (0) so go-fuse auto-assigns the inode number, exactly
// as before the fix. mockNode deliberately does NOT implement InodeNumber(),
// so it stands in for a synthetic/test node with no LinkID. This asserts the
// preserved behavior for Requirement 3.4 across both child-minting paths
// (DispatchNode.Lookup and readdirHandle.Lookup).
//
// Observation-first note: the design describes this behavior as "StableAttr.Ino
// == 0", which is the value passed to NewInode. Observed through the in-process
// go-fuse bridge, however, an Ino of 0 is rewritten to a go-fuse *automatic*
// inode number (>= 1<<63), freshly allocated on each mint. The preserved,
// observable behavior is therefore: a no-LinkID node receives an automatic
// inode number (not a stable, caller-supplied one), and two mints of the same
// no-LinkID node yield DISTINCT numbers (no stable identity) — the direct
// contrast to Property 1's stable, LinkID-derived numbers.
//
// The attribute-population (Req 3.1) and file-type Mode-bit (Req 3.2)
// invariants are already covered by readdir_handle_property_test.go Property 1
// (TestPropertyAttrPopulationCorrectness) and Property 4
// (TestPropertyDirectoryTypeInvariants); the root/namespace fixed-inode
// invariant (Req 3.3) is out of scope for the child-minting paths and left
// unchanged. This test adds only the missing no-LinkID observation.
//
// This is a PRESERVATION test: it PASSES on the unfixed code (baseline to
// preserve) and MUST continue to pass after the fix, because childStableAttr
// leaves Ino == 0 for nodes that do not implement InodeNumber().
//
// **Validates: Requirements 3.1, 3.2, 3.3, 3.4**
func TestPropertyNoLinkIDNodeMintsAutoAssigned(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		isDir := rapid.Bool().Draw(t, "isDir")
		var child Node
		var mode uint32
		if isDir {
			child = &mockNode{attr: Attr{Mode: syscall.S_IFDIR | 0700, Nlink: 2}}
			mode = syscall.S_IFDIR | 0700
		} else {
			perms := rapid.Uint32Range(0, 0o777).Draw(t, "perms")
			child = &mockNode{attr: Attr{Mode: syscall.S_IFREG | perms, Nlink: 1}}
			mode = syscall.S_IFREG | perms
		}
		h := &mockHandler{nodes: map[string]Node{"c": child}}
		root := mountedRoot(h, 5, 6)

		// Path 1: DispatchNode.Lookup (LOOKUP → wrapChild).
		var out1 fuse.EntryOut
		inode1, errno1 := root.Lookup(context.Background(), "c", &out1)
		if errno1 != 0 {
			t.Fatalf("DispatchNode.Lookup errno %d", errno1)
		}
		ino1 := inode1.StableAttr().Ino

		// Path 2: readdirHandle.Lookup (READDIRPLUS → wrapChild), same object.
		entries := []DirEntry{{Name: "c", Mode: mode, Link: &mockPrefetchLink{}}}
		handle := buildReaddirHandle(root, entries)
		var out2 fuse.EntryOut
		inode2, errno2 := handle.Lookup(context.Background(), "c", &out2)
		if errno2 != 0 {
			t.Fatalf("readdirHandle.Lookup errno %d", errno2)
		}
		ino2 := inode2.StableAttr().Ino

		// A no-LinkID node must be auto-assigned (Ino left 0 → go-fuse range),
		// not given a stable caller-supplied number.
		if ino1 < firstAutomaticIno {
			t.Fatalf("no-LinkID node via Lookup got Ino=%d, want auto-assigned (>= %d)", ino1, firstAutomaticIno)
		}
		if ino2 < firstAutomaticIno {
			t.Fatalf("no-LinkID node via READDIRPLUS got Ino=%d, want auto-assigned (>= %d)", ino2, firstAutomaticIno)
		}
		// No stable identity: two mints of the same no-LinkID node differ.
		if ino1 == ino2 {
			t.Fatalf("no-LinkID node minted twice got identical Ino=%d; expected auto-assigned (distinct) numbers", ino1)
		}
	})
}
