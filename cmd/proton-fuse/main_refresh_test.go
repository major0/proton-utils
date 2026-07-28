//go:build linux

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// fakeStore is a concurrency-safe api.SessionStore double for the daemon
// refresh tests. It records Load/Save counts so tests can assert whether the
// gate skipped a refresh (no Save) or a refresh persisted rotated tokens.
// Credentials are deep-copied in and out so a caller cannot mutate stored
// state through an aliased Cookies slice.
type fakeStore struct {
	mu        sync.Mutex
	creds     *api.SessionCredentials
	loadCount int
	saveCount int
}

func (s *fakeStore) Load() (*api.SessionCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCount++
	if s.creds == nil {
		return nil, api.ErrKeyNotFound
	}
	return cloneStoreCreds(s.creds), nil
}

func (s *fakeStore) Save(c *api.SessionCredentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCount++
	s.creds = cloneStoreCreds(c)
	return nil
}

func (s *fakeStore) Delete() error           { s.creds = nil; return nil }
func (s *fakeStore) List() ([]string, error) { return nil, nil }
func (s *fakeStore) Switch(string) error     { return nil }

func (s *fakeStore) saves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCount
}

func (s *fakeStore) snapshot() *api.SessionCredentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds == nil {
		return nil
	}
	return cloneStoreCreds(s.creds)
}

// cloneStoreCreds returns a deep copy of c so stored state cannot be mutated
// through an aliased Cookies slice.
func cloneStoreCreds(c *api.SessionCredentials) *api.SessionCredentials {
	cc := *c
	if c.Cookies != nil {
		cc.Cookies = make([]api.SerialCookie, len(c.Cookies))
		copy(cc.Cookies, c.Cookies)
	}
	return &cc
}

// bearerRefreshServer returns an httptest server that answers
// /auth/v4/refresh: a request carrying wantRefresh rotates to
// newAccess/newRefresh (Code 1000); any other refresh token is rejected with
// 422 (a dead rotating credential). It increments *count under mu on every
// refresh call so tests can assert how many refresh attempts the daemon made.
func bearerRefreshServer(t *testing.T, mu *sync.Mutex, count *int, wantRefresh, newAccess, newRefresh string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/auth/v4/refresh" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
			return
		}
		mu.Lock()
		*count++
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		var req struct {
			UID          string
			RefreshToken string
		}
		_ = json.Unmarshal(body, &req)
		if req.RefreshToken != wantRefresh {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 10013, "Error": "Invalid refresh token"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":         1000,
			"UID":          req.UID,
			"AccessToken":  newAccess,
			"RefreshToken": newRefresh,
		})
	}))
}

// transientRefreshServer returns an httptest server that always answers
// /auth/v4/refresh with HTTP 500, incrementing *count on each hit. It models
// a transient backend failure that the daemon must log and retry.
func transientRefreshServer(t *testing.T, mu *sync.Mutex, count *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/auth/v4/refresh" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
			return
		}
		mu.Lock()
		*count++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 2000, "Error": "server error"})
	}))
}

func testManager(t *testing.T, hostURL string) *proton.Manager {
	t.Helper()
	mgr := proton.New(
		proton.WithHostURL(hostURL),
		proton.WithAppVersion("test@1.0.0"),
	)
	t.Cleanup(mgr.Close)
	return mgr
}

// TestRefreshAccountToken_GateHonored_Fresh verifies Req 2.2: when the account
// token is fresh (LastRefresh recent, age < ProactiveRefreshAge), the periodic
// refresh is skipped — no server refresh call and no store write.
//
// Validates: Requirements 2.1, 2.2
func TestRefreshAccountToken_GateHonored_Fresh(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var (
		mu    sync.Mutex
		count int
	)
	srv := bearerRefreshServer(t, &mu, &count, "any", "a", "b")
	defer srv.Close()

	store := &fakeStore{creds: &api.SessionCredentials{
		UID:          "uid-fresh",
		AccessToken:  "acc",
		RefreshToken: "ref",
		LastRefresh:  time.Now(), // age well under ProactiveRefreshAge (1h)
	}}

	deauthed := refreshAccountToken(context.Background(), testManager(t, srv.URL), store)
	if deauthed {
		t.Fatal("fresh token reported deauthed, want false")
	}
	mu.Lock()
	got := count
	mu.Unlock()
	if got != 0 {
		t.Fatalf("server refresh count = %d, want 0 (fresh token must not refresh)", got)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (gated refresh must not persist)", store.saves())
	}
}

// TestRefreshAccountToken_GateHonored_Stale verifies Req 2.2: when the account
// token is stale (age > ProactiveRefreshAge), a coordinated refresh is
// attempted, rotates the tokens, and persists them.
//
// Validates: Requirements 2.1, 2.2
func TestRefreshAccountToken_GateHonored_Stale(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		oldRefresh = "old-refresh"
		newAccess  = "new-access"
		newRefresh = "new-refresh"
	)
	var (
		mu    sync.Mutex
		count int
	)
	srv := bearerRefreshServer(t, &mu, &count, oldRefresh, newAccess, newRefresh)
	defer srv.Close()

	oldLast := time.Now().Add(-2 * time.Hour) // age > ProactiveRefreshAge
	store := &fakeStore{creds: &api.SessionCredentials{
		UID:          "uid-stale",
		AccessToken:  "old-access",
		RefreshToken: oldRefresh,
		LastRefresh:  oldLast,
	}}

	deauthed := refreshAccountToken(context.Background(), testManager(t, srv.URL), store)
	if deauthed {
		t.Fatal("stale-token refresh reported deauthed, want false")
	}
	mu.Lock()
	got := count
	mu.Unlock()
	if got != 1 {
		t.Fatalf("server refresh count = %d, want 1 (stale token must refresh)", got)
	}
	saved := store.snapshot()
	if saved.RefreshToken != newRefresh || saved.AccessToken != newAccess {
		t.Fatalf("stored tokens = (%q, %q), want (%q, %q)", saved.AccessToken, saved.RefreshToken, newAccess, newRefresh)
	}
	if !saved.LastRefresh.After(oldLast) {
		t.Fatalf("stored LastRefresh = %v, want advanced past %v", saved.LastRefresh, oldLast)
	}
}

