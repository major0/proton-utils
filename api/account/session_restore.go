package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
)

// RestoreServiceSession restores or creates a service-specific session.
// If no session exists for the service, it forks from the account session.
// If no account session exists, it returns ErrNotLoggedIn.
//
// The flow:
//  1. Load account session config from accountStore.
//  2. If CookieAuth=true, use cookie fork path (CookieSessionRestore → cookieFork).
//  3. Otherwise, build account session from credentials.
//  4. If account session age > ProactiveRefreshAge, trigger proactive refresh.
//  5. If service session missing or stale (account LastRefresh > service LastRefresh),
//     fork from account session via ForkSessionWithKeyPass.
//  6. Set session.BaseURL and AppVersion from the ServiceConfig.
//  7. Return session.
func RestoreServiceSession(ctx context.Context, service string, options []proton.Option, store api.SessionStore, accountStore api.SessionStore, cookieStore api.SessionStore, version string, managerHook func(*proton.Manager)) (*api.Session, error) {
	svc, err := api.LookupService(service)
	if err != nil {
		return nil, err
	}

	// Load account session config (needed for staleness check and fork source).
	acctConfig, err := accountStore.Load()
	if err != nil {
		if errors.Is(err, api.ErrKeyNotFound) {
			return nil, api.ErrNotLoggedIn
		}
		return nil, fmt.Errorf("restore service session %q: account: %w", service, err)
	}

	// When CookieAuth=true, restore the cookie session and fork to get
	// service-specific scopes (e.g., "lumo"). The fork push uses cookie
	// auth, and the child session uses the parent's cookie jar with
	// CookieTransport — cookies have Domain=proton.me so they work for
	// all *.proton.me subdomains.
	if acctConfig.CookieAuth && cookieStore != nil {
		slog.Debug("restore service session: cookie auth mode", "service", service)

		// Check if the service session already exists and is fresh.
		svcConfig, svcErr := store.Load()
		if svcErr != nil && !errors.Is(svcErr, api.ErrKeyNotFound) {
			return nil, fmt.Errorf("restore service session %q: %w", service, svcErr)
		}

		if !shouldFork(svcConfig, svcErr, acctConfig, service) {
			// Service session is fresh — restore it with cookie auth.
			slog.Debug("restore service session: reusing fresh cookie session", "service", service)
			return restoreExistingCookieService(ctx, svcConfig, store, cookieStore, acctConfig, svc, service, managerHook)
		}

		// Service session missing or stale — fork from account.
		acctSession, err := CookieSessionRestore(ctx, options, cookieStore, acctConfig, managerHook)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: %w", service, err)
		}

		keypass, err := api.Base64Decode(acctConfig.SaltedKeyPass)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: decode keypass: %w", service, err)
		}

		child, childKeyPass, err := CookieFork(ctx, acctSession, acctConfig, svc, "", keypass, cookieStore)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: fork: %w", service, err)
		}

		if err := forkUnlockAndSave(ctx, child, childKeyPass, store, service); err != nil {
			return nil, err
		}

		return child, nil
	}

	// Build account session using account-specific options (not the service options).
	acctSvc, _ := api.LookupService("account")
	acctOpts := []proton.Option{
		proton.WithHostURL(acctSvc.Host),
		proton.WithAppVersion(acctSvc.AppVersion("")),
	}
	acctSession, err := SessionFromCredentials(ctx, acctOpts, acctConfig, managerHook)
	if err != nil {
		return nil, fmt.Errorf("restore service session %q: account credentials: %w", service, err)
	}

	acctSession.AppVersion = acctSvc.AppVersion("")
	acctSession.BaseURL = acctSvc.Host

	// Restore cookies into account session.
	api.LoadCookies(acctSession.CookieJar(), acctConfig.Cookies, api.CookieURL())

	// Register auth handler on account session so proactive refresh persists tokens.
	acctSession.AddAuthHandler(NewAuthHandler(accountStore, acctSession))
	acctSession.AddDeauthHandler(NewDeauthHandler())

	// Proactive refresh on account session.
	if err := proactiveRefresh(ctx, acctSession, acctConfig); err != nil {
		return nil, err
	}

	// Try loading the service session.
	svcConfig, svcErr := store.Load()
	if svcErr != nil && !errors.Is(svcErr, api.ErrKeyNotFound) {
		return nil, fmt.Errorf("restore service session %q: %w", service, svcErr)
	}

	needsFork := shouldFork(svcConfig, svcErr, acctConfig, service)

	if needsFork {
		// Decrypt account keypass for fork blob.
		keypass, err := api.Base64Decode(acctConfig.SaltedKeyPass)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: decode keypass: %w", service, err)
		}

		// Log cookies available for the target service host.
		if targetURL, err := url.Parse(svc.Host); err == nil {
			cookies := acctSession.CookieJar().Cookies(targetURL)
			names := make([]string, len(cookies))
			for i, c := range cookies {
				names[i] = c.Name
			}
			slog.Debug("fork.cookies", "host", svc.Host, "cookies", names)
		}

		var child *api.Session
		var childKeyPass []byte

		if acctConfig.CookieAuth && cookieStore != nil {
			child, childKeyPass, err = CookieFork(ctx, acctSession, acctConfig, svc, version, keypass, cookieStore)
		} else {
			child, childKeyPass, err = ForkSessionWithKeyPass(ctx, acctSession, svc, version, keypass)
		}
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: fork: %w", service, err)
		}

		if err := forkUnlockAndSave(ctx, child, childKeyPass, store, service); err != nil {
			return nil, err
		}

		return child, nil
	}

	// Restore existing service session.
	return restoreExistingService(ctx, options, svcConfig, store, svc, service, managerHook)
}

