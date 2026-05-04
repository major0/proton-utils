// Package account provides Proton Account-specific types and operations.
package account

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
)

// Client wraps an api.Session with Account-specific operations.
type Client struct {
	Session *api.Session
	cache   *api.ObjectCache
}

// NewClient constructs an Account client from an existing session.
func NewClient(session *api.Session) *Client {
	return &Client{Session: session}
}

// NewClientWithCache constructs an Account client with an ObjectCache
// scoped to the given UID. The cache stores raw encrypted User and
// Address API objects on disk at $XDG_RUNTIME_DIR/proton/account/{uid}/.
// Returns a Client with caching disabled when uid is empty or
// $XDG_RUNTIME_DIR is not set.
func NewClientWithCache(session *api.Session, uid string) *Client {
	return &Client{
		Session: session,
		cache:   newAccountCache(uid),
	}
}

// newAccountCache constructs an ObjectCache scoped to a Proton UID.
// Returns nil when $XDG_RUNTIME_DIR is not set or uid is empty,
// which disables caching (ObjectCache is nil-safe).
func newAccountCache(uid string) *api.ObjectCache {
	if uid == "" {
		return nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return nil
	}
	basePath := filepath.Join(dir, "proton", "account", uid)
	return api.NewObjectCache(basePath)
}

// getUser loads a cached User. Returns nil on cache miss.
func (c *Client) getUser() *proton.User {
	data, err := c.cache.Read("user")
	if err != nil || data == nil {
		return nil
	}
	var u proton.User
	if err := json.Unmarshal(data, &u); err != nil {
		return nil
	}
	return &u
}

// putUser caches a User. Silently discards errors.
func (c *Client) putUser(u proton.User) {
	data, err := json.Marshal(u)
	if err != nil {
		return
	}
	_ = c.cache.Write("user", data)
}

// getAddresses loads cached addresses. Returns nil on cache miss.
func (c *Client) getAddresses() []proton.Address {
	data, err := c.cache.Read("addresses")
	if err != nil || data == nil {
		return nil
	}
	var addrs []proton.Address
	if err := json.Unmarshal(data, &addrs); err != nil {
		return nil
	}
	return addrs
}

// putAddresses caches the address list. Silently discards errors.
func (c *Client) putAddresses(addrs []proton.Address) {
	data, err := json.Marshal(addrs)
	if err != nil {
		return
	}
	_ = c.cache.Write("addresses", data)
}

// GetUser returns the authenticated user's profile and quota information.
// The returned User is an opaque wrapper — consumers access fields via
// accessor methods and do not need to import go-proton-api.
func (c *Client) GetUser(ctx context.Context) (User, error) {
	u, err := c.Session.Client.GetUser(ctx)
	if err != nil {
		return User{}, fmt.Errorf("account.GetUser: %w", err)
	}
	return newUser(u), nil
}

// GetAddresses returns all email addresses associated with the account.
// The returned Address values are opaque wrappers — consumers access
// fields via accessor methods and do not need to import go-proton-api.
func (c *Client) GetAddresses(ctx context.Context) ([]Address, error) {
	raw, err := c.Session.Client.GetAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("account.GetAddresses: %w", err)
	}
	addrs := make([]Address, len(raw))
	for i, a := range raw {
		addrs[i] = newAddress(a)
	}
	return addrs, nil
}

// PopulateAccountCache creates a temporary ObjectCache scoped to the
// given UID and stores the provided User and Address data. This is a
// convenience for callers (e.g., login flows) that do not hold a Client
// but need to populate the account cache.
func PopulateAccountCache(uid string, user proton.User, addrs []proton.Address) {
	cache := newAccountCache(uid)
	if cache == nil {
		return
	}
	if data, err := json.Marshal(user); err == nil {
		_ = cache.Write("user", data)
	}
	if data, err := json.Marshal(addrs); err == nil {
		_ = cache.Write("addresses", data)
	}
}
