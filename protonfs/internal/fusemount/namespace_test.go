//go:build linux

package fusemount

import (
	"context"
	"syscall"
	"testing"
)

// mockHandler implements NamespaceHandler for testing.
type mockHandler struct {
	entries []DirEntry
	nodes   map[string]Node
	attr    Attr
}

func (m *mockHandler) Lookup(ctx context.Context, name string) (Node, syscall.Errno) {
	if n, ok := m.nodes[name]; ok {
		return n, 0
	}
	return nil, syscall.ENOENT
}

func (m *mockHandler) Readdir(ctx context.Context) ([]DirEntry, syscall.Errno) {
	return m.entries, 0
}

func (m *mockHandler) Getattr(ctx context.Context) (Attr, syscall.Errno) {
	return m.attr, 0
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(r.List()) != 0 {
		t.Fatalf("expected empty registry, got %d entries", len(r.List()))
	}
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	h := &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0755}}

	r.Register("drive", h)

	got, ok := r.Lookup("drive")
	if !ok {
		t.Fatal("expected to find 'drive' handler")
	}
	if got != h {
		t.Fatal("returned handler does not match registered handler")
	}
}

func TestRegistryLookupMissing(t *testing.T) {
	r := NewRegistry()

	_, ok := r.Lookup("nonexistent")
	if ok {
		t.Fatal("expected Lookup to return false for unregistered prefix")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register("mail", &mockHandler{})
	r.Register("drive", &mockHandler{})
	r.Register("lumo", &mockHandler{})

	list := r.List()
	expected := []string{"drive", "lumo", "mail"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d entries, got %d", len(expected), len(list))
	}
	for i, name := range expected {
		if list[i] != name {
			t.Errorf("list[%d] = %q, want %q", i, list[i], name)
		}
	}
}

func TestRegistryListEmpty(t *testing.T) {
	r := NewRegistry()
	list := r.List()
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(list))
	}
}

func TestRegistryOverwrite(t *testing.T) {
	r := NewRegistry()
	h1 := &mockHandler{attr: Attr{Mode: 1}}
	h2 := &mockHandler{attr: Attr{Mode: 2}}

	r.Register("drive", h1)
	r.Register("drive", h2)

	got, ok := r.Lookup("drive")
	if !ok {
		t.Fatal("expected to find 'drive' handler")
	}
	if got != h2 {
		t.Fatal("expected overwritten handler")
	}

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 entry after overwrite, got %d", len(list))
	}
}
