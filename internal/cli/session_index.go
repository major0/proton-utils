package cli

import "github.com/major0/proton-utils/internal/keyring"

// SessionIndex is re-exported from internal/keyring for backward compatibility.
type SessionIndex = keyring.SessionIndex

// SessionIndexData is re-exported from internal/keyring for backward compatibility.
type SessionIndexData = keyring.SessionIndexData

// AccountEntry is re-exported from internal/keyring for backward compatibility.
type AccountEntry = keyring.AccountEntry

// NewSessionStore delegates to keyring.NewSessionStore.
func NewSessionStore(path string, account string, service string, kr Keyring) *SessionIndex {
	return keyring.NewSessionStore(path, account, service, kr)
}
