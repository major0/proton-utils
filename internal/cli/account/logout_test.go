package accountCmd

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/quick"
	"time"

	common "github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/account"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/major0/proton-utils/internal/keyring"
	"github.com/spf13/cobra"
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

// TestLogout_PurgesAllLocalSessionState verifies the NEW account-wide cleanup
// semantics (design Change 3): after a successful revoke, logout purges EVERY
// local session slot for the account via the accountPurger (DeleteAllSessions),
// not just the current service slot plus cookie. The restore and revoke are
// injected via their seams so the path runs without a network round-trip.
func TestLogout_PurgesAllLocalSessionState(t *testing.T) {
	origForce := authLogoutForce
	origRestore := logoutRestoreFn
	origRevoke := logoutRevokeFn
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutRestoreFn = origRestore
		logoutRevokeFn = origRevoke
		logoutCookieDeleteFn = origCookieDelete
	})

	// Seed the full multi-service slot set behind a shared registry so the
	// account store's DeleteAllSessions is observable.
	reg := newSlotRegistry(knownServiceSlots...)
	accountStore := &slotStore{reg: reg, slot: "account"}
	rc := &cli.RuntimeContext{
		SessionStore: accountStore,
		AccountStore: accountStore,
		CookieStore:  &slotStore{reg: reg, slot: "cookie"},
		ServiceName:  "account",
		Timeout:      5 * time.Second,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	// Restore yields a live session; revoke succeeds.
	logoutRestoreFn = func(_ context.Context, _ *cli.RuntimeContext) (*common.Session, error) {
		return &common.Session{}, nil
	}
	logoutRevokeFn = func(_ context.Context, _ *common.Session, _ bool) error { return nil }
	logoutCookieDeleteFn = func(store common.SessionStore) error { return store.Delete() }

	if err := authLogoutCmd.RunE(authLogoutCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Account-wide cleanup: every local slot for the account is gone.
	if remaining := reg.remaining(); len(remaining) != 0 {
		t.Errorf("account-wide cleanup incomplete: slots survived: %v (want none)", remaining)
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
// delete failure is logged but does not fail the logout, and that account-wide
// cleanup (DeleteAllSessions) still runs afterward (Req 3.4). Reworked for the
// new cleanup semantics: the surviving-slot outcome is asserted rather than a
// single-store Delete call.
func TestLogout_CookieStoreDeleteFailureLogged(t *testing.T) {
	origForce := authLogoutForce
	origRestore := logoutRestoreFn
	origRevoke := logoutRevokeFn
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutRestoreFn = origRestore
		logoutRevokeFn = origRevoke
		logoutCookieDeleteFn = origCookieDelete
	})

	reg := newSlotRegistry(knownServiceSlots...)
	accountStore := &slotStore{reg: reg, slot: "account"}
	rc := &cli.RuntimeContext{
		SessionStore: accountStore,
		AccountStore: accountStore,
		CookieStore:  &slotStore{reg: reg, slot: "cookie"},
		ServiceName:  "account",
		Timeout:      5 * time.Second,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	logoutRestoreFn = func(_ context.Context, _ *cli.RuntimeContext) (*common.Session, error) {
		return &common.Session{}, nil
	}
	logoutRevokeFn = func(_ context.Context, _ *common.Session, _ bool) error { return nil }
	logoutCookieDeleteFn = func(_ common.SessionStore) error {
		return fmt.Errorf("cookie keyring locked")
	}

	// Logout should succeed even though cookie delete fails.
	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	if err != nil {
		t.Fatalf("logout should succeed despite cookie delete failure, got: %v", err)
	}
	// The non-fatal cookie failure must not block the account-wide purge.
	if remaining := reg.remaining(); len(remaining) != 0 {
		t.Errorf("cleanup did not run after non-fatal cookie failure: %v survived", remaining)
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

// TestLogout_RevokeOutcomeGatesCleanup drives the force/no-force × revoke
// success/failure truth table through RunE. The revoke seam mirrors
// SessionRevoke's own force gate (error surfaces only when !force), so the
// table exercises how logout reacts to SessionRevoke's return value:
//
//	revoke returns nil  -> cleanup runs, RunE returns nil
//	revoke returns err  -> RunE returns the error, cleanup is skipped (Req 2.6)
//
// With --force a revoke failure is swallowed by SessionRevoke (nil return here),
// so cleanup still runs and RunE succeeds (Req 2.5). A restored session is
// injected via logoutRestoreFn.
func TestLogout_RevokeOutcomeGatesCleanup(t *testing.T) {
	revokeErr := errors.New("insufficient scope (9101)")

	tests := []struct {
		name        string
		force       bool
		revokeErr   error // error the underlying revoke would encounter
		wantErr     bool
		wantCleared bool // whether local slots were purged
	}{
		{name: "success no-force purges", force: false, revokeErr: nil, wantErr: false, wantCleared: true},
		{name: "success force purges", force: true, revokeErr: nil, wantErr: false, wantCleared: true},
		{name: "failure no-force returns and skips cleanup", force: false, revokeErr: revokeErr, wantErr: true, wantCleared: false},
		{name: "failure force swallows and purges", force: true, revokeErr: revokeErr, wantErr: false, wantCleared: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origForce := authLogoutForce
			origRestore := logoutRestoreFn
			origRevoke := logoutRevokeFn
			origCookieDelete := logoutCookieDeleteFn
			t.Cleanup(func() {
				authLogoutForce = origForce
				logoutRestoreFn = origRestore
				logoutRevokeFn = origRevoke
				logoutCookieDeleteFn = origCookieDelete
			})

			reg := newSlotRegistry(knownServiceSlots...)
			accountStore := &slotStore{reg: reg, slot: "account"}
			rc := &cli.RuntimeContext{
				SessionStore: accountStore,
				AccountStore: accountStore,
				CookieStore:  &slotStore{reg: reg, slot: "cookie"},
				ServiceName:  "account",
				Timeout:      5 * time.Second,
			}
			cli.SetContext(authLogoutCmd, rc)
			authLogoutForce = tt.force

			logoutRestoreFn = func(_ context.Context, _ *cli.RuntimeContext) (*common.Session, error) {
				return &common.Session{}, nil
			}
			// Mirror SessionRevoke's force gate: the error surfaces to logout
			// only when !force; with force it is swallowed (nil).
			logoutRevokeFn = func(_ context.Context, _ *common.Session, force bool) error {
				if tt.revokeErr != nil && !force {
					return tt.revokeErr
				}
				return nil
			}
			logoutCookieDeleteFn = func(store common.SessionStore) error { return store.Delete() }

			err := authLogoutCmd.RunE(authLogoutCmd, nil)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected revoke error to propagate, got nil")
				}
				if !errors.Is(err, revokeErr) {
					t.Fatalf("error = %v, want %v", err, revokeErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			cleared := len(reg.remaining()) == 0
			if cleared != tt.wantCleared {
				t.Errorf("slots cleared = %v, want %v (remaining: %v)", cleared, tt.wantCleared, reg.remaining())
			}
		})
	}
}

// purgingStore is a SessionStore whose DeleteAllSessions returns purgeErr. It
// exercises logout's non-fatal purge-error warning path (Req 3.4): an
// aggregated local-cleanup failure is logged, not returned.
type purgingStore struct {
	purgeErr error
}

func (s *purgingStore) Load() (*common.SessionCredentials, error) { return nil, common.ErrKeyNotFound }
func (s *purgingStore) Save(_ *common.SessionCredentials) error   { return nil }
func (s *purgingStore) Delete() error                             { return nil }
func (s *purgingStore) List() ([]string, error)                   { return nil, nil }
func (s *purgingStore) Switch(_ string) error                     { return nil }
func (s *purgingStore) DeleteAllSessions() error                  { return s.purgeErr }

// TestLogout_PurgeErrorIsNonFatal verifies that an aggregated local-cleanup
// error from DeleteAllSessions is logged as a warning and does NOT fail the
// logout (Req 3.4). The restore/revoke seams keep the path network-free.
func TestLogout_PurgeErrorIsNonFatal(t *testing.T) {
	origForce := authLogoutForce
	origRestore := logoutRestoreFn
	origRevoke := logoutRevokeFn
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutRestoreFn = origRestore
		logoutRevokeFn = origRevoke
		logoutCookieDeleteFn = origCookieDelete
	})

	store := &purgingStore{purgeErr: errors.Join(
		fmt.Errorf("delete %q: keyring locked", "drive"),
		fmt.Errorf("delete %q: keyring locked", "lumo"),
	)}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5 * time.Second,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	logoutRestoreFn = func(_ context.Context, _ *cli.RuntimeContext) (*common.Session, error) {
		return nil, common.ErrNotLoggedIn
	}
	logoutRevokeFn = func(_ context.Context, _ *common.Session, _ bool) error { return nil }
	logoutCookieDeleteFn = func(_ common.SessionStore) error { return nil }

	if err := authLogoutCmd.RunE(authLogoutCmd, nil); err != nil {
		t.Fatalf("logout should succeed despite purge error, got: %v", err)
	}
}

// --- Bug condition exploration test (account-logout-scope) -----------------
//
// Property 1 (Bug Condition): `account logout` must revoke the real account
// session and clear ALL local session state. See spec design
// "Exploratory Bug Condition Checking".
//
// This test drives RunE(authLogoutCmd, nil) against a session store seeded
// with the full multi-service slot set (account, drive, lumo, cookie, *) and
// asserts the expected post-logout invariant: every local slot for the
// account is deleted.
//
// On UNFIXED code this FAILS: logout only deletes the current service slot
// (account) plus cookie, so the drive/lumo/* slots survive (Req 1.5, 1.6).
// The surviving slots are the runtime counterexample proving the incomplete
// local-cleanup defect. Once the fix wires SessionIndex.DeleteAllSessions()
// into logout (design Change 3), the same assertion passes.
//
// DO NOT "fix" this test or the code when it fails here — the failure is the
// point.

// slotRegistry models the local session keyring as a set of present slots
// keyed by service name (never by decrypted content). It lets separate store
// instances (session/account/cookie) share one observable backing set.
type slotRegistry struct {
	present map[string]bool
}

func newSlotRegistry(slots ...string) *slotRegistry {
	r := &slotRegistry{present: make(map[string]bool, len(slots))}
	for _, s := range slots {
		r.present[s] = true
	}
	return r
}

func (r *slotRegistry) remaining() []string {
	out := make([]string, 0, len(r.present))
	for s := range r.present {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// slotStore is a SessionStore bound to a single slot in a shared registry.
// Delete() removes only that slot (the current-service delete contract that
// the unfixed logout relies on). DeleteAllSessions() removes every slot — the
// account-wide purge the fix introduces (design Change 3). Load() returns
// loadErr so tests can drive the not-logged-in path deterministically without
// touching the network.
type slotStore struct {
	reg     *slotRegistry
	slot    string
	loadErr error
}

func (s *slotStore) Load() (*common.SessionCredentials, error) { return nil, s.loadErr }
func (s *slotStore) Save(_ *common.SessionCredentials) error   { return nil }
func (s *slotStore) Delete() error                             { delete(s.reg.present, s.slot); return nil }
func (s *slotStore) List() ([]string, error)                   { return s.reg.remaining(), nil }
func (s *slotStore) Switch(_ string) error                     { return nil }

// DeleteAllSessions purges every local slot for the account. The fixed logout
// consumes this via the accountPurger interface; the unfixed logout never
// calls it, which is exactly why the drive/lumo/* slots survive.
func (s *slotStore) DeleteAllSessions() error {
	for k := range s.reg.present {
		delete(s.reg.present, k)
	}
	return nil
}

// TestLogout_BugCondition_RevokesAndClearsAllLocalState is the Property 1 bug
// condition exploration test. EXPECTED TO FAIL on unfixed code.
func TestLogout_BugCondition_RevokesAndClearsAllLocalState(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})

	// Seed the full multi-service slot set for a single account.
	reg := newSlotRegistry("account", "drive", "lumo", "cookie", "*")

	rc := &cli.RuntimeContext{
		// Account slot present locally, but Load reports not-logged-in so the
		// logout path is exercised deterministically without a live session.
		SessionStore: &slotStore{reg: reg, slot: "account", loadErr: common.ErrKeyNotFound},
		AccountStore: &slotStore{reg: reg, slot: "account", loadErr: common.ErrKeyNotFound},
		CookieStore:  &slotStore{reg: reg, slot: "cookie", loadErr: common.ErrKeyNotFound},
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	// Preserve the default cookie-delete behavior (delete the cookie slot).
	logoutCookieDeleteFn = func(store common.SessionStore) error { return store.Delete() }

	if err := authLogoutCmd.RunE(authLogoutCmd, nil); err != nil {
		t.Fatalf("logout returned error: %v", err)
	}

	// Expected Behavior (Req 2.4): every local slot for the account is gone.
	if remaining := reg.remaining(); len(remaining) != 0 {
		t.Fatalf("incomplete local cleanup: slots still resolvable after logout: %v "+
			"(want none); counterexample confirms drive/lumo/* forked slots survive",
			remaining)
	}
}

// --- Preservation tests (account-logout-scope, Property 2) -----------------
//
// Property 2 (Preservation): non-logout paths and the force/cleanup semantics
// must be UNCHANGED by the fix. These tests encode behavior OBSERVED on
// UNFIXED code (observation-first) so they pass now and keep passing after the
// fix (spec task 3.6).
//
// They are built on the slotStore/slotRegistry helpers from the Property 1
// test, which implement BOTH Delete() (the current-service delete the unfixed
// logout uses) and DeleteAllSessions() (the account-wide purge the fix adds).
// The assertions target observable OUTCOMES — RunE's error result and the
// surviving slot set — not which specific store method ran, so they stay valid
// across the change in cleanup mechanism (Delete+cookie → DeleteAllSessions).

// knownServiceSlots is the fixed universe of session-store slots for one
// account (design "multi-service slot set": account, drive, lumo, cookie, *).
var knownServiceSlots = []string{"account", "drive", "lumo", "cookie", "*"}

// slotSet is a testing/quick generator that yields a random subset of
// knownServiceSlots. It is a smart generator constrained to the real input
// space (never arbitrary strings), modelling the task's "arbitrary account
// records (random service-slot sets)".
type slotSet struct{ slots []string }

// Generate implements quick.Generator for slotSet.
func (slotSet) Generate(r *rand.Rand, _ int) reflect.Value {
	picked := make([]string, 0, len(knownServiceSlots))
	for _, s := range knownServiceSlots {
		if r.Intn(2) == 0 {
			picked = append(picked, s)
		}
	}
	return reflect.ValueOf(slotSet{slots: picked})
}

// equalSlotSets reports whether a and b contain the same slot names.
func equalSlotSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// TestLogout_Preservation_ForceCleanupTruthTable encodes the force/cleanup
// truth table (Req 2.5, 2.6). A restore failure is the deterministic,
// network-free failure the logout force-gate acts on before any local
// cleanup runs:
//
//	no --force + failure -> RunE returns the error and deletes nothing (Req 2.6)
//	  --force  + failure -> RunE completes, returning nil            (Req 2.5)
//
// The property must hold for any stored slot set. This behavior is present on
// unfixed code (the gate `err != nil && !ErrNotLoggedIn && !force`) and is
// preserved verbatim by the fix.
func TestLogout_Preservation_ForceCleanupTruthTable(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})
	logoutCookieDeleteFn = func(store common.SessionStore) error { return store.Delete() }

	// A non-ErrNotLoggedIn restore failure, distinct from ErrKeyNotFound so it
	// trips the force-gate rather than the not-logged-in path.
	restoreErr := errors.New("preservation: simulated restore failure")

	property := func(force bool, ss slotSet) bool {
		reg := newSlotRegistry(ss.slots...)
		rc := &cli.RuntimeContext{
			SessionStore: &slotStore{reg: reg, slot: "account", loadErr: restoreErr},
			AccountStore: &slotStore{reg: reg, slot: "account", loadErr: restoreErr},
			CookieStore:  &slotStore{reg: reg, slot: "cookie", loadErr: restoreErr},
			ServiceName:  "account",
			Timeout:      5 * time.Second,
		}
		cli.SetContext(authLogoutCmd, rc)
		authLogoutForce = force

		err := authLogoutCmd.RunE(authLogoutCmd, nil)

		if force {
			// Req 2.5: --force completes the logout despite the failure.
			return err == nil
		}
		// Req 2.6: no --force returns the error and leaves every slot intact.
		return err != nil && equalSlotSets(reg.remaining(), ss.slots)
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatalf("force/cleanup truth table property failed: %v", err)
	}
}

// TestLogout_Preservation_NotLoggedInProceedsToCleanup encodes Req 3.3: a
// not-logged-in logout is NOT a hard failure — the missing session is tolerated
// and the path proceeds to local cleanup, returning nil. Observed on unfixed
// code (ErrNotLoggedIn is excluded from the force-gate) and preserved by the
// fix.
func TestLogout_Preservation_NotLoggedInProceedsToCleanup(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})
	logoutCookieDeleteFn = func(store common.SessionStore) error { return store.Delete() }

	reg := newSlotRegistry(knownServiceSlots...)
	rc := &cli.RuntimeContext{
		SessionStore: &slotStore{reg: reg, slot: "account", loadErr: common.ErrKeyNotFound},
		AccountStore: &slotStore{reg: reg, slot: "account", loadErr: common.ErrKeyNotFound},
		CookieStore:  &slotStore{reg: reg, slot: "cookie", loadErr: common.ErrKeyNotFound},
		ServiceName:  "account",
		Timeout:      5 * time.Second,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	if err := authLogoutCmd.RunE(authLogoutCmd, nil); err != nil {
		t.Fatalf("not-logged-in logout should not be a hard failure, got: %v", err)
	}
	// Cleanup proceeded past the missing session: the account slot is gone.
	if reg.present["account"] {
		t.Error("not-logged-in logout did not proceed to cleanup: account slot survived")
	}
}

