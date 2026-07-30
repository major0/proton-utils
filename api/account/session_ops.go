package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// SessionFromCredentials initializes a new session from the provided config.
// The session is not fully usable until it has been Unlock'ed using the
// user-provided keypass.
//
// When store is non-nil, the token-persisting auth handler is attached to the
// client BEFORE the first API call. This is essential: Proton rotates the
// refresh token on every /auth/refresh (invalidating the old one), so a
// refresh triggered by the GetUser below must persist the rotated token.
// Attaching the handler afterward would drop it and de-auth the next process.
func SessionFromCredentials(ctx context.Context, options []proton.Option, config *api.SessionCredentials, managerHook func(*proton.Manager), store api.SessionStore) (*api.Session, error) {
	if config.UID == "" {
		return nil, api.ErrMissingUID
	}

	if config.AccessToken == "" {
		return nil, api.ErrMissingAccessToken
	}

	if config.RefreshToken == "" {
		return nil, api.ErrMissingRefreshToken
	}

	session, _ := api.InitSession(ctx, options, managerHook)

	slog.Debug("session.refresh client")
	slog.Debug("session.config", "uid", config.UID, "access_token", "<redacted>", "refresh_token", "<redacted>")
	session.Client = session.Manager().NewClient(config.UID, config.AccessToken, config.RefreshToken)
	session.Auth = proton.Auth{
		UID:          config.UID,
		AccessToken:  config.AccessToken,
		RefreshToken: config.RefreshToken,
	}

	// Load persisted cookies into the jar before attaching the handler, so
	// that if a refresh fires during GetUser the handler re-serializes the
	// real cookies rather than clobbering the store with an empty jar.
	api.LoadCookies(session.CookieJar(), config.Cookies, api.CookieURL())

	// Attach the token-persisting handler before any API call so a refresh
	// triggered by GetUser (below) persists the rotated refresh token.
	if store != nil {
		session.AddAuthHandler(NewAuthHandler(store, session))
		session.AddDeauthHandler(NewDeauthHandler())
	}

	slog.Debug("session.GetUser")
	u, err := session.Client.GetUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("account.SessionFromCredentials: get user: %w", err)
	}
	session.SetUser(u)

	return session, nil
}

// sessionFromLogin creates a session with common setup shared by
// SessionFromLogin and SessionRetryWithHV. It returns the prepared
// session and manager; the caller performs the actual login call.
func sessionFromLogin(ctx context.Context, options []proton.Option, managerHook func(*proton.Manager)) (*api.Session, *proton.Manager) {
	return api.InitSession(ctx, options, managerHook)
}

// SessionRestore loads credentials from the store and creates an unlocked
// session. Returns ErrNotLoggedIn if no session is stored. When the loaded
// config has CookieAuth=true and cookieStore is non-nil, the cookie restore
// path is used instead of the Bearer path.
func SessionRestore(ctx context.Context, options []proton.Option, store api.SessionStore, cookieStore api.SessionStore, managerHook func(*proton.Manager)) (*api.Session, error) {
	config, err := store.Load()
	if err != nil {
		if errors.Is(err, api.ErrKeyNotFound) {
			return nil, api.ErrNotLoggedIn
		}
		return nil, fmt.Errorf("account.SessionRestore: load: %w", err)
	}

	// Route to cookie restore when CookieAuth=true.
	if config.CookieAuth && cookieStore != nil {
		return CookieSessionRestore(ctx, options, cookieStore, config, managerHook)
	}

	slog.Debug("SessionRestore", "uid", config.UID, "access_token", "<redacted>", "refresh_token", "<redacted>")

	// Staleness check.
	if !config.LastRefresh.IsZero() {
		age := time.Since(config.LastRefresh)
		if age > api.TokenExpireAge {
			slog.Warn("session tokens likely expired", "age", age)
		} else if age > api.TokenWarnAge {
			slog.Warn("session tokens near expiry", "age", age)
		}
	}

	session, err := SessionFromCredentials(ctx, options, config, managerHook, store)
	if err != nil {
		return nil, fmt.Errorf("account.SessionRestore: %w", err)
	}

	// Restore persisted cookies into the session's jar.
	api.LoadCookies(session.CookieJar(), config.Cookies, api.CookieURL())

	keypass, err := api.Base64Decode(config.SaltedKeyPass)
	if err != nil {
		return nil, fmt.Errorf("account.SessionRestore: decode keypass: %w", err)
	}

	addrs, err := session.Client.GetAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("account.SessionRestore: get addresses: %w", err)
	}

	if err := session.Unlock(keypass, addrs); err != nil {
		return nil, fmt.Errorf("account.SessionRestore: unlock: %w", err)
	}

	// Proactive refresh: make a lightweight API call to trigger
	// go-proton-api's auto-refresh if the access token is expired.
	if !config.LastRefresh.IsZero() && time.Since(config.LastRefresh) > api.TokenExpireAge {
		if _, err := session.Client.GetUser(ctx); err != nil {
			return nil, fmt.Errorf("%w: proactive refresh: %w", ErrSessionExpired, err)
		}
	}

	return session, nil
}

