package api

import (
	"encoding/json"
	"os"
	"path/filepath"

	proton "github.com/ProtonMail/go-proton-api"
)

// newAccountCacheForUID constructs an ObjectCache scoped to a Proton UID
// at $XDG_RUNTIME_DIR/proton/account/{uid}/. Returns nil when
// $XDG_RUNTIME_DIR is not set or uid is empty (disabling caching).
//
// This is a transitional helper — when session operations move to
// api/account/ (Task 5), these functions become unexported Client methods.
func newAccountCacheForUID(uid string) *ObjectCache {
	if uid == "" {
		return nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return nil
	}
	basePath := filepath.Join(dir, "proton", "account", uid)
	return NewObjectCache(basePath)
}

// accountCacheGetUser loads a cached User from the ObjectCache.
// Returns nil on cache miss or nil cache.
func accountCacheGetUser(cache *ObjectCache) *proton.User {
	data, err := cache.Read("user")
	if err != nil || data == nil {
		return nil
	}
	var u proton.User
	if err := json.Unmarshal(data, &u); err != nil {
		return nil
	}
	return &u
}

// accountCachePutUser caches a User in the ObjectCache.
// Silently discards errors.
func accountCachePutUser(cache *ObjectCache, u proton.User) {
	data, err := json.Marshal(u)
	if err != nil {
		return
	}
	_ = cache.Write("user", data)
}

// accountCacheGetAddresses loads cached addresses from the ObjectCache.
// Returns nil on cache miss or nil cache.
func accountCacheGetAddresses(cache *ObjectCache) []proton.Address {
	data, err := cache.Read("addresses")
	if err != nil || data == nil {
		return nil
	}
	var addrs []proton.Address
	if err := json.Unmarshal(data, &addrs); err != nil {
		return nil
	}
	return addrs
}

// accountCachePutAddresses caches the address list in the ObjectCache.
// Silently discards errors.
func accountCachePutAddresses(cache *ObjectCache, addrs []proton.Address) {
	data, err := json.Marshal(addrs)
	if err != nil {
		return
	}
	_ = cache.Write("addresses", data)
}
