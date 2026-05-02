package api

import (
	"encoding/json"
	"os"
	"path/filepath"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/peterbourgon/diskv/v3"
)

// AccountCache caches encrypted User and Address API objects on disk
// at $XDG_RUNTIME_DIR/proton/account/{uid}/. These are raw encrypted
// objects from the API — no decrypted content is stored.
//
// When nil (XDG_RUNTIME_DIR unset or uid empty), all operations are
// safe no-ops.
type AccountCache struct {
	dv *diskv.Diskv
}

// NewAccountCache constructs an AccountCache scoped to a Proton UID.
// Returns nil when $XDG_RUNTIME_DIR is not set or uid is empty.
func NewAccountCache(uid string) *AccountCache {
	if uid == "" {
		return nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return nil
	}
	basePath := filepath.Join(dir, "proton", "account", uid)
	dv := diskv.New(diskv.Options{
		BasePath:     basePath,
		Transform:    func(_ string) []string { return []string{} },
		CacheSizeMax: 0,
		TempDir:      filepath.Join(basePath, ".tmp"),
	})
	return &AccountCache{dv: dv}
}

// GetUser loads a cached User. Returns nil on miss.
func (c *AccountCache) GetUser() *proton.User {
	if c == nil {
		return nil
	}
	data, err := c.dv.Read("user")
	if err != nil {
		return nil
	}
	var u proton.User
	if err := json.Unmarshal(data, &u); err != nil {
		return nil
	}
	return &u
}

// PutUser caches a User.
func (c *AccountCache) PutUser(u proton.User) {
	if c == nil {
		return
	}
	data, err := json.Marshal(u)
	if err != nil {
		return
	}
	_ = c.dv.Write("user", data)
}

// GetAddresses loads cached addresses. Returns nil on miss.
func (c *AccountCache) GetAddresses() []proton.Address {
	if c == nil {
		return nil
	}
	data, err := c.dv.Read("addresses")
	if err != nil {
		return nil
	}
	var addrs []proton.Address
	if err := json.Unmarshal(data, &addrs); err != nil {
		return nil
	}
	return addrs
}

// PutAddresses caches the address list.
func (c *AccountCache) PutAddresses(addrs []proton.Address) {
	if c == nil {
		return
	}
	data, err := json.Marshal(addrs)
	if err != nil {
		return
	}
	_ = c.dv.Write("addresses", data)
}
