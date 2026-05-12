// Package testutil provides shared test mock types for CLI packages.
package testutil

import "github.com/major0/proton-cli/api"

// MockSessionStore implements api.SessionStore for testing.
// All methods return nil by default. Set LoadErr to simulate load failures.
type MockSessionStore struct {
	LoadErr error
}

// Load returns nil credentials and the configured LoadErr.
func (m *MockSessionStore) Load() (*api.SessionCredentials, error) { return nil, m.LoadErr }

// Save is a no-op that always returns nil.
func (m *MockSessionStore) Save(_ *api.SessionCredentials) error { return nil }

// Delete is a no-op that always returns nil.
func (m *MockSessionStore) Delete() error { return nil }

// List returns nil and no error.
func (m *MockSessionStore) List() ([]string, error) { return nil, nil }

// Switch is a no-op that always returns nil.
func (m *MockSessionStore) Switch(_ string) error { return nil }
