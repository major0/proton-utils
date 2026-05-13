// Package api provides the core API client library for proton-utils.
package api

// SessionStore defines the interface for persisting and retrieving session data.
type SessionStore interface {
	Load() (*SessionCredentials, error)
	Save(creds *SessionCredentials) error
	Delete() error
	List() ([]string, error)
	Switch(account string) error
}
