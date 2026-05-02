package client_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
	"github.com/major0/proton-cli/api/drive/client"
	"pgregory.net/rapid"
)

// walkResolver is a mock LinkResolver that returns predetermined children
// for specific link IDs, with pre-decrypted names. Enables tree walk
// testing without real crypto infrastructure.
type walkResolver struct {
	// children maps linkID → slice of child proton.Links.
	children map[string][]proton.Link
	// names maps linkID → decrypted name.
	names map[string]string
}

func (m *walkResolver) ListLinkChildren(_ context.Context, _, linkID string, _ bool) ([]proton.Link, error) {
	return m.children[linkID], nil
}

func (m *walkResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	name := m.names[pLink.LinkID]
	return drive.NewTestLink(pLink, parent, parent.Share(), m, name)
}

func (m *walkResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (m *walkResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (m *walkResolver) Throttle() *api.Throttle { return nil }
func (m *walkResolver) MaxWorkers() int         { return 1 }

// treeNode describes a node in a generated test tree.
type treeNode struct {
	id       string
	name     string
	isFolder bool
	children []treeNode
}

// buildResolver constructs a walkResolver from a tree description.
func buildResolver(root treeNode) *drive.Link {
	r := &walkResolver{
		children: make(map[string][]proton.Link),
		names:    make(map[string]string),
	}

	// Register all nodes recursively.
	var register func(node treeNode)
	register = func(node treeNode) {
		r.names[node.id] = node.name
		if node.isFolder && len(node.children) > 0 {
			pChildren := make([]proton.Link, len(node.children))
			for i, child := range node.children {
				lt := proton.LinkTypeFile
				if child.isFolder {
					lt = proton.LinkTypeFolder
				}
				pChildren[i] = proton.Link{LinkID: child.id, Type: lt}
			}
			r.children[node.id] = pChildren
		}
		for _, child := range node.children {
			register(child)
		}
	}
	register(root)

	// Build the root link and share.
	lt := proton.LinkTypeFile
	if root.isFolder {
		lt = proton.LinkTypeFolder
	}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: root.id, Type: lt}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, r, root.name)
	share := drive.NewShare(pShare, nil, rootLink, r, "")

	// Re-create root link with share set.
	rootLink = drive.NewTestLink(rootPLink, nil, share, r, root.name)
	// Update share's root link.
	share.Link = rootLink

	return rootLink
}

// genTree generates a random tree structure for property testing.
func genTree(t *rapid.T, prefix string, depth, maxDepth int) treeNode {
	id := fmt.Sprintf("%s-dir-%d", prefix, depth)
	name := fmt.Sprintf("dir%d", depth)

	node := treeNode{
		id:       id,
		name:     name,
		isFolder: true,
	}

	if depth >= maxDepth {
		return node
	}

	// Generate 0-4 file children.
	nFiles := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("nFiles-%s-%d", prefix, depth))
	for i := 0; i < nFiles; i++ {
		fid := fmt.Sprintf("%s-file-%d-%d", prefix, depth, i)
		node.children = append(node.children, treeNode{
			id:       fid,
			name:     fmt.Sprintf("file%d_%d", depth, i),
			isFolder: false,
		})
	}

	// Generate 0-3 folder children.
	nFolders := rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("nFolders-%s-%d", prefix, depth))
	for i := 0; i < nFolders; i++ {
		childPrefix := fmt.Sprintf("%s-%d", prefix, i)
		child := genTree(t, childPrefix, depth+1, maxDepth)
		child.id = fmt.Sprintf("%s-subdir-%d-%d", prefix, depth, i)
		child.name = fmt.Sprintf("subdir%d_%d", depth, i)
		node.children = append(node.children, child)
	}

	return node
}

// collectEntries runs TreeWalk and collects all entries from the channel.
func collectEntries(ctx context.Context, c *client.Client, root *drive.Link, rootPath string, order drive.WalkOrder) ([]client.WalkEntry, error) {
	results := make(chan client.WalkEntry, 256)
	var entries []client.WalkEntry
	var walkErr error

	done := make(chan struct{})
	go func() {
		defer close(done)
		walkErr = c.TreeWalk(ctx, root, rootPath, order, -1, results)
		close(results)
	}()

	for entry := range results {
		entries = append(entries, entry)
	}
	<-done

	return entries, walkErr
}

