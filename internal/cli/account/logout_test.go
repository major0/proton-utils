package accountCmd

import (
	"fmt"
	"strings"
	"testing"

	common "github.com/major0/proton-utils/api"
	cli "github.com/major0/proton-utils/internal/cli"
)

// trackingStore is a SessionStore that tracks Delete calls and can return errors.
type trackingStore struct {
	failingStore
	deleted   bool
	deleteErr error
}

func (s *trackingStore) Delete() error {
	s.deleted = true
	return s.deleteErr
}

// TestLogout_DeletesCookieAndAccountStore verifies that logout deletes both
// the session store and the cookie store.
func TestLogout_DeletesCookieAndAccountStore(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})

	sessionStore := &trackingStore{failingStore: failingStore{err: common.ErrKeyNotFound}}
	rc := &cli.RuntimeContext{
		SessionStore: sessionStore,
		AccountStore: sessionStore,
		CookieStore:  sessionStore,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	var cookieDeleted bool
	logoutCookieDeleteFn = func(_ common.SessionStore) error {
		cookieDeleted = true
		return nil
	}

	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sessionStore.deleted {
		t.Error("session store Delete was not called")
	}
	if !cookieDeleted {
		t.Error("cookie store Delete was not called")
	}
}

// TestLogout_ForceLogoutContinuesOnRestoreFailure verifies that with --force,
// logout continues even when session restore fails.
func TestLogout_ForceLogoutContinuesOnRestoreFailure(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})

	store := &trackingStore{
		failingStore: failingStore{err: fmt.Errorf("disk error")},
	}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = true

	var cookieDeleted bool
	logoutCookieDeleteFn = func(_ common.SessionStore) error {
		cookieDeleted = true
		return nil
	}

	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	if err != nil {
		t.Fatalf("force logout should not fail, got: %v", err)
	}
	if !cookieDeleted {
		t.Error("cookie store Delete was not called during force logout")
	}
}

// TestLogout_CookieStoreDeleteFailureLogged verifies that a cookie store
// delete failure is logged but does not fail the logout.
func TestLogout_CookieStoreDeleteFailureLogged(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})

	sessionStore := &trackingStore{failingStore: failingStore{err: common.ErrKeyNotFound}}
	rc := &cli.RuntimeContext{
		SessionStore: sessionStore,
		AccountStore: sessionStore,
		CookieStore:  sessionStore,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	logoutCookieDeleteFn = func(_ common.SessionStore) error {
		return fmt.Errorf("cookie keyring locked")
	}

	// Logout should succeed even though cookie delete fails.
	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	if err != nil {
		t.Fatalf("logout should succeed despite cookie delete failure, got: %v", err)
	}
	if !sessionStore.deleted {
		t.Error("session store Delete was not called")
	}
}

// TestLogout_RestoreErrorWithoutForce verifies that a non-ErrNotLoggedIn
// restore error is returned when --force is not set.
func TestLogout_RestoreErrorWithoutForce(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})

	rc := &cli.RuntimeContext{
		SessionStore: &failingStore{err: fmt.Errorf("disk error")},
		AccountStore: &failingStore{err: fmt.Errorf("disk error")},
		CookieStore:  &failingStore{err: fmt.Errorf("disk error")},
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	logoutCookieDeleteFn = func(_ common.SessionStore) error { return nil }

	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "disk error") {
		t.Errorf("error = %q, want substring %q", err.Error(), "disk error")
	}
}
