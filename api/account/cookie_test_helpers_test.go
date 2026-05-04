package account

import (
	"github.com/major0/proton-cli/api"
	"pgregory.net/rapid"
)

// cookieMockStore is a SessionStore that tracks Save calls and supports
// configurable Load behavior for cookie session testing.
type cookieMockStore struct {
	config    *api.SessionCredentials
	saved     *api.SessionCredentials
	saveCount int
	loadErr   error
}

func (s *cookieMockStore) Load() (*api.SessionCredentials, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.config == nil {
		return nil, api.ErrKeyNotFound
	}
	cfg := *s.config
	return &cfg, nil
}

func (s *cookieMockStore) Save(cfg *api.SessionCredentials) error {
	s.saved = cfg
	s.saveCount++
	return nil
}

func (s *cookieMockStore) Delete() error           { return nil }
func (s *cookieMockStore) List() ([]string, error) { return nil, nil }
func (s *cookieMockStore) Switch(string) error     { return nil }

// genCookieName generates a random cookie name using only safe characters.
func genCookieName(t *rapid.T, label string) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	n := rapid.IntRange(1, 32).Draw(t, label+"Len")
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rapid.IntRange(0, len(chars)-1).Draw(t, label+"Char")]
	}
	return string(b)
}

// genCookieValue generates a random cookie value using safe characters.
func genCookieValue(t *rapid.T, label string) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-."
	n := rapid.IntRange(0, 64).Draw(t, label+"Len")
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rapid.IntRange(0, len(chars)-1).Draw(t, label+"Char")]
	}
	return string(b)
}
