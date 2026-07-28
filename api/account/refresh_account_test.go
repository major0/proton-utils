package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// trackingStore is a concurrency-safe api.SessionStore that records Load and
// Save counts. The counts let the adopt/transient tests assert that no
// unnecessary write occurred, and the mutex makes the store safe for the
// rapid concurrency property (memSessionStore, used elsewhere, is neither
// counted nor mutex-guarded). Credentials are deep-copied on the way in and
// out so callers cannot mutate stored state through an aliased slice.
type trackingStore struct {
	mu        sync.Mutex
	creds     *api.SessionCredentials
	loadCount int
	saveCount int
}

func (s *trackingStore) Load() (*api.SessionCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCount++
	if s.creds == nil {
		return nil, api.ErrKeyNotFound
	}
	return cloneCreds(s.creds), nil
}

func (s *trackingStore) Save(c *api.SessionCredentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCount++
	s.creds = cloneCreds(c)
	return nil
}

func (s *trackingStore) Delete() error           { s.creds = nil; return nil }
func (s *trackingStore) List() ([]string, error) { return nil, nil }
func (s *trackingStore) Switch(string) error     { return nil }

func (s *trackingStore) saves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCount
}

func (s *trackingStore) snapshot() *api.SessionCredentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds == nil {
		return nil
	}
	return cloneCreds(s.creds)
}

// cloneCreds returns a deep copy of c so stored state cannot be mutated
// through an aliased Cookies slice.
func cloneCreds(c *api.SessionCredentials) *api.SessionCredentials {
	cc := *c
	if c.Cookies != nil {
		cc.Cookies = make([]api.SerialCookie, len(c.Cookies))
		copy(cc.Cookies, c.Cookies)
	}
	return &cc
}

// withAccountHost overrides the account service host in the global registry
// for the duration of the test and restores it afterward. The cookie refresh
// path (refreshCookieLocked) reads api.Services["account"].Host directly, so
// cookie-mode tests must point it at their httptest server. These tests must
// not run in parallel because they mutate global registry state.
func withAccountHost(t *testing.T, host string) {
	t.Helper()
	orig := api.Services["account"]
	svc := orig
	svc.Host = host
	api.Services["account"] = svc
	t.Cleanup(func() { api.Services["account"] = orig })
}

// bearerRefreshServer returns an httptest server that answers
// /auth/v4/refresh: a request carrying wantRefresh rotates to
// newAccess/newRefresh (Code 1000); any other refresh token is rejected with
// 422 (a dead rotating credential). It increments *count on every refresh
// call so tests can assert exactly-once rotation.
func bearerRefreshServer(t *testing.T, count *int32, wantRefresh, newAccess, newRefresh string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/auth/v4/refresh" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
			return
		}
		atomic.AddInt32(count, 1)
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

func bearerManager(t *testing.T, hostURL string) *proton.Manager {
	t.Helper()
	mgr := proton.New(
		proton.WithHostURL(hostURL),
		proton.WithAppVersion("test@1.0.0"),
	)
	t.Cleanup(mgr.Close)
	return mgr
}

// --- Bearer mode ---

// TestRefreshAccountLocked_BearerRotatesAndPersists verifies P4: when the
// refresh token is unchanged under the lock, the coordinated refresh rotates
// the Bearer tokens, persists them, and advances LastRefresh.
//
// Validates: Requirements 1.4
func TestRefreshAccountLocked_BearerRotatesAndPersists(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid        = "uid-bearer"
		oldRefresh = "old-refresh"
		oldAccess  = "old-access"
		newRefresh = "new-refresh"
		newAccess  = "new-access"
	)
	var count int32
	srv := bearerRefreshServer(t, &count, oldRefresh, newAccess, newRefresh)
	defer srv.Close()

	oldLast := time.Now().Add(-2 * time.Hour)
	store := &trackingStore{creds: &api.SessionCredentials{
		UID:          uid,
		AccessToken:  oldAccess,
		RefreshToken: oldRefresh,
		LastRefresh:  oldLast,
	}}

	got, err := RefreshAccountLocked(context.Background(), bearerManager(t, srv.URL), store, oldRefresh)
	if err != nil {
		t.Fatalf("RefreshAccountLocked: %v", err)
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Fatalf("refresh count = %d, want 1", count)
	}
	if got.RefreshToken != newRefresh || got.AccessToken != newAccess {
		t.Fatalf("returned tokens = (%q, %q), want (%q, %q)", got.AccessToken, got.RefreshToken, newAccess, newRefresh)
	}
	if !got.LastRefresh.After(oldLast) {
		t.Fatalf("LastRefresh = %v, want advanced past %v", got.LastRefresh, oldLast)
	}

	saved := store.snapshot()
	if saved.RefreshToken != newRefresh || saved.AccessToken != newAccess {
		t.Fatalf("stored tokens = (%q, %q), want (%q, %q)", saved.AccessToken, saved.RefreshToken, newAccess, newRefresh)
	}
	if !saved.LastRefresh.After(oldLast) {
		t.Fatalf("stored LastRefresh = %v, want advanced past %v", saved.LastRefresh, oldLast)
	}
}