// --- Revoke force/cleanup truth table property (account-logout-scope, Task 4)

// TestLogout_Property_RevokeForceCleanupTruthTable is the property-based form
// of the revoke-gated force/cleanup truth table (Req 2.5, 2.6). It complements
// the example-based TestLogout_RevokeOutcomeGatesCleanup by quantifying over
// random stored slot sets and both revoke outcomes. The revoke seam mirrors
// SessionRevoke's own force gate: a would-be revoke error surfaces to logout
// only when !force.
//
//	error reaches logout (revokeErr present, !force) -> RunE returns it, nothing purged (Req 2.6)
//	otherwise (success, or --force swallows the error) -> RunE returns nil, every slot purged (Req 2.5)
//
// Restore always yields a live session so the path reaches the revoke gate.
func TestLogout_Property_RevokeForceCleanupTruthTable(t *testing.T) {
	origForce := authLogoutForce
	origRestore := logoutRestoreFn
	origRevoke := logoutRevokeFn
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutRestoreFn = origRestore
		logoutRevokeFn = origRevoke
		logoutCookieDeleteFn = origCookieDelete
	})

	revokeErr := errors.New("insufficient scope (9101)")

	logoutRestoreFn = func(_ context.Context, _ *cli.RuntimeContext) (*common.Session, error) {
		return &common.Session{}, nil
	}
	logoutCookieDeleteFn = func(store common.SessionStore) error { return store.Delete() }

	property := func(force, hasRevokeErr bool, ss slotSet) bool {
		reg := newSlotRegistry(ss.slots...)
		accountStore := &slotStore{reg: reg, slot: "account"}
		rc := &cli.RuntimeContext{
			SessionStore: accountStore,
			AccountStore: accountStore,
			CookieStore:  &slotStore{reg: reg, slot: "cookie"},
			ServiceName:  "account",
			Timeout:      5 * time.Second,
		}
		cli.SetContext(authLogoutCmd, rc)
		authLogoutForce = force

		logoutRevokeFn = func(_ context.Context, _ *common.Session, f bool) error {
			if hasRevokeErr && !f {
				return revokeErr
			}
			return nil
		}

		err := authLogoutCmd.RunE(authLogoutCmd, nil)

		if hasRevokeErr && !force {
			// Req 2.6: the error propagates and cleanup is skipped — the
			// stored slot set is left exactly as seeded.
			return err != nil && errors.Is(err, revokeErr) &&
				equalSlotSets(reg.remaining(), ss.slots)
		}
		// Req 2.5 / success: logout completes and purges every local slot.
		return err == nil && len(reg.remaining()) == 0
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatalf("revoke force/cleanup truth table property failed: %v", err)
	}
}

