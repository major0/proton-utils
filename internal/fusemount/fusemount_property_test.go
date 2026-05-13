//go:build linux

package fusemount

import (
	"context"
	"sort"
	"syscall"
	"testing"
	"unicode"

	"github.com/hanwen/go-fuse/v2/fuse"
	"pgregory.net/rapid"
)

// Feature: proton-service, Property 1: Readdir consistency
// For any set of registered namespace prefixes, RootNode.Readdir SHALL return
// exactly those prefixes as directory entries (no more, no fewer), each with
// mode S_IFDIR.
// **Validates: Requirements 2.2, 2.4**
func TestPropertyReaddirConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a set of valid prefix strings (non-empty, no slashes, printable).
		prefixes := rapid.SliceOfN(
			rapid.StringMatching(`[a-z][a-z0-9]{0,9}`),
			0, 10,
		).Draw(t, "prefixes")

		// Deduplicate.
		seen := make(map[string]bool)
		var unique []string
		for _, p := range prefixes {
			if !seen[p] {
				seen[p] = true
				unique = append(unique, p)
			}
		}

		reg := NewRegistry()
		for _, p := range unique {
			reg.Register(p, &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0755}})
		}

		root := NewRoot(reg, testMountInfo{})
		stream, errno := root.Readdir(context.Background())
		if errno != 0 {
			t.Fatalf("Readdir returned errno %d", errno)
		}

		var entries []fuse.DirEntry
		for stream.HasNext() {
			e, _ := stream.Next()
			entries = append(entries, e)
		}

		// Verify count matches: . and .. plus namespace entries.
		wantCount := 2 + len(unique)
		if len(entries) != wantCount {
			t.Fatalf("Readdir returned %d entries, want %d", len(entries), wantCount)
		}

		// First two entries are . and ..
		if entries[0].Name != "." {
			t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, ".")
		}
		if entries[1].Name != ".." {
			t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "..")
		}

		// Verify remaining entries match sorted unique prefixes.
		sort.Strings(unique)
		for i, e := range entries[2:] {
			if e.Name != unique[i] {
				t.Errorf("entry[%d].Name = %q, want %q", i+2, e.Name, unique[i])
			}
			if e.Mode != fuse.S_IFDIR {
				t.Errorf("entry[%d].Mode = %o, want S_IFDIR", i, e.Mode)
			}
		}
	})
}

// Feature: proton-service, Property 2: Dispatch correctness
// For any registered namespace prefix P and any lookup name N: if N equals a
// registered prefix, Lookup SHALL delegate to that handler; if N does not match
// any registered prefix, Lookup SHALL return ENOENT.
// **Validates: Requirements 2.3, 2.4, 3.4**
func TestPropertyDispatchCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate registered prefixes (lowercase).
		prefixes := rapid.SliceOfN(
			rapid.StringMatching(`[a-z][a-z0-9]{0,9}`),
			1, 5,
		).Draw(t, "prefixes")

		seen := make(map[string]bool)
		var unique []string
		for _, p := range prefixes {
			if !seen[p] {
				seen[p] = true
				unique = append(unique, p)
			}
		}

		reg := NewRegistry()
		for _, p := range unique {
			reg.Register(p, &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0755}})
		}

		root := NewRoot(reg, testMountInfo{})

		// Verify registry-level dispatch: registered name found.
		registered := rapid.SampledFrom(unique).Draw(t, "registered")
		h, ok := reg.Lookup(registered)
		if !ok {
			t.Fatalf("registry.Lookup(%q) returned false for registered prefix", registered)
		}
		if h == nil {
			t.Fatalf("registry.Lookup(%q) returned nil handler", registered)
		}

		// Lookup an unregistered name (uppercase to avoid collision) — should return ENOENT.
		unregistered := rapid.StringMatching(`[A-Z][A-Z0-9]{0,9}`).Draw(t, "unregistered")
		if !seen[unregistered] {
			_, errno := root.Lookup(context.Background(), unregistered, &fuse.EntryOut{})
			if errno != syscall.ENOENT {
				t.Errorf("Lookup(%q) returned errno %d, want ENOENT", unregistered, errno)
			}
		}
	})
}