// TestRefreshAccountLocked_BearerAdopts verifies P1/P2: when a peer already
// rotated the refresh token under the lock (stored token != startedWith), the
// refresher adopts the reloaded credentials and performs no refresh of its
// own.
//
// Validates: Requirements 1.3
func TestRefreshAccountLocked_BearerAdopts(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid         = "uid-adopt"
		startedWith = "original-refresh"
		peerRefresh = "peer-rotated-refresh"
		peerAccess  = "peer-rotated-access"
	)
	var count int32
	// wantRefresh is set to a value the caller never presents; any refresh
	// call would therefore be a bug. The server still counts the hit.
	srv := bearerRefreshServer(t, &count, "unreachable", "x", "y")
	defer srv.Close()

	store := &trackingStore{creds: &api.SessionCredentials{
		UID:          uid,
		AccessToken:  peerAccess,
		RefreshToken: peerRefresh,
		LastRefresh:  time.Now(),
	}}

	got, err := RefreshAccountLocked(context.Background(), bearerManager(t, srv.URL), store, startedWith)
	if err != nil {
		t.Fatalf("RefreshAccountLocked: %v", err)
	}
	if atomic.LoadInt32(&count) != 0 {
		t.Fatalf("refresh count = %d, want 0 (adopt path must not refresh)", count)
	}
	if got.RefreshToken != peerRefresh {
		t.Fatalf("adopted refresh token = %q, want %q", got.RefreshToken, peerRefresh)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (adopt path must not persist)", store.saves())
	}
}

// TestRefreshAccountLocked_BearerDeauthed verifies that a Bearer refresh
// rejected with HTTP 422 (dead refresh token) is reported as
// ErrAccountDeauthed and the store is left unchanged.
//
// Validates: Requirements 2.4
func TestRefreshAccountLocked_BearerDeauthed(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "uid-deauth"
	var count int32
	// The caller presents "dead-refresh" but the server only accepts
	// "some-other" — so the presented token draws a 422.
	srv := bearerRefreshServer(t, &count, "some-other", "a", "b")
	defer srv.Close()

	store := &trackingStore{creds: &api.SessionCredentials{
		UID:          uid,
		AccessToken:  "dead-access",
		RefreshToken: "dead-refresh",
		LastRefresh:  time.Now(),
	}}

	_, err := RefreshAccountLocked(context.Background(), bearerManager(t, srv.URL), store, "dead-refresh")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrAccountDeauthed) {
		t.Fatalf("error %v does not wrap ErrAccountDeauthed", err)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (deauth must not persist)", store.saves())
	}
	if got := store.snapshot(); got.RefreshToken != "dead-refresh" {
		t.Fatalf("stored refresh token = %q, want unchanged %q", got.RefreshToken, "dead-refresh")
	}
}

