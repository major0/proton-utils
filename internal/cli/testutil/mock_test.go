package testutil

import (
	"errors"
	"testing"

	"github.com/major0/proton-utils/api"
)

// TestMockSessionStoreInterface verifies that MockSessionStore satisfies
// the api.SessionStore interface at compile time.
func TestMockSessionStoreInterface(_ *testing.T) {
	var _ api.SessionStore = (*MockSessionStore)(nil)
}

// TestMockSessionStoreDefaultNil verifies all methods return nil by default.
func TestMockSessionStoreDefaultNil(t *testing.T) {
	m := &MockSessionStore{}

	creds, err := m.Load()
	if creds != nil {
		t.Fatalf("expected nil creds, got %v", creds)
	}
	if err != nil {
		t.Fatalf("expected nil error from Load, got %v", err)
	}

	if err := m.Save(nil); err != nil {
		t.Fatalf("expected nil from Save, got %v", err)
	}
	if err := m.Delete(); err != nil {
		t.Fatalf("expected nil from Delete, got %v", err)
	}

	list, err := m.List()
	if list != nil {
		t.Fatalf("expected nil list, got %v", list)
	}
	if err != nil {
		t.Fatalf("expected nil error from List, got %v", err)
	}

	if err := m.Switch("test"); err != nil {
		t.Fatalf("expected nil from Switch, got %v", err)
	}
}

// TestMockSessionStoreLoadErr verifies that LoadErr is returned by Load.
func TestMockSessionStoreLoadErr(t *testing.T) {
	wantErr := errors.New("session not found")
	m := &MockSessionStore{LoadErr: wantErr}

	creds, err := m.Load()
	if creds != nil {
		t.Fatalf("expected nil creds, got %v", creds)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}