// --- Integration guards (account-logout-scope, Task 4) ---------------------
//
// These exercise the fix through the REAL keyring.SessionIndex (the concrete
// accountPurger) backed by an in-memory keyring, rather than the slotStore
// fake, so the account-wide purge and the service-fork routing run against
// production code.
//
// Fidelity note: a fully live end-to-end run (logout hitting AuthDelete, then
// `drive ls` performing a real fork) requires a Proton backend and credentials
// that are not available in the unit environment. The logout restore/revoke
// are therefore driven through their existing seams (network-free), while the
// LOCAL session-store effects — account-wide purge and the not-logged-in
// routing that follows — run against the real keyring.SessionIndex and
// account.RestoreServiceSession. What is covered: (1) logout purges every
// local slot and a subsequent drive session setup then fails not-logged-in
// with no surviving forked slot; (2) drive still routes through
// RestoreServiceSession (the fork-capable path) after the logout routing
// change. What is NOT covered here: the server-side revoke/cascade and the
// live fork handshake — those are covered by api/account unit tests
// (TestSessionRevoke, TestShouldFork, RestoreServiceSession tests).

// memKeyring is an in-memory keyring.Keyring for the integration guards. It
// mirrors internal/keyring's test-only MockKeyring, which is not importable
// across packages.
type memKeyring struct{ store map[string]string }