// TestRefreshAccountLocked_BearerTransient verifies that a transient Bearer
// refresh failure (HTTP 500) is returned as a non-deauth error and the store
// is left unchanged for retry on the next tick.
//
// Validates: Requirements 2.3
func TestRefreshAccountLocked_BearerTransient(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "uid-transient"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 2000, "Error": "server error"})
	}))
	defer srv.Close()

	oldLast := time.Now().Add(-2 * time.Hour)
	store := &trackingStore{creds: &api.SessionCredentials{
		UID:          uid,
		AccessToken:  "acc",
		RefreshToken: "ref",
		LastRefresh:  oldLast,
	}}

	_, err := RefreshAccountLocked(context.Background(), bearerManager(t, srv.URL), store, "ref")
	if err == nil {
		t.Fatal("expected transient error, got nil")
	}
	if errors.Is(err, ErrAccountDeauthed) {
		t.Fatalf("transient error must not be ErrAccountDeauthed: %v", err)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (transient must not persist)", store.saves())
	}
	if got := store.snapshot(); got.RefreshToken != "ref" || !got.LastRefresh.Equal(oldLast) {
		t.Fatalf("store changed on transient failure: %+v", got)
	}
}

// --- Cookie mode (Req 1.7) ---

// cookieRefreshServer returns an httptest server that answers /auth/refresh by
// setting rotated AUTH-/REFRESH-<uid> cookies (Code 1000) and incrementing
// *count. It mirrors the Bearer harness for the cookie auth style.
func cookieRefreshServer(t *testing.T, count *int32, uid, newAuth, newRefresh string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/refresh" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
			return
		}
		atomic.AddInt32(count, 1)
		http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: newAuth, Path: "/"})       //nolint:gosec // G124: test cookie
		http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: newRefresh, Path: "/"}) //nolint:gosec // G124: test cookie
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
}

// cookieCreds builds cookie-auth credentials carrying AUTH-/REFRESH-<uid>
// cookies with the given refresh value. Domain/Path are left blank so the
// non-Proton test host stores them host-only at path "/".
func cookieCreds(uid, authVal, refreshVal string, last time.Time) *api.SessionCredentials {
	return &api.SessionCredentials{
		UID:        uid,
		CookieAuth: true,
		Cookies: []api.SerialCookie{
			{Name: "AUTH-" + uid, Value: authVal},
			{Name: "REFRESH-" + uid, Value: refreshVal},
		},
		LastRefresh: last,
	}
}

// TestRefreshAccountLocked_CookieRotatesAndPersists verifies P4 for cookie
// auth: RefreshCookies rotates the AUTH-/REFRESH-<uid> cookies, which are
// persisted with a fresh LastRefresh.
//
// Validates: Requirements 1.7
func TestRefreshAccountLocked_CookieRotatesAndPersists(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid        = "uid-cookie"
		oldRefresh = "old-refresh-cookie"
		newAuth    = "new-auth-cookie"
		newRefresh = "new-refresh-cookie"
	)
	var count int32
	srv := cookieRefreshServer(t, &count, uid, newAuth, newRefresh)
	defer srv.Close()
	withAccountHost(t, srv.URL)

	oldLast := time.Now().Add(-2 * time.Hour)
	store := &trackingStore{creds: cookieCreds(uid, "old-auth-cookie", oldRefresh, oldLast)}

	got, err := RefreshAccountLocked(context.Background(), nil, store, oldRefresh)
	if err != nil {
		t.Fatalf("RefreshAccountLocked: %v", err)
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Fatalf("refresh count = %d, want 1", count)
	}
	if rc := rotatingCredential(got); rc != newRefresh {
		t.Fatalf("returned REFRESH cookie = %q, want %q", rc, newRefresh)
	}
	if !got.LastRefresh.After(oldLast) {
		t.Fatalf("LastRefresh = %v, want advanced past %v", got.LastRefresh, oldLast)
	}

	saved := store.snapshot()
	if rc := rotatingCredential(saved); rc != newRefresh {
		t.Fatalf("stored REFRESH cookie = %q, want %q", rc, newRefresh)
	}
	if !saved.LastRefresh.After(oldLast) {
		t.Fatalf("stored LastRefresh = %v, want advanced past %v", saved.LastRefresh, oldLast)
	}
}

