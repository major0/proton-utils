package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// forkWithRefreshRetry runs a fork push once and, on a stale-credential 401,
// refreshes the account credential under the cross-process refresh lock and
// retries the fork exactly once. It factors out the common
// attempt→detect-401→refresh→retry skeleton shared by the Bearer and cookie
// fork pushes; per-style behavior is injected via the callbacks.
//
// The callbacks are:
//   - refreshCred returns the rotating credential the caller currently holds
//     (passed to RefreshAccountLocked as startedWith so a peer's concurrent
//     rotation is detected). It is computed lazily, only on the 401 path, so
//     the success path performs no extra I/O.
//   - applyRefreshed applies the refreshed credentials to the account session
//     before the retry (a no-op for the cookie style, which reloads the
//     rotated cookies from its store on retry).
//   - forkOnce performs a single fork attempt.
//
// mgr and refreshStore are forwarded to RefreshAccountLocked; logUID is the
// session UID used for diagnostic logging (never tokens or cookie values).
//
// A successful first attempt returns immediately with no refresh or retry. A
// non-401 error propagates unchanged (errors.As unwraps both the Bearer
// ErrForkFailed-wrapped error and the CookieFork bare *api.Error). When the
// refresh reports ErrAccountDeauthed the error is returned without retrying,
// so a genuine de-auth surfaces the re-login case.
func forkWithRefreshRetry(
	ctx context.Context,
	mgr *proton.Manager,
	refreshStore api.SessionStore,
	logUID string,
	refreshCred func() (string, error),
	applyRefreshed func(*api.SessionCredentials),
	forkOnce func() (*api.Session, []byte, error),
) (*api.Session, []byte, error) {
	child, keypass, err := forkOnce()
	if err == nil {
		// Success path preserved: return the child with no refresh/retry.
		return child, keypass, nil
	}

	// Only a 401 (stale credential) triggers refresh-and-retry. Any other
	// error propagates unchanged, preserving each style's wrapping.
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		return nil, nil, err
	}

	slog.Debug("fork.refresh-retry", "uid", logUID)

	startedWith, err := refreshCred()
	if err != nil {
		return nil, nil, err
	}

	// Refresh under the cross-process lock. On any refresh error (including
	// ErrAccountDeauthed) return it unchanged so a genuine de-auth skips the
	// retry. RefreshAccountLocked persists the rotated credential to the
	// store, so the store holds it after this call succeeds.
	refreshed, rerr := RefreshAccountLocked(ctx, mgr, refreshStore, startedWith)
	if rerr != nil {
		return nil, nil, rerr
	}

	applyRefreshed(refreshed)

	// Retry exactly once with the refreshed credential.
	return forkOnce()
}

// forkBearerWithRefreshRetry wraps the Bearer fork push
// (ForkSessionWithKeyPass) with refresh-and-retry on a stale-token 401. On a
// 401 it refreshes the account's Bearer access/refresh token via
// RefreshAccountLocked against accountStore, applies the rotated tokens to
// acctSession and rebuilds its client, then retries the fork once.
func forkBearerWithRefreshRetry(
	ctx context.Context,
	acctSession *api.Session,
	acctConfig *api.SessionCredentials,
	svc api.ServiceConfig,
	version string,
	keypass []byte,
	accountStore api.SessionStore,
) (*api.Session, []byte, error) {
	forkOnce := func() (*api.Session, []byte, error) {
		return ForkSessionWithKeyPass(ctx, acctSession, svc, version, keypass)
	}

	// Bearer rotating credential is the in-memory RefreshToken; no I/O.
	refreshCred := func() (string, error) {
		return rotatingCredential(acctConfig), nil
	}

	// Apply the rotated tokens to the account session and rebuild its client,
	// mirroring the NewClient pattern in SessionFromForkPull.
	applyRefreshed := func(refreshed *api.SessionCredentials) {
		acctSession.Auth.UID = refreshed.UID
		acctSession.Auth.AccessToken = refreshed.AccessToken
		acctSession.Auth.RefreshToken = refreshed.RefreshToken
		acctSession.Client = acctSession.Manager().NewClient(refreshed.UID, refreshed.AccessToken, refreshed.RefreshToken)
	}

	return forkWithRefreshRetry(ctx, acctSession.Manager(), accountStore, acctSession.Auth.UID, refreshCred, applyRefreshed, forkOnce)
}

// forkCookieWithRefreshRetry wraps the cookie fork push (CookieFork) with
// refresh-and-retry on a stale-cookie 401. On a 401 it refreshes the
// AUTH-/REFRESH-<uid> cookies via RefreshAccountLocked against cookieStore —
// the store holding the cookies used for the push — then retries the fork
// once. Applying the rotated cookies is a no-op here: RefreshAccountLocked
// persists them to cookieStore, and CookieFork's loadOrCreateCookieSession
// reloads them on the retry.
func forkCookieWithRefreshRetry(
	ctx context.Context,
	acctSession *api.Session,
	acctConfig *api.SessionCredentials,
	svc api.ServiceConfig,
	version string,
	keypass []byte,
	cookieStore api.SessionStore,
) (*api.Session, []byte, error) {
	forkOnce := func() (*api.Session, []byte, error) {
		return CookieFork(ctx, acctSession, acctConfig, svc, version, keypass, cookieStore)
	}

	// The cookie rotating credential is the REFRESH-<uid> cookie value, loaded
	// lazily on the 401 path only. By then CookieFork has ensured a cookie
	// config exists in cookieStore.
	refreshCred := func() (string, error) {
		cfg, err := cookieStore.Load()
		if err != nil {
			return "", fmt.Errorf("cookie fork refresh: load cookie store: %w", err)
		}
		return rotatingCredential(cfg), nil
	}

	// No-op: RefreshAccountLocked persists the rotated AUTH-/REFRESH-<uid>
	// cookies to cookieStore; the retry's loadOrCreateCookieSession reloads
	// them, so no in-memory session mutation is needed here.
	applyRefreshed := func(*api.SessionCredentials) {}

	// acctSession.Manager() is ignored by the cookie refresh path.
	return forkWithRefreshRetry(ctx, acctSession.Manager(), cookieStore, acctSession.Auth.UID, refreshCred, applyRefreshed, forkOnce)
}
