package account

import (
	"testing"

	"github.com/major0/proton-cli/api"
)

// TestNewClient verifies that NewClient returns a non-nil Client
// with the provided session stored.
func TestNewClient(t *testing.T) {
	session := &api.Session{}
	c := NewClient(session)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.Session != session {
		t.Fatal("NewClient did not store the session")
	}
}

// TestNewClient_NilSession verifies that NewClient handles a nil session
// without panicking. The caller is responsible for providing a valid
// session — this test documents the behavior.
func TestNewClient_NilSession(t *testing.T) {
	c := NewClient(nil)
	if c == nil {
		t.Fatal("NewClient returned nil for nil session")
	}
	if c.Session != nil {
		t.Fatal("expected nil session on client")
	}
}
