package cli

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
