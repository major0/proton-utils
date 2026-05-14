// Package keyring provides session index management and system keyring
// abstraction for Proton utilities. It is imported by both internal/cli
// (cobra-based CLI) and cmd/proton-fuse (daemon) without pulling in any
// CLI framework dependencies.
package keyring

import (
	"github.com/zalando/go-keyring"
)

// Keyring abstracts system keyring operations for testing.
type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, password string) error
	Delete(service, account string) error
}

// SystemKeyring delegates to go-keyring's package-level functions.
type SystemKeyring struct{}

// Get retrieves a secret from the system keyring.
func (SystemKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

// Set stores a secret in the system keyring.
func (SystemKeyring) Set(service, account, password string) error {
	return keyring.Set(service, account, password)
}

// Delete removes a secret from the system keyring.
func (SystemKeyring) Delete(service, account string) error {
	return keyring.Delete(service, account)
}
