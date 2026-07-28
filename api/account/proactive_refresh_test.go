package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// sessionForManager builds a minimal *api.Session whose Manager() points at
// hostURL. proactiveRefresh only needs session.Manager() to reach the
// httptest server, so no client/auth/keyring setup is required.
func sessionForManager(t *testing.T, hostURL string) *api.Session {
	t.Helper()
	session, _ := api.InitSession(context.Background(), []proton.Option{
		proton.WithHostURL(hostURL),
		proton.WithAppVersion("test@1.0.0"),
	}, nil)
	t.Cleanup(session.Stop)
	return session
}

// TestProactiveRefresh_StaleTriggersCoordinatedRefresh verifies Req 3.1: a
// stale account token on the restore path (LastRefresh age >
// ProactiveRefreshAge) triggers exactly one coordinated refresh via
// RefreshAccountLocked, and the rotated Bearer credentials are persisted to
// the store with a fresh LastRefresh.
//
// Validates: Requirements 3.1
func TestProactiveRefresh_StaleTriggersCoordinatedRefresh(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid        = "uid-restore-stale"
		oldRefresh = "old-refresh"
		oldAccess  = "old-access"
		newRefresh = "new-refresh"
		newAccess  = "new-access"
	)
	var count int32
	srv := bearerRefreshServer(t, &count, oldRefresh, newAccess, newRefresh)
	defer srv.Close()

	oldLast := time.Now().Add(-2 * time.Hour) // > ProactiveRefreshAge (1h)
	config := &api.SessionCredentials{
		UID:          uid,
		AccessToken:  oldAccess,
		RefreshToken: oldRefresh,
		LastRefresh:  oldLast,
	}
	store := &trackingStore{creds: cloneCreds(config)}

	if err := proactiveRefresh(context.Background(), sessionForManager(t, srv.URL), config, store); err != nil {
		t.Fatalf("proactiveRefresh: %v", err)
	}

	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("refresh count = %d, want exactly 1", got)
	}

	saved := store.snapshot()
	if saved.RefreshToken != newRefresh || saved.AccessToken != newAccess {
		t.Fatalf("stored tokens = (%q, %q), want (%q, %q)", saved.AccessToken, saved.RefreshToken, newAccess, newRefresh)
	}
	if !saved.LastRefresh.After(oldLast) {
		t.Fatalf("stored LastRefresh = %v, want advanced past %v", saved.LastRefresh, oldLast)
	}
}

// TestProactiveRefresh_FreshTokenSkips verifies Req 3.2's gate: a fresh
// account token (LastRefresh age < ProactiveRefreshAge) performs no refresh —
// the coordinated refresh is skipped and the store is left untouched.
//
// Validates: Requirements 3.1, 3.2
func TestProactiveRefresh_FreshTokenSkips(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid     = "uid-restore-fresh"
		refresh = "fresh-refresh"
		access  = "fresh-access"
	)
	var count int32
	// wantRefresh is a value the caller never presents; a refresh would be
	// a bug, and the server still counts any hit.
	srv := bearerRefreshServer(t, &count, "unreachable", "x", "y")
	defer srv.Close()

	freshLast := time.Now().Add(-1 * time.Minute) // < ProactiveRefreshAge (1h)
	config := &api.SessionCredentials{
		UID:          uid,
		AccessToken:  access,
		RefreshToken: refresh,
		LastRefresh:  freshLast,
	}
	store := &trackingStore{creds: cloneCreds(config)}

	if err := proactiveRefresh(context.Background(), sessionForManager(t, srv.URL), config, store); err != nil {
		t.Fatalf("proactiveRefresh: %v", err)
	}

	if got := atomic.LoadInt32(&count); got != 0 {
		t.Fatalf("refresh count = %d, want 0 (fresh token must not refresh)", got)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (fresh token must not persist)", store.saves())
	}
}
