package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// RefreshAccountLocked refreshes the shared account token under a
// cross-process lock. startedWith is the rotating credential the caller
// currently holds for the active auth style: the Bearer refresh token when
// CookieAuth is false, or the REFRESH-<uid> cookie value when CookieAuth is
// true. It reloads the store; if a peer already rotated (stored rotating
// credential != startedWith), it returns the reloaded credentials without
// refreshing. Otherwise it refreshes via the mechanism for the active style
// (NewClientWithRefresh for Bearer, RefreshCookies for cookie), persists the
// rotated credentials with a fresh LastRefresh, and returns them.
//
// The lock is best-effort: when it cannot be acquired (e.g. $XDG_RUNTIME_DIR
// unset, or a lock error) the refresh proceeds uncoordinated rather than
// blocking the caller. The lock is released on every return path (success,
// adopt, error).
//
// A refresh rejected with HTTP 400/422 (a dead rotating credential, either
// style) is reported as ErrAccountDeauthed. Transient failures
// (429/5xx/network) are returned wrapped for the caller to retry.
func RefreshAccountLocked(ctx context.Context, mgr *proton.Manager, store api.SessionStore, startedWith string) (*api.SessionCredentials, error) {
	// Load once to derive the UID that keys the lock. This read is not
	// authoritative for the refresh decision — that is the read-after-lock
	// below.
	pre, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("account.RefreshAccountLocked: load: %w", err)
	}

	// Best-effort lock: on failure proceed uncoordinated rather than block
	// the application (Req 1.6).
	lock, lockErr := acquireRefreshLock(pre.UID)
	if lockErr != nil {
		slog.Warn("account refresh: lock unavailable, proceeding best-effort", "error", lockErr)
	} else {
		defer lock.release()
	}

	// Read-after-lock: reload the latest credentials before deciding to
	// refresh, so a peer's rotation under the lock is observed (Req 1.2).
	cur, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("account.RefreshAccountLocked: reload: %w", err)
	}

	// Adopt path: a peer already rotated the rotating credential while we
	// waited for the lock. Take its credentials rather than refreshing a
	// stale one (Req 1.3).
	if rotatingCredential(cur) != startedWith {
		slog.Debug("account refresh: peer already rotated, adopting reloaded credentials")
		return cur, nil
	}

	// Rotating credential unchanged: perform the refresh via the mechanism
	// for the active auth style (Req 1.4, 1.7).
	if cur.CookieAuth {
		return refreshCookieLocked(ctx, store, cur)
	}
	return refreshBearerLocked(ctx, mgr, store, cur)
}

// refreshBearerLocked performs a Bearer-auth account refresh via
// NewClientWithRefresh, persists the rotated tokens with a fresh LastRefresh,
// and returns the updated credentials. Called under the refresh lock.
func refreshBearerLocked(ctx context.Context, mgr *proton.Manager, store api.SessionStore, cur *api.SessionCredentials) (*api.SessionCredentials, error) {
	_, auth, err := mgr.NewClientWithRefresh(ctx, cur.UID, cur.RefreshToken)
	if err != nil {
		if isDeauthError(err) {
			return nil, fmt.Errorf("%w: %w", ErrAccountDeauthed, err)
		}
		return nil, fmt.Errorf("account.RefreshAccountLocked: refresh: %w", err)
	}

	slog.Debug("account refresh: rotated bearer tokens", "uid", auth.UID,
		"access_token", "<redacted>", "refresh_token", "<redacted>")

	cur.AccessToken = auth.AccessToken
	cur.RefreshToken = auth.RefreshToken
	cur.LastRefresh = time.Now()
	if err := store.Save(cur); err != nil {
		return nil, fmt.Errorf("account.RefreshAccountLocked: save: %w", err)
	}

	return cur, nil
}

// refreshCookieLocked performs a cookie-auth account refresh via
// RefreshCookies on a CookieSession built from the stored cookies, persists
// the rotated AUTH-/REFRESH-<uid> cookies with a fresh LastRefresh, and
// returns the updated credentials. Called under the refresh lock.
func refreshCookieLocked(ctx context.Context, store api.SessionStore, cur *api.SessionCredentials) (*api.SessionCredentials, error) {
	acctSvc, _ := api.LookupService("account")

	// Build a CookieSession over the stored cookies. Store is left nil so
	// this function owns persistence (updating cur, then a single Save),
	// rather than RefreshCookies persisting a separate config.
	jar := NewProtonCookieJar(cur.Cookies, acctSvc.Host)
	cs := NewCookieSessionForRefresh(cur.UID, acctSvc.Host, acctSvc.AppVersion(""), jar, nil)

	if err := cs.RefreshCookies(ctx); err != nil {
		if isDeauthError(err) {
			return nil, fmt.Errorf("%w: %w", ErrAccountDeauthed, err)
		}
		return nil, fmt.Errorf("account.RefreshAccountLocked: cookie refresh: %w", err)
	}

	slog.Debug("account refresh: rotated cookies", "uid", cur.UID)

	// Adopt the rotated cookies from the refreshed jar.
	refreshed := cs.Config()
	cur.Cookies = refreshed.Cookies
	cur.LastRefresh = time.Now()
	if err := store.Save(cur); err != nil {
		return nil, fmt.Errorf("account.RefreshAccountLocked: save: %w", err)
	}

	return cur, nil
}

// rotatingCredential returns the credential that Proton rotates on each
// refresh for the credentials' active auth style: the Bearer RefreshToken
// when CookieAuth is false, or the REFRESH-<uid> cookie value when CookieAuth
// is true. It is the value compared across a refresh to detect whether a peer
// already rotated (Req 1.3). Returns an empty string when a cookie-auth
// session has no REFRESH cookie.
func rotatingCredential(cur *api.SessionCredentials) string {
	if !cur.CookieAuth {
		return cur.RefreshToken
	}
	for _, c := range cur.Cookies {
		if strings.HasPrefix(c.Name, "REFRESH-") {
			return c.Value
		}
	}
	return ""
}

// isDeauthError reports whether err is a Proton API error with an HTTP status
// that means the rotating credential is dead (400 Bad Request or 422
// Unprocessable Entity). It matches both the go-proton-api APIError (Bearer
// refresh via NewClientWithRefresh) and the package's own api.Error (cookie
// refresh via RefreshCookies). Network and transient errors (429/5xx) do not
// match, so callers treat them as retryable.
func isDeauthError(err error) bool {
	var apiErr *proton.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusBadRequest ||
			apiErr.Status == http.StatusUnprocessableEntity
	}
	var cookieErr *api.Error
	if errors.As(err, &cookieErr) {
		return cookieErr.Status == http.StatusBadRequest ||
			cookieErr.Status == http.StatusUnprocessableEntity
	}
	return false
}