// countDescendants counts total descendants of a treeNode (recursive).
func countDescendants(node treeNode) int {
	count := 0
	for _, child := range node.children {
		count++ // the child itself
		count += countDescendants(child)
	}
	return count
}

// TestTreeWalk_BreadthFirst_NonDecreasingDepth_Property verifies that
// BreadthFirst TreeWalk emits entries with non-decreasing Depth values
// for any random tree structure.
//
// **Property 8: TreeWalk Ordering Invariants**
// **Validates: Requirement 10.7**
func TestTreeWalk_BreadthFirst_NonDecreasingDepth_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxDepth := rapid.IntRange(1, 4).Draw(t, "maxDepth")
		tree := genTree(t, "root", 0, maxDepth)

		rootLink := buildResolver(tree)
		c := &client.Client{}

		entries, err := collectEntries(context.Background(), c, rootLink, "root", drive.BreadthFirst)
		if err != nil {
			t.Fatalf("TreeWalk BreadthFirst: %v", err)
		}

		// Verify non-decreasing depths.
		for i := 1; i < len(entries); i++ {
			if entries[i].Depth < entries[i-1].Depth {
				t.Fatalf("BreadthFirst: depth decreased from %d to %d at index %d (path %q → %q)",
					entries[i-1].Depth, entries[i].Depth, i,
					entries[i-1].Path, entries[i].Path)
			}
		}
	})
}

// TestTreeWalk_DepthFirst_DirectoryAfterDescendants_Property verifies that
// DepthFirst TreeWalk emits every directory only after all its descendants.
//
// **Property 8: TreeWalk Ordering Invariants**
// **Validates: Requirement 10.7**
func TestTreeWalk_DepthFirst_DirectoryAfterDescendants_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxDepth := rapid.IntRange(1, 4).Draw(t, "maxDepth")
		tree := genTree(t, "root", 0, maxDepth)

		rootLink := buildResolver(tree)
		c := &client.Client{}

		entries, err := collectEntries(context.Background(), c, rootLink, "root", drive.DepthFirst)
		if err != nil {
			t.Fatalf("TreeWalk DepthFirst: %v", err)
		}

		// Build a map of linkID → index in the emission order.
		indexByID := make(map[string]int, len(entries))
		for i, e := range entries {
			indexByID[e.Link.LinkID()] = i
		}

		// For every folder entry, verify all its descendants appear before it.
		var verifyOrder func(node treeNode)
		verifyOrder = func(node treeNode) {
			if !node.isFolder {
				return
			}
			parentIdx, ok := indexByID[node.id]
			if !ok {
				return // node wasn't emitted (no children to produce it)
			}
			for _, child := range node.children {
				childIdx, ok := indexByID[child.id]
				if !ok {
					continue
				}
				if childIdx >= parentIdx {
					t.Fatalf("DepthFirst: child %q (idx %d) emitted after parent %q (idx %d)",
						child.id, childIdx, node.id, parentIdx)
				}
				verifyOrder(child)
			}
		}
		verifyOrder(tree)
	})
}

// TestTreeWalk_ContextCancellation verifies that TreeWalk stops promptly
// when the context is cancelled and returns ctx.Err().
func TestTreeWalk_ContextCancellation(t *testing.T) {
	// Build a tree with enough entries to not finish before cancellation.
	tree := treeNode{
		id: "root", name: "root", isFolder: true,
		children: []treeNode{
			{id: "d1", name: "d1", isFolder: true, children: []treeNode{
				{id: "d1f1", name: "f1", isFolder: false},
				{id: "d1f2", name: "f2", isFolder: false},
				{id: "d1d1", name: "sub1", isFolder: true, children: []treeNode{
					{id: "d1d1f1", name: "f1", isFolder: false},
					{id: "d1d1f2", name: "f2", isFolder: false},
				}},
			}},
			{id: "d2", name: "d2", isFolder: true, children: []treeNode{
				{id: "d2f1", name: "f1", isFolder: false},
				{id: "d2f2", name: "f2", isFolder: false},
			}},
			{id: "f1", name: "f1", isFolder: false},
		},
	}

	rootLink := buildResolver(tree)
	c := &client.Client{}

	// Cancel after receiving 2 entries.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := make(chan client.WalkEntry, 1)

	done := make(chan error, 1)
	go func() {
		done <- c.TreeWalk(ctx, rootLink, "root", drive.BreadthFirst, -1, results)
		close(results)
	}()

	received := 0
	for range results {
		received++
		if received >= 2 {
			cancel()
		}
	}

	err := <-done
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled or nil, got: %v", err)
	}

	// We should have received at most a few entries, not the full tree.
	total := 1 + countDescendants(tree) // root + all descendants
	if received >= total {
		t.Fatalf("expected early termination, but received all %d entries", total)
	}
}