// ReadySession restores a session from the store, registers auth/deauth
// handlers, and returns a fully initialized Session ready for use.
// This is the recommended entry point for consumers that need an
// authenticated session. When cookieStore is non-nil and the session has
// CookieAuth=true, the cookie restore path is used.
func ReadySession(ctx context.Context, options []proton.Option, store api.SessionStore, cookieStore api.SessionStore, managerHook func(*proton.Manager)) (*api.Session, error) {
	session, err := SessionRestore(ctx, options, store, cookieStore, managerHook)
	if err != nil {
		return nil, err
	}
	// Auth/deauth handlers are attached during restore before the first
	// refreshing call: SessionFromCredentials attaches them for the bearer
	// path, and the cookie path installs its own refresh handler on the
	// transport. Attaching here would only duplicate them.
	return session, nil
}

// SessionSave persists session credentials, cookie jar state, and a refresh
// timestamp to the store. Uses CookieQueryURL (path=/api/auth/refresh) so
// the jar query matches both AUTH (path=/api/) and REFRESH
// (path=/api/auth/refresh) cookies.
func SessionSave(store api.SessionStore, session *api.Session, keypass []byte) error {
	queryURL := cookieQueryURL(session.BaseURL)
	config := &api.SessionCredentials{
		UID:           session.Auth.UID,
		AccessToken:   session.Auth.AccessToken,
		RefreshToken:  session.Auth.RefreshToken,
		SaltedKeyPass: api.Base64Encode(keypass),
		Cookies:       api.SerializeCookies(session.CookieJar(), queryURL),
		LastRefresh:   time.Now(),
	}
	return store.Save(config)
}

// sessionAuthDeleteFn performs the self-delete revoke against the session's
// own token. It is a package var so tests can inject a revoke outcome (e.g. a
// 9101 "insufficient scope" error) without a live client.
var sessionAuthDeleteFn = func(ctx context.Context, s *api.Session) error {
	return s.Client.AuthDelete(ctx)
}

// SessionRevoke revokes the account session via the self-delete endpoint
// (DELETE /auth/v4, through AuthDelete), which authenticates with the
// session's own token and needs no elevated scope. Dependent child forks
// (Independent: 0) are cascaded and invalidated server-side. A nil session
// (not logged in) is a no-op. When the revoke fails, the error is returned
// unless force is set, in which case it is logged and swallowed. Local
// session-store cleanup is the caller's responsibility.
func SessionRevoke(ctx context.Context, session *api.Session, force bool) error {
	if session == nil {
		return nil // not logged in: nothing to revoke server-side
	}
	slog.Debug("SessionRevoke", "uid", session.Auth.UID)
	if err := sessionAuthDeleteFn(ctx, session); err != nil {
		if !force {
			return err
		}
		slog.Error("SessionRevoke", "error", err)
	}
	return nil
}

