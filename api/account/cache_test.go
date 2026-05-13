package account

import (
	"path/filepath"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// TestClientCacheRoundTrip verifies that putUser/getUser and
// putAddresses/getAddresses round-trip through the ObjectCache.
func TestClientCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cache := api.NewObjectCache(filepath.Join(dir, "account", "test-uid"))
	c := &Client{cache: cache}

	// Initially empty — cache miss returns nil.
	if u := c.getUser(); u != nil {
		t.Fatal("expected nil user on empty cache")
	}
	if addrs := c.getAddresses(); addrs != nil {
		t.Fatal("expected nil addresses on empty cache")
	}

	// Put and get user.
	user := proton.User{
		ID:          "user-123",
		Name:        "testuser",
		DisplayName: "Test User",
		Email:       "test@proton.me",
	}
	c.putUser(user)
	got := c.getUser()
	if got == nil {
		t.Fatal("expected cached user, got nil")
	}
	if got.ID != user.ID {
		t.Errorf("user ID: got %q, want %q", got.ID, user.ID)
	}
	if got.Name != user.Name {
		t.Errorf("user Name: got %q, want %q", got.Name, user.Name)
	}
	if got.Email != user.Email {
		t.Errorf("user Email: got %q, want %q", got.Email, user.Email)
	}

	// Put and get addresses.
	addrs := []proton.Address{
		{ID: "addr-1", Email: "one@proton.me"},
		{ID: "addr-2", Email: "two@proton.me"},
	}
	c.putAddresses(addrs)
	gotAddrs := c.getAddresses()
	if gotAddrs == nil {
		t.Fatal("expected cached addresses, got nil")
	}
	if len(gotAddrs) != 2 {
		t.Fatalf("addresses length: got %d, want 2", len(gotAddrs))
	}
	if gotAddrs[0].ID != "addr-1" {
		t.Errorf("address[0] ID: got %q, want %q", gotAddrs[0].ID, "addr-1")
	}
	if gotAddrs[1].Email != "two@proton.me" {
		t.Errorf("address[1] Email: got %q, want %q", gotAddrs[1].Email, "two@proton.me")
	}
}

// TestClientCacheNilSafe verifies that cache methods on a Client with
// a nil cache do not panic and return nil (cache miss).
func TestClientCacheNilSafe(t *testing.T) {
	c := &Client{cache: nil}

	// All operations on nil cache should be no-ops.
	if u := c.getUser(); u != nil {
		t.Fatal("expected nil user on nil cache")
	}
	if addrs := c.getAddresses(); addrs != nil {
		t.Fatal("expected nil addresses on nil cache")
	}

	// Put should not panic.
	c.putUser(proton.User{ID: "u1"})
	c.putAddresses([]proton.Address{{ID: "a1"}})
}

// TestNewClientWithCache verifies that NewClientWithCache creates a
// Client with a non-nil cache when given valid inputs.
func TestNewClientWithCache(t *testing.T) {
	// With empty UID, cache should be nil (disabled).
	c := NewClientWithCache(&api.Session{}, "")
	if c.cache != nil {
		t.Fatal("expected nil cache for empty UID")
	}

	// With non-empty UID but no XDG_RUNTIME_DIR, cache should be nil.
	t.Setenv("XDG_RUNTIME_DIR", "")
	c = NewClientWithCache(&api.Session{}, "some-uid")
	if c.cache != nil {
		t.Fatal("expected nil cache when XDG_RUNTIME_DIR is empty")
	}

	// With valid XDG_RUNTIME_DIR and UID, cache should be non-nil.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c = NewClientWithCache(&api.Session{}, "some-uid")
	if c.cache == nil {
		t.Fatal("expected non-nil cache with valid XDG_RUNTIME_DIR and UID")
	}
}

// TestPopulateAccountCache verifies the convenience function for
// populating the account cache from login flows.
func TestPopulateAccountCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	uid := "populate-test-uid"
	user := proton.User{ID: "u-pop", Name: "popuser"}
	addrs := []proton.Address{{ID: "a-pop", Email: "pop@proton.me"}}

	PopulateAccountCache(uid, user, addrs)

	// Verify by reading back through a Client with the same UID.
	cache := api.NewObjectCache(filepath.Join(dir, "proton", "account", uid))
	c := &Client{cache: cache}

	got := c.getUser()
	if got == nil {
		t.Fatal("expected cached user after PopulateAccountCache")
	}
	if got.ID != "u-pop" {
		t.Errorf("user ID: got %q, want %q", got.ID, "u-pop")
	}

	gotAddrs := c.getAddresses()
	if gotAddrs == nil {
		t.Fatal("expected cached addresses after PopulateAccountCache")
	}
	if len(gotAddrs) != 1 || gotAddrs[0].ID != "a-pop" {
		t.Errorf("unexpected addresses: %+v", gotAddrs)
	}
}

// TestPopulateAccountCache_EmptyUID verifies that PopulateAccountCache
// is a no-op when UID is empty.
func TestPopulateAccountCache_EmptyUID(_ *testing.T) {
	// Should not panic.
	PopulateAccountCache("", proton.User{}, nil)
}