func newMemKeyring() *memKeyring { return &memKeyring{store: make(map[string]string)} }

func (m *memKeyring) key(service, account string) string { return service + "/" + account }

func (m *memKeyring) Get(service, account string) (string, error) {
	v, ok := m.store[m.key(service, account)]
	if !ok {
		return "", fmt.Errorf("secret not found in keyring")
	}
	return v, nil
}

func (m *memKeyring) Set(service, account, password string) error {
	m.store[m.key(service, account)] = password
	return nil
}

func (m *memKeyring) Delete(service, account string) error {
	delete(m.store, m.key(service, account))
	return nil
}

// seedRealAccount saves a minimal session under each service slot for account,
// using a real keyring.SessionIndex so the on-disk index and keyring reflect a
// logged-in, multi-service (forked) account.
func seedRealAccount(t *testing.T, indexPath, account string, kr keyring.Keyring, services ...string) {
	t.Helper()
	for _, svc := range services {
		st := keyring.NewSessionStore(indexPath, account, svc, kr)
		if err := st.Save(&common.SessionCredentials{
			UID:           account + "-" + svc,
			AccessToken:   "at-" + svc,
			RefreshToken:  "rt-" + svc,
			SaltedKeyPass: "skp-" + svc,
		}); err != nil {
			t.Fatalf("seed %s/%s: %v", account, svc, err)
		}
	}
}