// restoreExistingService restores a service session from persisted credentials.
// Used when the service session exists and is not stale.
func restoreExistingService(ctx context.Context, options []proton.Option, svcConfig *api.SessionCredentials, store api.SessionStore, svc api.ServiceConfig, service string, managerHook func(*proton.Manager)) (*api.Session, error) {
	session, err := SessionFromCredentials(ctx, options, svcConfig, managerHook)
	if err != nil {
		return nil, fmt.Errorf("restore service session %q: credentials: %w", service, err)
	}

	api.LoadCookies(session.CookieJar(), svcConfig.Cookies, api.CookieURL())

	keypass, err := api.Base64Decode(svcConfig.SaltedKeyPass)
	if err != nil {
		return nil, fmt.Errorf("restore service session %q: decode keypass: %w", service, err)
	}

	addrs, err := session.Client.GetAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("restore service session %q: get addresses: %w", service, err)
	}

	if err := session.Unlock(keypass, addrs); err != nil {
		return nil, fmt.Errorf("restore service session %q: unlock: %w", service, err)
	}

	session.BaseURL = svc.Host
	session.AppVersion = svc.AppVersion("")

	session.AddAuthHandler(NewAuthHandler(store, session))
	session.AddDeauthHandler(NewDeauthHandler())

	return session, nil
}

// restoreExistingCookieService restores a service session that was created
// via cookieFork. The saved session has empty Bearer tokens (cleared after
// cookie transition) but valid cookies persisted in the service store.
// Uses CookieTransport so Resty-based calls use cookie auth, with 401
// retry via RefreshCookies.
func restoreExistingCookieService(ctx context.Context, svcConfig *api.SessionCredentials, store api.SessionStore, _ api.SessionStore, acctConfig *api.SessionCredentials, svc api.ServiceConfig, service string, managerHook func(*proton.Manager)) (*api.Session, error) {
	// Build cookie jar and inject persisted cookies from the service store.
	jar := NewProtonCookieJar(svcConfig.Cookies, svc.Host)

	// Proactive cookie refresh if stale.
	if NeedsCookieRefresh(svcConfig.LastRefresh) {
		slog.Debug("restoreExistingCookieService.proactiveRefresh", "service", service, "age", time.Since(svcConfig.LastRefresh))

		cs := NewCookieSessionForRefresh(svcConfig.UID, svc.Host, svc.AppVersion(""), jar, store)
		if refreshErr := cs.RefreshCookies(ctx); refreshErr != nil {
			slog.Debug("restoreExistingCookieService: refresh failed, will re-fork", "service", service, "error", refreshErr)
			return nil, fmt.Errorf("restore service session %q: cookie refresh: %w", service, refreshErr)
		}

		// Rebuild the jar from the refreshed cookies.
		refreshedCfg := cs.Config()
		svcConfig.Cookies = refreshedCfg.Cookies
		svcConfig.LastRefresh = refreshedCfg.LastRefresh

		jar = NewProtonCookieJar(svcConfig.Cookies, svc.Host)

		if saveErr := store.Save(svcConfig); saveErr != nil {
			slog.Error("restoreExistingCookieService: persist refreshed cookies", "error", saveErr)
		}
	}

	// Build proton.Manager with CookieTransport.
	ct := &CookieTransport{Base: http.DefaultTransport}
	managerOpts := []proton.Option{
		proton.WithTransport(ct),
		proton.WithCookieJar(jar),
		proton.WithHostURL(svc.Host),
		proton.WithAppVersion(svc.AppVersion("")),
	}

	session := api.InitSessionWithJar(ctx, jar, managerOpts, managerHook)

	// Attach cookie refresh handler to the transport for 401 retry.
	attachCookieRefresh(ctx, svcConfig, jar, ct, store)

	// Create client with UID, empty Bearer tokens (cookie auth only).
	session.Client = session.Manager().NewClient(svcConfig.UID, "", "")
	session.Auth = proton.Auth{
		UID:          svcConfig.UID,
		AccessToken:  "",
		RefreshToken: "",
	}

	// Load user and addresses from account cache, falling back to API.
	acctCache := newAccountCache(acctConfig.UID)
	c := &Client{cache: acctCache}
	user := c.getUser()
	if user == nil {
		u, err := session.Client.GetUser(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: get user: %w", service, err)
		}
		user = &u
		c.putUser(u)
	}
	session.SetUser(*user)

	addrs := c.getAddresses()
	if addrs == nil {
		var err error
		addrs, err = session.Client.GetAddresses(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: get addresses: %w", service, err)
		}
		c.putAddresses(addrs)
	}

	keypass, err := api.Base64Decode(acctConfig.SaltedKeyPass)
	if err != nil {
		return nil, fmt.Errorf("restore service session %q: decode keypass: %w", service, err)
	}
	if err := session.Unlock(keypass, addrs); err != nil {
		return nil, fmt.Errorf("restore service session %q: unlock: %w", service, err)
	}

	session.BaseURL = svc.Host
	session.AppVersion = svc.AppVersion("")

	session.AddAuthHandler(NewAuthHandler(store, session))
	session.AddDeauthHandler(NewDeauthHandler())

	return session, nil
}

// logCookies logs the current cookie names in the session's jar for debugging.
// Only names are logged — values are sensitive and must not appear in logs.
func logCookies(label string, session *api.Session) {
	u := cookieQueryURL(session.BaseURL)
	cookies := session.CookieJar().Cookies(u)
	names := make([]string, len(cookies))
	for i, c := range cookies {
		names[i] = c.Name
	}
	slog.Debug(label, "cookies", names)
}