// TestRefreshLoopLatch_DeauthStopsRetries verifies Req 2.4: once a refresh is
// rejected as ErrAccountDeauthed, the loop's accountDeauthed latch stops
// further account-token refresh attempts on subsequent ticks. It replicates
// the latch logic from startRefreshLoop (share refresh continues
// independently and is not exercised here) and asserts the server saw exactly
// one refresh attempt across several ticks.
//
// Validates: Requirements 2.4
func TestRefreshLoopLatch_DeauthStopsRetries(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var (
		mu    sync.Mutex
		count int
	)
	// The stored refresh token "dead-refresh" is never accepted (server only
	// honors "unreachable"), so every attempt draws a 422 deauth.
	srv := bearerRefreshServer(t, &mu, &count, "unreachable", "a", "b")
	defer srv.Close()

	store := &fakeStore{creds: &api.SessionCredentials{
		UID:          "uid-deauth",
		AccessToken:  "dead-access",
		RefreshToken: "dead-refresh",
		LastRefresh:  time.Now().Add(-2 * time.Hour),
	}}
	mgr := testManager(t, srv.URL)

	// Replicate the startRefreshLoop latch: refresh only while not latched.
	accountDeauthed := false
	const ticks = 3
	for range ticks {
		if !accountDeauthed {
			accountDeauthed = refreshAccountToken(context.Background(), mgr, store)
		}
	}

	if !accountDeauthed {
		t.Fatal("accountDeauthed = false, want true after 422 rejection")
	}
	mu.Lock()
	got := count
	mu.Unlock()
	if got != 1 {
		t.Fatalf("server refresh count = %d, want 1 (latch must stop retries after deauth)", got)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (deauth must not persist)", store.saves())
	}
}

// TestRefreshLoopLatch_TransientKeepsLoopAlive verifies Req 2.3: a transient
// refresh failure (HTTP 500) does not latch and does not crash the loop — the
// refresh is retried on each subsequent tick. It replicates the latch logic
// from startRefreshLoop and asserts the server was hit once per tick with the
// store left unchanged.
//
// Validates: Requirements 2.3
func TestRefreshLoopLatch_TransientKeepsLoopAlive(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var (
		mu    sync.Mutex
		count int
	)
	srv := transientRefreshServer(t, &mu, &count)
	defer srv.Close()

	oldLast := time.Now().Add(-2 * time.Hour)
	store := &fakeStore{creds: &api.SessionCredentials{
		UID:          "uid-transient",
		AccessToken:  "acc",
		RefreshToken: "ref",
		LastRefresh:  oldLast,
	}}
	mgr := testManager(t, srv.URL)

	accountDeauthed := false
	const ticks = 3
	for range ticks {
		if !accountDeauthed {
			accountDeauthed = refreshAccountToken(context.Background(), mgr, store)
		}
	}

	if accountDeauthed {
		t.Fatal("accountDeauthed = true, want false (transient failure must not latch)")
	}
	mu.Lock()
	got := count
	mu.Unlock()
	if got != ticks {
		t.Fatalf("server refresh count = %d, want %d (transient must retry each tick)", got, ticks)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (transient must not persist)", store.saves())
	}
	if s := store.snapshot(); s.RefreshToken != "ref" || !s.LastRefresh.Equal(oldLast) {
		t.Fatalf("store changed on transient failure: %+v", s)
	}
}

// TestRefreshAccountToken_CookieGateHonored_Fresh verifies Req 2.2 for the
// cookie auth style: a fresh cookie session (age < CookieRefreshAge) is gated
// off by the NeedsCookieRefresh branch, so no refresh is attempted. The
// deauth/transient cookie paths are covered at the api/account layer
// (task 2.2); here only the daemon's style-aware gate needs coverage.
//
// Validates: Requirements 2.2
func TestRefreshAccountToken_CookieGateHonored_Fresh(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "uid-cookie-fresh"
	store := &fakeStore{creds: &api.SessionCredentials{
		UID:        uid,
		CookieAuth: true,
		Cookies: []api.SerialCookie{
			{Name: "AUTH-" + uid, Value: "auth"},
			{Name: "REFRESH-" + uid, Value: "refresh"},
		},
		LastRefresh: time.Now(), // age well under CookieRefreshAge (1h)
	}}

	// mgr is unused on the gated path (refresh is skipped before dispatch).
	deauthed := refreshAccountToken(context.Background(), nil, store)
	if deauthed {
		t.Fatal("fresh cookie session reported deauthed, want false")
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (fresh cookie session must not refresh)", store.saves())
	}
}