// TestIntegration_LogoutPurgesAllSlotsThenDriveNotLoggedIn is the end-to-end
// guard: after logout, every local slot for the account is gone and a
// subsequent drive session setup fails with ErrNotLoggedIn — there is no
// surviving forked slot to restore from (Req 2.4).
func TestIntegration_LogoutPurgesAllSlotsThenDriveNotLoggedIn(t *testing.T) {
	origForce := authLogoutForce
	origRestore := logoutRestoreFn
	origRevoke := logoutRevokeFn
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutRestoreFn = origRestore
		logoutRevokeFn = origRevoke
		logoutCookieDeleteFn = origCookieDelete
	})

	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.db")
	kr := newMemKeyring()
	const acct = "default"

	// Seed a logged-in account with the full forked slot set.
	seedRealAccount(t, indexPath, acct, kr, "account", "drive", "lumo", "cookie", "*")

	accountStore := keyring.NewSessionStore(indexPath, acct, "account", kr)
	cookieStore := keyring.NewSessionStore(indexPath, acct, "cookie", kr)
	wildcardStore := keyring.NewSessionStore(indexPath, acct, "*", kr)

	rc := &cli.RuntimeContext{
		SessionStore: wildcardStore,
		AccountStore: accountStore,
		CookieStore:  cookieStore,
		ServiceName:  "account",
		Timeout:      5 * time.Second,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	// Network-free logout: restore yields a session and revoke succeeds. The
	// account-wide purge runs against the REAL keyring.SessionIndex.
	logoutRestoreFn = func(_ context.Context, _ *cli.RuntimeContext) (*common.Session, error) {
		return &common.Session{}, nil
	}
	logoutRevokeFn = func(_ context.Context, _ *common.Session, _ bool) error { return nil }
	logoutCookieDeleteFn = func(store common.SessionStore) error { return store.Delete() }

	if err := authLogoutCmd.RunE(authLogoutCmd, nil); err != nil {
		t.Fatalf("logout: %v", err)
	}

	// No local account entries survive.
	names, err := accountStore.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("account entries survived logout: %v (want none)", names)
	}

	// End-to-end: a subsequent drive session setup fails not-logged-in because
	// the account (fork source) and every forked slot are gone.
	driveStore := keyring.NewSessionStore(indexPath, acct, "drive", kr)
	_, err = account.RestoreServiceSession(
		context.Background(), "drive", nil,
		driveStore, accountStore, cookieStore, common.DefaultVersion, nil,
	)
	if !errors.Is(err, common.ErrNotLoggedIn) {
		t.Fatalf("drive after logout: error = %v, want ErrNotLoggedIn", err)
	}
}