// Feature: proton-service, Property 3: Capability gating
// For any NamespaceHandler that does not implement an optional capability
// interface, the corresponding FUSE operation SHALL return ENOSYS.
// **Validates: Requirements 3.5**
func TestPropertyCapabilityGating(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random subset of capabilities to include.
		hasCreate := rapid.Bool().Draw(t, "hasCreate")
		hasMkdir := rapid.Bool().Draw(t, "hasMkdir")
		hasRemove := rapid.Bool().Draw(t, "hasRemove")
		hasRename := rapid.Bool().Draw(t, "hasRename")

		// Build a handler with the selected capabilities.
		var handler NamespaceHandler
		switch {
		case hasCreate && hasMkdir && hasRemove && hasRename:
			handler = &fullHandler{}
		case hasCreate:
			handler = &mockCreatorHandler{}
		case hasMkdir:
			handler = &mockMkdirerHandler{}
		case hasRemove:
			handler = &mockRemoverHandler{}
		default:
			handler = &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0755}}
		}

		d := &DispatchNode{handler: handler, isRoot: true}

		// Test Create capability.
		_, _, _, createErr := d.Create(context.Background(), "f", 0, 0644, &fuse.EntryOut{})
		if !hasCreate {
			if _, ok := handler.(NodeCreator); !ok {
				if createErr != syscall.ENOSYS {
					t.Errorf("Create without NodeCreator: got errno %d, want ENOSYS", createErr)
				}
			}
		}

		// Test Mkdir capability.
		_, mkdirErr := d.Mkdir(context.Background(), "d", 0755, &fuse.EntryOut{})
		if !hasMkdir {
			if _, ok := handler.(NodeMkdirer); !ok {
				if mkdirErr != syscall.ENOSYS {
					t.Errorf("Mkdir without NodeMkdirer: got errno %d, want ENOSYS", mkdirErr)
				}
			}
		}

		// Test Unlink capability.
		unlinkErr := d.Unlink(context.Background(), "f")
		if !hasRemove {
			if _, ok := handler.(NodeRemover); !ok {
				if unlinkErr != syscall.ENOSYS {
					t.Errorf("Unlink without NodeRemover: got errno %d, want ENOSYS", unlinkErr)
				}
			}
		}

		// Test Rmdir capability.
		rmdirErr := d.Rmdir(context.Background(), "d")
		if !hasRemove {
			if _, ok := handler.(NodeRemover); !ok {
				if rmdirErr != syscall.ENOSYS {
					t.Errorf("Rmdir without NodeRemover: got errno %d, want ENOSYS", rmdirErr)
				}
			}
		}

		// Test Rename capability.
		renameErr := d.Rename(context.Background(), "old", nil, "new", 0)
		if !hasRename {
			if _, ok := handler.(NodeRenamer); !ok {
				if renameErr != syscall.ENOSYS {
					t.Errorf("Rename without NodeRenamer: got errno %d, want ENOSYS", renameErr)
				}
			}
		}
	})
}

// Feature: proton-service, Property 7: Panic recovery
// For any NamespaceHandler method that panics, the dispatch layer SHALL recover
// the panic and return EIO to the caller without terminating the process.
// **Validates: Requirements 8.4**
func TestPropertyPanicRecovery(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random panic message.
		msg := rapid.StringOfN(
			rapid.RuneFrom(nil, unicode.Letter, unicode.Digit),
			1, 50, -1,
		).Draw(t, "panicMsg")

		h := &panicHandler{msg: msg}
		d := &DispatchNode{handler: h, isRoot: true}

		// All operations should recover and return EIO.
		var out fuse.AttrOut
		if errno := d.Getattr(context.Background(), nil, &out); errno != syscall.EIO {
			t.Errorf("Getattr after panic(%q): got errno %d, want EIO", msg, errno)
		}

		if _, errno := d.Readdir(context.Background()); errno != syscall.EIO {
			t.Errorf("Readdir after panic(%q): got errno %d, want EIO", msg, errno)
		}

		if _, errno := d.Lookup(context.Background(), "x", &fuse.EntryOut{}); errno != syscall.EIO {
			t.Errorf("Lookup after panic(%q): got errno %d, want EIO", msg, errno)
		}
	})
}

// fullHandler implements all optional capability interfaces for testing.
type fullHandler struct {
	mockHandler
}

func (f *fullHandler) Create(ctx context.Context, name string, flags uint32, mode uint32) (Node, FileHandle, syscall.Errno) {
	return &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644}}, nil, 0
}

func (f *fullHandler) Mkdir(ctx context.Context, name string, mode uint32) (Node, syscall.Errno) {
	return &mockNode{attr: Attr{Mode: syscall.S_IFDIR | 0755}}, 0
}

func (f *fullHandler) Unlink(ctx context.Context, name string) syscall.Errno {
	return 0
}

func (f *fullHandler) Rmdir(ctx context.Context, name string) syscall.Errno {
	return 0
}

func (f *fullHandler) Rename(ctx context.Context, oldName string, newParent Node, newName string) syscall.Errno {
	return 0
}