// SessionList returns account names from the session store.
func SessionList(store api.SessionStore) ([]string, error) {
	return store.List()
}

// SessionFromLogin initializes a new session from the provided login/password.
// If hvDetails is non-nil, the login includes the HV token for CAPTCHA retry.
// The same manager (and cookie jar) is used for both initial and HV-retried
// login attempts — this is required because Proton's backend correlates the
// solved CAPTCHA with the session cookie from the initial attempt.
//
// On error, the returned *Session is intentionally non-nil and reusable for
// SessionRetryWithHV. The manager and cookie jar must be preserved across
// attempts so that the solved CAPTCHA correlates with the session cookie
// established during the initial (failed) login request.
func SessionFromLogin(ctx context.Context, options []proton.Option, username string, password string, hvDetails *proton.APIHVDetails, managerHook func(*proton.Manager)) (*api.Session, error) {
	session, manager := sessionFromLogin(ctx, options, managerHook)

	slog.Debug("session.login", "username", "<redacted>", "password", "<hidden>")

	// Fetch AuthInfo separately so we can cache it for HV retries.
	// The SRP session in AuthInfo is bound to the CAPTCHA token — reusing
	// it on retry is required for the solved token to be accepted.
	info, err := manager.AuthInfo(ctx, proton.AuthInfoReq{Username: username})
	if err != nil {
		return session, fmt.Errorf("%w: auth info: %w", ErrAuthFailed, err)
	}
	session.SetCachedAuthInfo(&info)

	session.Client, session.Auth, err = manager.NewClientWithLoginWithCachedInfo(ctx, info, username, []byte(password), hvDetails)
	logCookies("session.login.done", session)
	slog.Debug("session.login.done", "error", err)
	if err != nil {
		return session, fmt.Errorf("%w: login: %w", ErrAuthFailed, err)
	}

	return session, nil
}

// SessionRetryWithHV retries login on an existing session (reusing its
// manager and cookie jar) with HV details after the user solved the CAPTCHA.
// A fresh AuthInfo is fetched because the original SRP session is invalidated
// by the 9001 response. The solved CAPTCHA composite token is NOT bound to
// the SRP session — it's bound to the HumanVerificationToken.
func SessionRetryWithHV(ctx context.Context, session *api.Session, username, password string, hv *proton.APIHVDetails) error {
	logCookies("session.login.hv.before", session)
	slog.Debug("session.login.hv", "username", "<redacted>", "password", "<hidden>")

	var err error
	session.Client, session.Auth, err = session.Manager().NewClientWithLoginWithHVToken(ctx, username, []byte(password), hv)
	logCookies("session.login.hv.after", session)
	if err != nil {
		return fmt.Errorf("%w: hv retry: %w", ErrAuthFailed, err)
	}
	return nil
}

// NeedsProactiveRefresh reports whether the session's LastRefresh age exceeds
// ProactiveRefreshAge. A zero-valued LastRefresh always triggers refresh.
func NeedsProactiveRefresh(lastRefresh time.Time) bool {
	if lastRefresh.IsZero() {
		return true
	}
	return time.Since(lastRefresh) > api.ProactiveRefreshAge
}

// NeedsCookieRefresh reports whether the cookie session's LastRefresh age
// exceeds CookieRefreshAge. A zero-valued LastRefresh always triggers refresh.
func NeedsCookieRefresh(lastRefresh time.Time) bool {
	if lastRefresh.IsZero() {
		return true
	}
	return time.Since(lastRefresh) > api.CookieRefreshAge
}

// IsStale reports whether a service session is stale relative to the account
// session. A service session is stale when the account's LastRefresh is after
// the service's LastRefresh, or when the service's LastRefresh is zero.
func IsStale(accountRefresh, serviceRefresh time.Time) bool {
	if serviceRefresh.IsZero() {
		return true
	}
	return accountRefresh.After(serviceRefresh)
}

