package account

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// memSessionStore is an in-memory api.SessionStore for tests.
type memSessionStore struct {
	creds *api.SessionCredentials
}

func (m *memSessionStore) Load() (*api.SessionCredentials, error) {
	if m.creds == nil {
		return nil, api.ErrKeyNotFound
	}
	c := *m.creds
	return &c, nil
}

func (m *memSessionStore) Save(c *api.SessionCredentials) error {
	cc := *c
	m.creds = &cc
	return nil
}

func (m *memSessionStore) Delete() error           { m.creds = nil; return nil }
func (m *memSessionStore) List() ([]string, error) { return nil, nil }
func (m *memSessionStore) Switch(string) error     { return nil }

// TestSessionFromCredentialsPersistsRotatedToken validates the ordering fix:
// when the access token is expired, the first GetUser during restore triggers
// an auth refresh (which rotates the refresh token). Because the persisting
// auth handler is now attached BEFORE that GetUser, the rotated refresh token
// must be written back to the store. Before the fix the handler was attached
// afterward and the rotation was lost, leaving a stale (server-invalidated)
// refresh token that de-authed the next process.
func TestSessionFromCredentialsPersistsRotatedToken(t *testing.T) {
	const (
		uid        = "test-uid"
		oldAccess  = "old-access"
		oldRefresh = "old-refresh"
		newAccess  = "new-access"
		newRefresh = "new-refresh"
	)

	var refreshCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/refresh":
			body, _ := io.ReadAll(r.Body)
			var req struct {
				UID          string
				RefreshToken string
			}
			_ = json.Unmarshal(body, &req)
			if req.RefreshToken != oldRefresh {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 10013, "Error": "Invalid refresh token"})
				return
			}
			refreshCount++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code":         1000,
				"UID":          uid,
				"AccessToken":  newAccess,
				"RefreshToken": newRefresh,
			})

		case "/core/v4/users":
			// The expired access token yields 401; go-proton-api then
			// refreshes and retries with the rotated access token.
			if r.Header.Get("Authorization") != "Bearer "+newAccess {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "expired access token"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000,
				"User": map[string]any{"ID": "user-1"},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer srv.Close()

	store := &memSessionStore{creds: &api.SessionCredentials{
		UID:          uid,
		AccessToken:  oldAccess,
		RefreshToken: oldRefresh,
	}}

	options := []proton.Option{
		proton.WithHostURL(srv.URL),
		proton.WithAppVersion("test@1.0.0"),
	}

	cfg := &api.SessionCredentials{
		UID:          uid,
		AccessToken:  oldAccess,
		RefreshToken: oldRefresh,
	}

	session, err := SessionFromCredentials(context.Background(), options, cfg, nil, store)
	if err != nil {
		t.Fatalf("SessionFromCredentials: %v", err)
	}
	if session == nil {
		t.Fatal("nil session")
	}

	if refreshCount != 1 {
		t.Fatalf("expected exactly one refresh, got %d", refreshCount)
	}

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("store load: %v", err)
	}
	if saved.RefreshToken != newRefresh {
		t.Fatalf("store refresh token = %q, want %q (rotated token was not persisted)", saved.RefreshToken, newRefresh)
	}
	if saved.AccessToken != newAccess {
		t.Fatalf("store access token = %q, want %q", saved.AccessToken, newAccess)
	}
}