// TestTreeWalk_EmptyRoot verifies that walking an empty folder emits
// only the root entry.
func TestTreeWalk_EmptyRoot(t *testing.T) {
	tree := treeNode{id: "root", name: "root", isFolder: true}
	rootLink := buildResolver(tree)
	c := &client.Client{}

	for _, order := range []drive.WalkOrder{drive.BreadthFirst, drive.DepthFirst} {
		entries, err := collectEntries(context.Background(), c, rootLink, "root", order)
		if err != nil {
			t.Fatalf("TreeWalk empty root: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry for empty root, got %d", len(entries))
		}
		if entries[0].Path != "root" {
			t.Fatalf("expected path 'root', got %q", entries[0].Path)
		}
		if entries[0].Depth != 0 {
			t.Fatalf("expected depth 0, got %d", entries[0].Depth)
		}
	}
}

// TestTreeWalk_FileRoot verifies that walking a file (non-folder) emits
// only the root entry.
func TestTreeWalk_FileRoot(t *testing.T) {
	r := &walkResolver{
		children: make(map[string][]proton.Link),
		names:    map[string]string{"file-root": "myfile.txt"},
	}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: "file-root", Type: proton.LinkTypeFile}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, r, "myfile.txt")
	share := drive.NewShare(pShare, nil, rootLink, r, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, r, "myfile.txt")
	share.Link = rootLink

	c := &client.Client{}

	for _, order := range []drive.WalkOrder{drive.BreadthFirst, drive.DepthFirst} {
		entries, err := collectEntries(context.Background(), c, rootLink, "myfile.txt", order)
		if err != nil {
			t.Fatalf("TreeWalk file root: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry for file root, got %d", len(entries))
		}
	}
}

// TestTreeWalk_EntryCount verifies that both walk orders emit the same
// number of entries for the same tree.
func TestTreeWalk_EntryCount(t *testing.T) {
	tree := treeNode{
		id: "root", name: "root", isFolder: true,
		children: []treeNode{
			{id: "d1", name: "d1", isFolder: true, children: []treeNode{
				{id: "d1f1", name: "f1", isFolder: false},
			}},
			{id: "f1", name: "f1", isFolder: false},
		},
	}

	rootLink := buildResolver(tree)
	c := &client.Client{}

	bfEntries, err := collectEntries(context.Background(), c, rootLink, "root", drive.BreadthFirst)
	if err != nil {
		t.Fatalf("BreadthFirst: %v", err)
	}

	dfEntries, err := collectEntries(context.Background(), c, rootLink, "root", drive.DepthFirst)
	if err != nil {
		t.Fatalf("DepthFirst: %v", err)
	}

	if len(bfEntries) != len(dfEntries) {
		t.Fatalf("entry count mismatch: BreadthFirst=%d, DepthFirst=%d", len(bfEntries), len(dfEntries))
	}

	expected := 1 + countDescendants(tree)
	if len(bfEntries) != expected {
		t.Fatalf("expected %d entries, got %d", expected, len(bfEntries))
	}
}

// TestWalkEntryNamePropagation_Property verifies that for all non-root
// WalkEntry values, EntryName is non-empty.
//
// **Property 7: WalkEntry.EntryName Propagation**
// **Validates: Requirement 4.1**
func TestWalkEntryNamePropagation_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxDepth := rapid.IntRange(1, 4).Draw(t, "maxDepth")
		tree := genTree(t, "root", 0, maxDepth)

		rootLink := buildResolver(tree)
		c := &client.Client{}

		for _, order := range []drive.WalkOrder{drive.BreadthFirst, drive.DepthFirst} {
			entries, err := collectEntries(context.Background(), c, rootLink, "root", order)
			if err != nil {
				t.Fatalf("TreeWalk: %v", err)
			}

			for _, entry := range entries {
				// Root entry has no EntryName (it's the walk starting point).
				if entry.Depth == 0 {
					continue
				}
				if entry.EntryName == "" {
					t.Fatalf("non-root WalkEntry at path %q depth %d has empty EntryName",
						entry.Path, entry.Depth)
				}
			}
		}
	})
}