// proactiveRefresh checks the account session's LastRefresh age and, when it
// exceeds the ProactiveRefreshAge threshold, performs a cross-process
// coordinated refresh via RefreshAccountLocked instead of an uncoordinated
// GetUser poke, so cold-starting several apps at once cannot de-auth each
// other (Req 3.1). Style dispatch (Bearer vs cookie) is internal to
// RefreshAccountLocked; the caller only supplies the rotating credential for
// the active style. The rotated credentials are persisted to store with a
// fresh LastRefresh.
func proactiveRefresh(ctx context.Context, session *api.Session, config *api.SessionCredentials, store api.SessionStore) error {
	if !NeedsProactiveRefresh(config.LastRefresh) {
		return nil
	}

	slog.Debug("proactiveRefresh", "age", time.Since(config.LastRefresh))

	if _, err := RefreshAccountLocked(ctx, session.Manager(), store, rotatingCredential(config)); err != nil {
		return fmt.Errorf("%w: proactive refresh: %w", ErrSessionExpired, err)
	}

	return nil
}

// shouldFork determines whether a service session needs to be forked from
// the account session. Returns true when the service session is missing,
// is a wildcard fallback, has no Service field (legacy), or is stale
// relative to the account session.
func shouldFork(svcConfig *api.SessionCredentials, svcErr error, acctConfig *api.SessionCredentials, service string) bool {
	if svcErr != nil {
		slog.Debug("service session not found, will fork", "service", service)
		return true
	}
	if svcConfig.Service != service && svcConfig.Service != "" {
		slog.Debug("service session is wildcard fallback, will fork", "service", service, "found_service", svcConfig.Service)
		return true
	}
	if svcConfig.Service == "" {
		slog.Debug("service session has no service field, will fork", "service", service)
		return true
	}
	if IsStale(acctConfig.LastRefresh, svcConfig.LastRefresh) {
		slog.Debug("service session is stale, will re-fork", "service", service)
		return true
	}
	slog.Debug("service session is fresh", "service", service, "uid", svcConfig.UID, "svc_service", svcConfig.Service, "svc_last_refresh", svcConfig.LastRefresh, "acct_last_refresh", acctConfig.LastRefresh)
	return false
}

// forkUnlockAndSave completes the post-fork sequence: fetch user, save
// session, fetch addresses, unlock keyrings, register auth/deauth handlers.
// Used by both the cookie-fork and bearer-fork paths.
func forkUnlockAndSave(ctx context.Context, child *api.Session, childKeyPass []byte, store api.SessionStore, service string) error {
	childUser, err := child.Client.GetUser(ctx)
	if err != nil {
		return fmt.Errorf("restore service session %q: get user: %w", service, err)
	}
	child.SetUser(childUser)

	if err := SessionSave(store, child, childKeyPass); err != nil {
		return fmt.Errorf("restore service session %q: save fork: %w", service, err)
	}

	// Update the saved config with service metadata so shouldFork can
	// identify this session on subsequent restores.
	if cfg, loadErr := store.Load(); loadErr == nil {
		cfg.Service = service
		cfg.CookieAuth = child.Auth.AccessToken == ""
		_ = store.Save(cfg)
	}

	addrs, err := child.Client.GetAddresses(ctx)
	if err != nil {
		return fmt.Errorf("restore service session %q: get addresses: %w", service, err)
	}
	slog.Debug("fork.unlock", "service", service, "child_uid", child.Auth.UID, "keypass_len", len(childKeyPass), "num_addresses", len(addrs))
	if err := child.Unlock(childKeyPass, addrs); err != nil {
		return fmt.Errorf("restore service session %q: unlock: %w", service, err)
	}

	child.AddAuthHandler(NewAuthHandler(store, child))
	child.AddDeauthHandler(NewDeauthHandler())
	return nil
}
