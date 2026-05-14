package keyring

import (
	"errors"
	"fmt"
	"testing"
)

// MockKeyring is an in-memory Keyring implementation for testing.
// It stores values keyed by "service/account". Reusable by session store tests.
type MockKeyring struct {
	store map[string]string
	// ErrGet, ErrSet, ErrDelete allow injecting errors for specific calls.
	ErrGet    error
	ErrSet    error
	ErrDelete error
}

// NewMockKeyring returns a ready-to-use MockKeyring.
func NewMockKeyring() *MockKeyring {
	return &MockKeyring{store: make(map[string]string)}
}

func mockKey(service, account string) string {
	return service + "/" + account
}

// Get retrieves a value or returns an error if not found.
func (m *MockKeyring) Get(service, account string) (string, error) {
	if m.ErrGet != nil {
		return "", m.ErrGet
	}
	v, ok := m.store[mockKey(service, account)]
	if !ok {
		return "", fmt.Errorf("secret not found in keyring")
	}
	return v, nil
}

// Set stores a value.
func (m *MockKeyring) Set(service, account, password string) error {
	if m.ErrSet != nil {
		return m.ErrSet
	}
	m.store[mockKey(service, account)] = password
	return nil
}

// Delete removes a value.
func (m *MockKeyring) Delete(service, account string) error {
	if m.ErrDelete != nil {
		return m.ErrDelete
	}
	delete(m.store, mockKey(service, account))
	return nil
}

// Compile-time interface checks.
var (
	_ Keyring = (*SystemKeyring)(nil)
	_ Keyring = (*MockKeyring)(nil)
)

func TestMockKeyring_SetAndGet(t *testing.T) {
	var kr Keyring = NewMockKeyring()

	if err := kr.Set("svc", "acct", "secret123"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := kr.Get("svc", "acct")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "secret123" {
		t.Errorf("Get = %q, want %q", got, "secret123")
	}
}

func TestMockKeyring_GetNotFound(t *testing.T) {
	var kr Keyring = NewMockKeyring()

	_, err := kr.Get("svc", "missing")
	if err == nil {
		t.Fatal("Get on missing key: expected error, got nil")
	}
}

func TestMockKeyring_Delete(t *testing.T) {
	var kr Keyring = NewMockKeyring()

	if err := kr.Set("svc", "acct", "secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := kr.Delete("svc", "acct"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := kr.Get("svc", "acct")
	if err == nil {
		t.Fatal("Get after Delete: expected error, got nil")
	}
}

func TestMockKeyring_OverwriteValue(t *testing.T) {
	var kr Keyring = NewMockKeyring()

	if err := kr.Set("svc", "acct", "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := kr.Set("svc", "acct", "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	got, err := kr.Get("svc", "acct")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v2" {
		t.Errorf("Get = %q, want %q", got, "v2")
	}
}

func TestMockKeyring_IsolatesByServiceAndAccount(t *testing.T) {
	var kr Keyring = NewMockKeyring()

	if err := kr.Set("svc1", "acct", "a"); err != nil {
		t.Fatalf("Set svc1: %v", err)
	}
	if err := kr.Set("svc2", "acct", "b"); err != nil {
		t.Fatalf("Set svc2: %v", err)
	}

	got1, _ := kr.Get("svc1", "acct")
	got2, _ := kr.Get("svc2", "acct")
	if got1 != "a" || got2 != "b" {
		t.Errorf("isolation failed: svc1=%q svc2=%q", got1, got2)
	}
}

func TestMockKeyring_ErrorInjection(t *testing.T) {
	injected := fmt.Errorf("keyring unavailable")

	t.Run("Get error", func(t *testing.T) {
		mk := NewMockKeyring()
		mk.ErrGet = injected
		var kr Keyring = mk

		_, err := kr.Get("svc", "acct")
		if !errors.Is(err, injected) {
			t.Errorf("Get error = %v, want %v", err, injected)
		}
	})

	t.Run("Set error", func(t *testing.T) {
		mk := NewMockKeyring()
		mk.ErrSet = injected
		var kr Keyring = mk

		err := kr.Set("svc", "acct", "pw")
		if !errors.Is(err, injected) {
			t.Errorf("Set error = %v, want %v", err, injected)
		}
	})

	t.Run("Delete error", func(t *testing.T) {
		mk := NewMockKeyring()
		mk.ErrDelete = injected
		var kr Keyring = mk

		err := kr.Delete("svc", "acct")
		if !errors.Is(err, injected) {
			t.Errorf("Delete error = %v, want %v", err, injected)
		}
	})
}

func TestMockKeyring_DeleteNonexistent(t *testing.T) {
	kr := NewMockKeyring()
	// Deleting a key that doesn't exist should not error (idempotent).
	if err := kr.Delete("svc", "nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestMockKeyring_MultipleServices(t *testing.T) {
	tests := []struct {
		name    string
		service string
		account string
		secret  string
	}{
		{"svc1/acct1", "svc1", "acct1", "s1"},
		{"svc1/acct2", "svc1", "acct2", "s2"},
		{"svc2/acct1", "svc2", "acct1", "s3"},
	}

	kr := NewMockKeyring()
	for _, tt := range tests {
		if err := kr.Set(tt.service, tt.account, tt.secret); err != nil {
			t.Fatalf("Set %s: %v", tt.name, err)
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kr.Get(tt.service, tt.account)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tt.secret {
				t.Errorf("Get = %q, want %q", got, tt.secret)
			}
		})
	}
}
