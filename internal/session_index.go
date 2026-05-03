package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/major0/proton-cli/api"
)

// SessionIndexData is the on-disk JSON structure for the session index file.
type SessionIndexData struct {
	Accounts map[string]AccountEntry `json:"accounts"`
}

// AccountEntry represents a single account in the session index.
type AccountEntry struct {
	Username string            `json:"username"`
	Sessions map[string]string `json:"sessions"` // service name → UUID
}

// SessionIndex manages the on-disk session index file and keyring lookups.
// It maps (account, service) pairs to UUIDs stored in the index file,
// while actual secrets live in the system keyring keyed by those UUIDs.
type SessionIndex struct {
	path    string // path to the index JSON file
	account string // current account name (from --account flag)
	service string // current service context (e.g. "drive", "mail", "*")
	kr      Keyring
	data    *SessionIndexData
}

// keyringService is the service name used for all keyring operations.
const keyringService = "proton-cli"

// NewSessionStore returns a SessionIndex for the given account and service.
// The path is the filesystem location of the JSON index file. The keyring
// is used for all secret storage and retrieval.
func NewSessionStore(path string, account string, service string, kr Keyring) *SessionIndex {
	return &SessionIndex{
		path:    path,
		account: account,
		service: service,
		kr:      kr,
	}
}

// readIndex reads the session index file from disk. A missing file is
// treated as an empty index (first-run case). Invalid JSON returns an error.
func (si *SessionIndex) readIndex() (*SessionIndexData, error) {
	data, err := os.ReadFile(si.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &SessionIndexData{Accounts: make(map[string]AccountEntry)}, nil
		}
		return nil, fmt.Errorf("session load %q/%q: %w", si.account, si.service, err)
	}

	var idx SessionIndexData
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("session load %q/%q: %w", si.account, si.service, err)
	}

	if idx.Accounts == nil {
		idx.Accounts = make(map[string]AccountEntry)
	}

	return &idx, nil
}

// writeIndex writes the session index data to disk as JSON.
func (si *SessionIndex) writeIndex(idx *SessionIndexData) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("session save %q/%q: %w", si.account, si.service, err)
	}

	if err := os.WriteFile(si.path, data, 0600); err != nil {
		return fmt.Errorf("session save %q/%q: %w", si.account, si.service, err)
	}

	return nil
}

// Load resolves (account, service) to a UUID via the index file, then
// retrieves the SessionCredentials from the keyring. It tries the exact service
// first, then falls back to the "*" wildcard. Stale index entries (UUID
// present in index but missing from keyring) are cleaned up automatically.
func (si *SessionIndex) Load() (*api.SessionCredentials, error) {
	idx, err := si.readIndex()
	if err != nil {
		return nil, err
	}

	acct, ok := idx.Accounts[si.account]
	if !ok {
		return nil, fmt.Errorf("session load %q/%q: %w", si.account, si.service, api.ErrKeyNotFound)
	}

	// Resolve UUID: try exact service, fall back to wildcard.
	uuid, ok := acct.Sessions[si.service]
	if !ok {
		uuid, ok = acct.Sessions["*"]
		if !ok {
			return nil, fmt.Errorf("session load %q/%q: %w", si.account, si.service, api.ErrKeyNotFound)
		}
	}

	secret, err := si.kr.Get(keyringService, uuid)
	if err != nil {
		// Stale entry: UUID in index but missing from keyring.
		// Clean up the stale entry and persist the updated index.
		resolvedService := si.service
		if _, exists := acct.Sessions[si.service]; !exists {
			resolvedService = "*"
		}
		delete(acct.Sessions, resolvedService)
		if len(acct.Sessions) == 0 {
			delete(idx.Accounts, si.account)
		} else {
			idx.Accounts[si.account] = acct
		}
		// Best-effort write; the primary error is the missing key.
		_ = si.writeIndex(idx)
		return nil, fmt.Errorf("session load %q/%q: %w", si.account, si.service, api.ErrKeyNotFound)
	}

	var cfg api.SessionCredentials
	if err := json.Unmarshal([]byte(secret), &cfg); err != nil {
		return nil, fmt.Errorf("session load %q/%q: %w", si.account, si.service, err)
	}

	return &cfg, nil
}

// Save stores the given SessionCredentials in the keyring and updates the on-disk
// index file. If no entry exists for (account, service), a new v4 UUID is
// generated as the keyring key. Parent directories for the index file are
// created if they don't exist.
func (si *SessionIndex) Save(session *api.SessionCredentials) error {
	idx, err := si.readIndex()
	if err != nil {
		return err
	}

	acct, ok := idx.Accounts[si.account]
	if !ok {
		acct = AccountEntry{
			Username: si.account,
			Sessions: make(map[string]string),
		}
	}

	// Reuse existing UUID or generate a new one.
	id, ok := acct.Sessions[si.service]
	if !ok {
		id = uuid.New().String()
		acct.Sessions[si.service] = id
	}

	// Marshal session config and store in keyring.
	//nolint:gosec // G117: marshaling SessionCredentials is intentional, tokens are the payload.
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("session save %q/%q: %w", si.account, si.service, err)
	}

	if err := si.kr.Set(keyringService, id, string(data)); err != nil {
		return fmt.Errorf("session save %q/%q: %w", si.account, si.service, err)
	}

	// Update index and persist to disk.
	idx.Accounts[si.account] = acct

	if err := os.MkdirAll(filepath.Dir(si.path), 0700); err != nil {
		return fmt.Errorf("session save %q/%q: %w", si.account, si.service, err)
	}

	if err := si.writeIndex(idx); err != nil {
		return err
	}

	return nil
}

// List returns the account names present in the session index file.
// A missing index file is treated as an empty list (no error).
func (si *SessionIndex) List() ([]string, error) {
	idx, err := si.readIndex()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(idx.Accounts))
	for name := range idx.Accounts {
		names = append(names, name)
	}

	return names, nil
}

// Switch updates the active account on this SessionIndex instance.
func (si *SessionIndex) Switch(account string) error {
	si.account = account
	return nil
}

// Delete removes the session for (account, service) from both the keyring
// and the on-disk index. If the account or session doesn't exist, Delete
// returns nil (idempotent). If the account has no remaining sessions after
// deletion, the account entry is removed entirely.
func (si *SessionIndex) Delete() error {
	idx, err := si.readIndex()
	if err != nil {
		return fmt.Errorf("session delete %q/%q: %w", si.account, si.service, err)
	}

	acct, ok := idx.Accounts[si.account]
	if !ok {
		return nil
	}

	id, ok := acct.Sessions[si.service]
	if !ok {
		return nil
	}

	if err := si.kr.Delete(keyringService, id); err != nil {
		return fmt.Errorf("session delete %q/%q: %w", si.account, si.service, err)
	}

	delete(acct.Sessions, si.service)

	if len(acct.Sessions) == 0 {
		delete(idx.Accounts, si.account)
	} else {
		idx.Accounts[si.account] = acct
	}

	if err := si.writeIndex(idx); err != nil {
		return fmt.Errorf("session delete %q/%q: %w", si.account, si.service, err)
	}

	return nil
}