// TestRefreshAccountLocked_CookieAdopts verifies P1/P2 for cookie auth: when
// the stored REFRESH-<uid> cookie was already rotated by a peer, the refresher
// adopts it and performs no refresh.
//
// Validates: Requirements 1.7, 1.3
func TestRefreshAccountLocked_CookieAdopts(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid         = "uid-cookie-adopt"
		startedWith = "original-refresh-cookie"
		peerRefresh = "peer-rotated-cookie"
	)
	var count int32
	srv := cookieRefreshServer(t, &count, uid, "x", "y")
	defer srv.Close()
	withAccountHost(t, srv.URL)

	store := &trackingStore{creds: cookieCreds(uid, "auth", peerRefresh, time.Now())}

	got, err := RefreshAccountLocked(context.Background(), nil, store, startedWith)
	if err != nil {
		t.Fatalf("RefreshAccountLocked: %v", err)
	}
	if atomic.LoadInt32(&count) != 0 {
		t.Fatalf("refresh count = %d, want 0 (adopt path must not refresh)", count)
	}
	if rc := rotatingCredential(got); rc != peerRefresh {
		t.Fatalf("adopted REFRESH cookie = %q, want %q", rc, peerRefresh)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (adopt path must not persist)", store.saves())
	}
}

// TestRefreshAccountLocked_CookieDeauthed verifies that a cookie refresh
// rejected with HTTP 422 is reported as ErrAccountDeauthed.
//
// Validates: Requirements 2.4
func TestRefreshAccountLocked_CookieDeauthed(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "uid-cookie-deauth"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/refresh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 10013, "Error": "Invalid refresh token"})
	}))
	defer srv.Close()
	withAccountHost(t, srv.URL)

	store := &trackingStore{creds: cookieCreds(uid, "auth", "dead-refresh-cookie", time.Now())}

	_, err := RefreshAccountLocked(context.Background(), nil, store, "dead-refresh-cookie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrAccountDeauthed) {
		t.Fatalf("error %v does not wrap ErrAccountDeauthed", err)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (deauth must not persist)", store.saves())
	}
}

// --- Property: rapid concurrency (P1) ---

// TestRefreshAccountLocked_ConcurrentSingleRotation is the in-process proxy for
// the cross-process serialization guarantee (P1): N goroutines calling
// RefreshAccountLocked with the same starting Bearer token yield exactly one
// server-side rotation and a consistent final stored token — the rest observe
// the peer's rotation under the lock and adopt it.
//
// Validates: Requirements 1.1, 1.2, 1.3, 1.4
func TestRefreshAccountLocked_ConcurrentSingleRotation(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		newAccess  = "rotated-access-token"
		newRefresh = "rotated-refresh-token"
	)

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(2, 8).Draw(rt, "goroutines")
		uid := rapid.StringMatching(`[a-zA-Z0-9]{1,16}`).Draw(rt, "uid")
		startTok := "start-" + rapid.StringMatching(`[a-zA-Z0-9]{1,16}`).Draw(rt, "startToken")

		var count int32
		srv := bearerRefreshServer(t, &count, startTok, newAccess, newRefresh)
		defer srv.Close()
		mgr := proton.New(proton.WithHostURL(srv.URL), proton.WithAppVersion("test@1.0.0"))
		defer mgr.Close()

		store := &trackingStore{creds: &api.SessionCredentials{
			UID:          uid,
			AccessToken:  "start-access",
			RefreshToken: startTok,
			LastRefresh:  time.Now().Add(-2 * time.Hour),
		}}

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			results []*api.SessionCredentials
			errs    []error
		)
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				got, err := RefreshAccountLocked(context.Background(), mgr, store, startTok)
				mu.Lock()
				results = append(results, got)
				errs = append(errs, err)
				mu.Unlock()
			}()
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				rt.Fatalf("RefreshAccountLocked returned error: %v", err)
			}
		}
		if got := atomic.LoadInt32(&count); got != 1 {
			rt.Fatalf("server rotations = %d, want exactly 1", got)
		}
		if final := store.snapshot(); final.RefreshToken != newRefresh {
			rt.Fatalf("final stored refresh token = %q, want %q", final.RefreshToken, newRefresh)
		}
		for _, got := range results {
			if got.RefreshToken != newRefresh {
				rt.Fatalf("goroutine observed refresh token = %q, want %q", got.RefreshToken, newRefresh)
			}
		}
	})
}