// TestIntegration_DriveStillRoutesThroughForkPath is the Req 3.1 regression
// guard: a service command (drive) still routes through
// account.RestoreServiceSession — the fork-capable path — after the logout
// routing change. With no account session, that path surfaces ErrNotLoggedIn
// (before any network call), confirming the logout fix did not divert drive to
// logout's no-fork ReadySession path. The fork decision itself is covered by
// api/account's TestShouldFork.
func TestIntegration_DriveStillRoutesThroughForkPath(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.db")
	kr := newMemKeyring()
	const acct = "default"

	// Not logged in: no account slot seeded.
	cmd := &cobra.Command{}
	rc := &cli.RuntimeContext{
		SessionStore: keyring.NewSessionStore(indexPath, acct, "drive", kr),
		AccountStore: keyring.NewSessionStore(indexPath, acct, "account", kr),
		CookieStore:  keyring.NewSessionStore(indexPath, acct, "cookie", kr),
		ServiceName:  "drive",
		Timeout:      5 * time.Second,
	}
	cli.SetContext(cmd, rc)

	_, err := cli.SetupSession(context.Background(), cmd)
	if !errors.Is(err, common.ErrNotLoggedIn) {
		t.Fatalf("drive SetupSession routing: error = %v, want ErrNotLoggedIn", err)
	}
}
