package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api/pool"
)

// serialCookie holds the minimal fields needed to reconstruct an http.Cookie
// for jar injection. Expiry is not persisted — the API server manages cookie
// lifetime.
type serialCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// MaxAutoWorkers is the upper bound for the auto-detected worker count.
// Proton's storage API rate-limits above ~64 concurrent requests.
const MaxAutoWorkers = 64

// DefaultMaxWorkers returns 3× the number of logical CPU cores, capped
// at MaxAutoWorkers. Minimum 2. This is the default concurrency limit
// for session operations and block pipelines.
func DefaultMaxWorkers() int {
	n := runtime.NumCPU() * 3
	if n < 2 {
		n = 2
	}
	if n > MaxAutoWorkers {
		n = MaxAutoWorkers
	}
	return n
}

// DefaultThrottleBackoff is the initial backoff duration for rate limiting.
const DefaultThrottleBackoff = time.Second

// DefaultThrottleMaxDelay is the maximum backoff duration for rate limiting.
const DefaultThrottleMaxDelay = 30 * time.Second

// ProactiveRefreshAge is the token age threshold for proactive refresh.
// When age exceeds this value, a lightweight API call triggers token refresh.
const ProactiveRefreshAge = 1 * time.Hour

// CookieRefreshAge is the cookie age threshold for proactive refresh.
// Cookie sessions use the same 1-hour threshold as bearer tokens —
// the session lifetime is 24 hours for both.
const CookieRefreshAge = 1 * time.Hour

// ProtonAccept is the Accept header value for Proton API requests.
// The vendor media type triggers full API behavior including service-specific
// scope grants on fork responses.
const ProtonAccept = "application/vnd.protonmail.v1+json"

// TokenWarnAge is the age at which session tokens are considered near expiry.
const TokenWarnAge = 20 * time.Hour

// TokenExpireAge is the age at which session tokens are considered expired.
const TokenExpireAge = 24 * time.Hour

// apiCookieURL returns the parsed Proton API base URL used for cookie scoping.
func apiCookieURL() *url.URL {
	u, _ := url.Parse(proton.DefaultHostURL)
	return u
}

// serializeCookies extracts cookies from the jar for the given API URL.
func serializeCookies(jar http.CookieJar, apiURL *url.URL) []serialCookie {
	cookies := jar.Cookies(apiURL)
	if len(cookies) == 0 {
		return nil
	}
	out := make([]serialCookie, len(cookies))
	for i, c := range cookies {
		out[i] = serialCookie{
			Name:   c.Name,
			Value:  c.Value,
			Domain: c.Domain,
			Path:   c.Path,
		}
	}
	return out
}

// loadCookies injects persisted cookies into the jar for the given API URL.
func loadCookies(jar http.CookieJar, cookies []serialCookie, apiURL *url.URL) {
	if len(cookies) == 0 {
		return
	}
	httpCookies := make([]*http.Cookie, len(cookies))
	for i, c := range cookies {
		httpCookies[i] = &http.Cookie{
			Name:   c.Name,
			Value:  c.Value,
			Domain: c.Domain,
			Path:   c.Path,
		}
	}
	jar.SetCookies(apiURL, httpCookies)
}

// Doer is the interface for making authenticated Proton API requests.
// Both Session (Bearer auth) and CookieSession (cookie auth) implement this.
type Doer interface {
	DoJSON(ctx context.Context, method, path string, body, result any) error
	DoSSE(ctx context.Context, path string, body any) (io.ReadCloser, error)
}

// Session holds an authenticated Proton API session with decrypted keyrings.
type Session struct {
	Client     *proton.Client
	Auth       proton.Auth
	BaseURL    string // override for DoJSON; defaults to proton.DefaultHostURL
	AppVersion string // x-pm-appversion header value for DoJSON requests
	UserAgent  string // User-Agent header value for DoJSON requests
	manager    *proton.Manager

	cookieJar http.CookieJar
	authMu    sync.Mutex // serializes auth handler updates

	// cachedAuthInfo holds the AuthInfo from the initial login attempt.
	// It is reused on HV retry so the SRP session matches the solved CAPTCHA.
	cachedAuthInfo *proton.AuthInfo

	Pool     *pool.Pool
	Throttle *Throttle

	addresses       map[string]proton.Address
	addressKeyRings map[string]*crypto.KeyRing

	user        proton.User
	UserKeyRing *crypto.KeyRing
}

// SessionFromCredentials initializes a new session from the provided config.
// The session is not fully usable until it has been Unlock'ed using the
// user-provided keypass.
func SessionFromCredentials(ctx context.Context, options []proton.Option, config *SessionConfig, managerHook func(*proton.Manager)) (*Session, error) {
	var err error

	if config.UID == "" {
		return nil, ErrMissingUID
	}

	if config.AccessToken == "" {
		return nil, ErrMissingAccessToken
	}

	if config.RefreshToken == "" {
		return nil, ErrMissingRefreshToken
	}

	var session Session
	session.Throttle = NewThrottle(DefaultThrottleBackoff, DefaultThrottleMaxDelay)
	session.Pool = pool.New(ctx, DefaultMaxWorkers(), pool.WithThrottle(session.Throttle))

	jar, _ := cookiejar.New(nil)
	session.cookieJar = jar

	slog.Debug("session.refresh client")

	session.manager = proton.New(append(options, proton.WithCookieJar(jar))...)

	if managerHook != nil {
		managerHook(session.manager)
	}

	slog.Debug("session.config", "uid", config.UID, "access_token", "<redacted>", "refresh_token", "<redacted>")
	session.Client = session.manager.NewClient(config.UID, config.AccessToken, config.RefreshToken)
	session.Auth = proton.Auth{
		UID:          config.UID,
		AccessToken:  config.AccessToken,
		RefreshToken: config.RefreshToken,
	}

	slog.Debug("session.GetUser")
	session.user, err = session.Client.GetUser(ctx)
	if err != nil {
		return nil, err
	}

	return &session, nil
}

// sessionFromLogin creates a session with common setup shared by
// SessionFromLogin and SessionFromLoginWithHV. It returns the prepared
// session and manager; the caller performs the actual login call.
func sessionFromLogin(ctx context.Context, options []proton.Option, managerHook func(*proton.Manager)) (*Session, *proton.Manager) {
	session := &Session{}
	session.Throttle = NewThrottle(DefaultThrottleBackoff, DefaultThrottleMaxDelay)
	session.Pool = pool.New(ctx, DefaultMaxWorkers(), pool.WithThrottle(session.Throttle))

	jar, _ := cookiejar.New(nil)
	session.cookieJar = jar

	session.manager = proton.New(append(options, proton.WithCookieJar(jar))...)

	if managerHook != nil {
		managerHook(session.manager)
	}

	return session, session.manager
}

// Unlock decrypts the user's account keyring and all address keyrings.
// The addresses slice is stored internally for backward compatibility with
// Drive methods that still reference s.addresses until they move to
// drive.Client.
func (s *Session) Unlock(keypass []byte, addresses []proton.Address) error {
	s.addresses = make(map[string]proton.Address, len(addresses))
	for _, addr := range addresses {
		s.addresses[addr.Email] = addr
	}

	var err error
	s.UserKeyRing, s.addressKeyRings, err = proton.Unlock(s.user, addresses, keypass, nil)
	return err
}

// AddressKeyRings returns the address keyrings produced by Unlock.
// Service-specific clients copy this map during their construction.
func (s *Session) AddressKeyRings() map[string]*crypto.KeyRing {
	return s.addressKeyRings
}

// User returns the proton.User for this session.
func (s *Session) User() proton.User { return s.user }

// SetUser sets the proton.User for this session.
// Used by cookie login to populate the user from a DoJSON response
// before calling Unlock.
func (s *Session) SetUser(u proton.User) { s.user = u }

// CookieJar returns the session's cookie jar.
func (s *Session) CookieJar() http.CookieJar { return s.cookieJar }

// SetCookieJar sets the session's cookie jar.
// Used by cookie login to inject the anonymous session's jar before
// transitioning to cookies.
func (s *Session) SetCookieJar(jar http.CookieJar) { s.cookieJar = jar }

// Addresses fetches addresses from the API.
// Service-specific clients call this during their own construction.
func (s *Session) Addresses(ctx context.Context) ([]proton.Address, error) {
	return s.Client.GetAddresses(ctx)
}

// AddAuthHandler registers a handler for authentication events.
func (s *Session) AddAuthHandler(handler proton.AuthHandler) {
	s.Client.AddAuthHandler(handler)
}

// AddDeauthHandler registers a handler for deauthentication events.
func (s *Session) AddDeauthHandler(handler proton.Handler) {
	s.Client.AddDeauthHandler(handler)
}

// Stop closes the underlying API manager.
func (s *Session) Stop() {
	s.manager.Close()
}

// resolveAppVersion returns the x-pm-appversion value for the given request
// URL. If the URL targets a known service host, returns that service's app
// version. Otherwise falls back to s.AppVersion.
func (s *Session) resolveAppVersion(reqURL string) string {
	u, err := url.Parse(reqURL)
	if err != nil || u.Host == "" {
		return s.AppVersion
	}
	svc, err := LookupServiceByHost(u.Hostname())
	if err != nil {
		return s.AppVersion
	}
	return svc.AppVersion("")
}

// apiEnvelope is the standard Proton API response wrapper.
type apiEnvelope struct {
	Code    int             `json:"Code"`
	Error   string          `json:"Error,omitempty"`
	Details json.RawMessage `json:"Details,omitempty"`
}

// DoJSON executes an authenticated JSON API request against the Proton API.
// Method is "GET", "POST", "DELETE", etc. Path is relative to the API base
// (e.g. "/drive/shares/{id}/members"). If body is non-nil it is JSON-encoded
// as the request body. If result is non-nil the response body is JSON-decoded
// into it. Returns an *Error on non-success API responses.
func (s *Session) DoJSON(ctx context.Context, method, path string, body, result any) error {
	reqURL := path
	if !strings.HasPrefix(path, "http") {
		base := s.BaseURL
		if base == "" {
			base = proton.DefaultHostURL
		}
		reqURL = base + path
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("doJSON: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("doJSON: new request: %w", err)
	}

	req.Header.Set("x-pm-uid", s.Auth.UID)
	// Only set Bearer auth when we have a token. Cookie-mode sessions
	// have empty AccessToken — auth is provided via cookies in the jar.
	if s.Auth.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.Auth.AccessToken)
	}
	appVer := s.resolveAppVersion(reqURL)
	if appVer != "" {
		req.Header.Set("x-pm-appversion", appVer)
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", ProtonAccept)

	slog.Debug("doJSON.request", "method", method, "url", reqURL, "appversion", appVer)

	// Log outgoing cookie names for debugging.
	if s.cookieJar != nil {
		if reqParsed, parseErr := url.Parse(reqURL); parseErr == nil {
			outCookies := s.cookieJar.Cookies(reqParsed)
			names := make([]string, len(outCookies))
			for i, c := range outCookies {
				names[i] = c.Name
			}
			slog.Debug("doJSON.sending-cookies", "url", reqURL, "cookies", names)
		}
	}

	httpClient := &http.Client{Jar: s.cookieJar}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("doJSON: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Log Set-Cookie headers from the response.
	if setCookies := resp.Header.Values("Set-Cookie"); len(setCookies) > 0 {
		names := make([]string, len(setCookies))
		for i, sc := range setCookies {
			if idx := strings.Index(sc, "="); idx > 0 {
				names[i] = sc[:idx]
			} else {
				names[i] = sc[:min(20, len(sc))]
			}
		}
		slog.Debug("doJSON.set-cookie", "url", reqURL, "cookies", names)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("doJSON: read response: %w", err)
	}

	// Parse the envelope to check the API-level error code.
	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("doJSON: unmarshal envelope: %w", err)
	}

	if envelope.Code != 1000 && envelope.Code != 1001 {
		slog.Debug("doJSON.error", "method", method, "url", reqURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
		return &Error{
			Status:  resp.StatusCode,
			Code:    envelope.Code,
			Message: envelope.Error,
			Details: envelope.Details,
		}
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("doJSON: unmarshal result: %w", err)
		}
	}

	return nil
}

// DoJSONCookie executes an authenticated JSON API request using cookie-based
// auth. Instead of the Authorization: Bearer header, auth is provided via the
// AUTH-<uid>=<token> cookie in the session's cookie jar. The x-pm-uid header
// is still sent. The x-pm-appversion is resolved from the target URL's host.
func (s *Session) DoJSONCookie(ctx context.Context, method, path string, body, result any) error {
	reqURL := path
	if !strings.HasPrefix(path, "http") {
		base := s.BaseURL
		if base == "" {
			base = proton.DefaultHostURL
		}
		reqURL = base + path
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("doJSONCookie: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("doJSONCookie: new request: %w", err)
	}

	req.Header.Set("x-pm-uid", s.Auth.UID)
	// Prefer cookie auth (AUTH-<uid>=<token>) but fall back to Bearer.
	// The AUTH cookie is set by POST /core/v4/auth/cookies. If not present,
	// Bearer auth works but grants restricted scopes. Cookie-mode sessions
	// have empty AccessToken.
	if s.Auth.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.Auth.AccessToken)
	}
	appVer := s.resolveAppVersion(reqURL)
	if appVer != "" {
		req.Header.Set("x-pm-appversion", appVer)
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", ProtonAccept)

	slog.Debug("doJSONCookie.request", "method", method, "url", reqURL, "appversion", appVer)

	// Log cookies being sent.
	if reqParsed, err := url.Parse(reqURL); err == nil {
		outCookies := s.cookieJar.Cookies(reqParsed)
		names := make([]string, len(outCookies))
		for i, c := range outCookies {
			names[i] = c.Name
		}
		slog.Debug("doJSONCookie.sending-cookies", "url", reqURL, "cookies", names)
	}

	httpClient := &http.Client{Jar: s.cookieJar}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("doJSONCookie: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Log Set-Cookie headers from the response.
	if setCookies := resp.Header.Values("Set-Cookie"); len(setCookies) > 0 {
		names := make([]string, len(setCookies))
		for i, sc := range setCookies {
			if idx := strings.Index(sc, "="); idx > 0 {
				names[i] = sc[:idx]
			} else {
				names[i] = sc[:min(20, len(sc))]
			}
		}
		slog.Debug("doJSONCookie.set-cookie", "url", reqURL, "cookies", names)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("doJSONCookie: read response: %w", err)
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("doJSONCookie: unmarshal envelope: %w", err)
	}

	if envelope.Code != 1000 && envelope.Code != 1001 {
		slog.Debug("doJSONCookie.error", "method", method, "url", reqURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
		return &Error{
			Status:  resp.StatusCode,
			Code:    envelope.Code,
			Message: envelope.Error,
			Details: envelope.Details,
		}
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("doJSONCookie: unmarshal result: %w", err)
		}
	}

	return nil
}

// SessionRestore loads credentials from the store and creates an unlocked
// session. Returns ErrNotLoggedIn if no session is stored. When the loaded
// config has CookieAuth=true and cookieStore is non-nil, the cookie restore
// path is used instead of the Bearer path.
func SessionRestore(ctx context.Context, options []proton.Option, store SessionStore, cookieStore SessionStore, managerHook func(*proton.Manager)) (*Session, error) {
	config, err := store.Load()
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			return nil, ErrNotLoggedIn
		}
		return nil, err
	}

	// Route to cookie restore when CookieAuth=true.
	if config.CookieAuth && cookieStore != nil {
		return CookieSessionRestore(ctx, options, cookieStore, config, managerHook)
	}

	slog.Debug("SessionRestore", "uid", config.UID, "access_token", "<redacted>", "refresh_token", "<redacted>")

	// Staleness check.
	if !config.LastRefresh.IsZero() {
		age := time.Since(config.LastRefresh)
		if age > TokenExpireAge {
			slog.Warn("session tokens likely expired", "age", age)
		} else if age > TokenWarnAge {
			slog.Warn("session tokens near expiry", "age", age)
		}
	}

	session, err := SessionFromCredentials(ctx, options, config, managerHook)
	if err != nil {
		return nil, err
	}

	// Restore persisted cookies into the session's jar.
	loadCookies(session.cookieJar, config.Cookies, apiCookieURL())

	keypass, err := Base64Decode(config.SaltedKeyPass)
	if err != nil {
		return nil, err
	}

	addrs, err := session.Client.GetAddresses(ctx)
	if err != nil {
		return nil, err
	}

	if err := session.Unlock(keypass, addrs); err != nil {
		return nil, err
	}

	// Proactive refresh: make a lightweight API call to trigger
	// go-proton-api's auto-refresh if the access token is expired.
	if !config.LastRefresh.IsZero() && time.Since(config.LastRefresh) > TokenExpireAge {
		if _, err := session.Client.GetUser(ctx); err != nil {
			return nil, fmt.Errorf("proactive refresh: %w", err)
		}
	}

	return session, nil
}

// ReadySession restores a session from the store, registers auth/deauth
// handlers, and returns a fully initialized Session ready for use.
// This is the recommended entry point for consumers that need an
// authenticated session. When cookieStore is non-nil and the session has
// CookieAuth=true, the cookie restore path is used.
func ReadySession(ctx context.Context, options []proton.Option, store SessionStore, cookieStore SessionStore, managerHook func(*proton.Manager)) (*Session, error) {
	session, err := SessionRestore(ctx, options, store, cookieStore, managerHook)
	if err != nil {
		return nil, err
	}
	session.AddAuthHandler(NewAuthHandler(store, session))
	session.AddDeauthHandler(NewDeauthHandler())
	return session, nil
}

// NeedsProactiveRefresh reports whether the session's LastRefresh age exceeds
// ProactiveRefreshAge. A zero-valued LastRefresh always triggers refresh.
func NeedsProactiveRefresh(lastRefresh time.Time) bool {
	if lastRefresh.IsZero() {
		return true
	}
	return time.Since(lastRefresh) > ProactiveRefreshAge
}

// NeedsCookieRefresh reports whether the cookie session's LastRefresh age
// exceeds CookieRefreshAge. A zero-valued LastRefresh always triggers refresh.
func NeedsCookieRefresh(lastRefresh time.Time) bool {
	if lastRefresh.IsZero() {
		return true
	}
	return time.Since(lastRefresh) > CookieRefreshAge
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

// proactiveRefresh checks the session's LastRefresh age and triggers a
// refresh if the token is past the ProactiveRefreshAge threshold.
// The auth handler callback updates Session.Auth and persists via SessionSave.
func proactiveRefresh(ctx context.Context, session *Session, config *SessionConfig) error {
	if !NeedsProactiveRefresh(config.LastRefresh) {
		return nil
	}

	slog.Debug("proactiveRefresh", "age", time.Since(config.LastRefresh))

	if _, err := session.Client.GetUser(ctx); err != nil {
		return fmt.Errorf("session expired, run `proton account login`: %w", err)
	}

	return nil
}

// RestoreServiceSession loads and unlocks a session for the given service.
// If no session exists for the service, it forks from the account session.
// If no account session exists, it returns ErrNotLoggedIn.
//
// The flow:
//  1. Look up ServiceConfig for the requested service.
//  2. Load session from store (service-specific store).
//  3. Load account session from accountStore.
//  4. If account session age > ProactiveRefreshAge, trigger proactive refresh.
//  5. If service session missing or stale (account LastRefresh > service LastRefresh),
//     fork from account session via ForkSessionWithKeyPass.
//  6. Set session.BaseURL and AppVersion from the ServiceConfig.
//  7. Return session.
func RestoreServiceSession(ctx context.Context, service string, options []proton.Option, store SessionStore, accountStore SessionStore, cookieStore SessionStore, version string, managerHook func(*proton.Manager)) (*Session, error) {
	svc, err := LookupService(service)
	if err != nil {
		return nil, err
	}

	// Load account session config (needed for staleness check and fork source).
	acctConfig, err := accountStore.Load()
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			return nil, ErrNotLoggedIn
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

		// Restore the cookie session for the fork push.
		acctSession, err := CookieSessionRestore(ctx, options, cookieStore, acctConfig, managerHook)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: %w", service, err)
		}

		keypass, err := Base64Decode(acctConfig.SaltedKeyPass)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: decode keypass: %w", service, err)
		}

		// Cookie fork: push with AUTH cookie, pull gets service scopes.
		child, childKeyPass, err := cookieFork(ctx, acctSession, acctConfig, svc, "", keypass, cookieStore)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: fork: %w", service, err)
		}

		childUser, err := child.Client.GetUser(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: get user: %w", service, err)
		}
		child.user = childUser

		if err := SessionSave(store, child, childKeyPass); err != nil {
			return nil, fmt.Errorf("restore service session %q: save fork: %w", service, err)
		}

		addrs, err := child.Client.GetAddresses(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: get addresses: %w", service, err)
		}
		if err := child.Unlock(childKeyPass, addrs); err != nil {
			return nil, fmt.Errorf("restore service session %q: unlock: %w", service, err)
		}

		child.AddAuthHandler(NewAuthHandler(store, child))
		child.AddDeauthHandler(NewDeauthHandler())

		return child, nil
	}

	// Build account session using account-specific options (not the service options).
	// The passed-in options point to the target service host, but the account
	// session must use the account host for proactive refresh and fork push.
	acctSvc, _ := LookupService("account")
	acctOpts := []proton.Option{
		proton.WithHostURL(acctSvc.Host),
		proton.WithAppVersion(acctSvc.AppVersion("")),
	}
	acctSession, err := SessionFromCredentials(ctx, acctOpts, acctConfig, managerHook)
	if err != nil {
		return nil, fmt.Errorf("restore service session %q: account credentials: %w", service, err)
	}

	// Set AppVersion and BaseURL on the session struct so DoJSON sends the
	// x-pm-appversion header. SessionFromCredentials passes options to the
	// proton.Manager (for Resty), but DoJSON uses Session.AppVersion directly.
	acctSession.AppVersion = acctSvc.AppVersion("")
	acctSession.BaseURL = acctSvc.Host

	// Restore cookies into account session.
	loadCookies(acctSession.cookieJar, acctConfig.Cookies, apiCookieURL())

	// Register auth handler on account session so proactive refresh persists tokens.
	acctSession.AddAuthHandler(NewAuthHandler(accountStore, acctSession))
	acctSession.AddDeauthHandler(NewDeauthHandler())

	// Proactive refresh on account session.
	if err := proactiveRefresh(ctx, acctSession, acctConfig); err != nil {
		return nil, err
	}

	// Try loading the service session.
	svcConfig, svcErr := store.Load()

	needsFork := false
	switch {
	case svcErr != nil:
		if !errors.Is(svcErr, ErrKeyNotFound) {
			return nil, fmt.Errorf("restore service session %q: %w", service, svcErr)
		}
		slog.Debug("service session not found, will fork", "service", service)
		needsFork = true
	case svcConfig.Service != service && svcConfig.Service != "":
		// The store returned a wildcard fallback, not a service-specific session.
		slog.Debug("service session is wildcard fallback, will fork", "service", service, "found_service", svcConfig.Service)
		needsFork = true
	case svcConfig.Service == "":
		// Legacy session without Service field — treat as wildcard.
		slog.Debug("service session has no service field, will fork", "service", service)
		needsFork = true
	case IsStale(acctConfig.LastRefresh, svcConfig.LastRefresh):
		slog.Debug("service session is stale, will re-fork", "service", service)
		needsFork = true
	default:
		slog.Debug("service session is fresh", "service", service, "uid", svcConfig.UID, "svc_service", svcConfig.Service, "svc_last_refresh", svcConfig.LastRefresh, "acct_last_refresh", acctConfig.LastRefresh)
	}

	if needsFork {
		// Decrypt account keypass for fork blob.
		keypass, err := Base64Decode(acctConfig.SaltedKeyPass)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: decode keypass: %w", service, err)
		}

		// The fork push goes to the account host with the account app version.
		// The fork pull goes to the target service host with the target app version.

		// Log cookies available for the target service host.
		if targetURL, err := url.Parse(svc.Host); err == nil {
			cookies := acctSession.cookieJar.Cookies(targetURL)
			names := make([]string, len(cookies))
			for i, c := range cookies {
				names[i] = c.Name
			}
			slog.Debug("fork.cookies", "host", svc.Host, "cookies", names)
		}

		var child *Session
		var childKeyPass []byte

		if acctConfig.CookieAuth && cookieStore != nil {
			// Cookie-aware fork: use CookieSession for the fork push.
			child, childKeyPass, err = cookieFork(ctx, acctSession, acctConfig, svc, version, keypass, cookieStore)
		} else {
			// Bearer fork: existing behavior.
			child, childKeyPass, err = ForkSessionWithKeyPass(ctx, acctSession, svc, version, keypass)
		}
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: fork: %w", service, err)
		}

		// Fetch the user for the child session (needed for key unlock).
		childUser, err := child.Client.GetUser(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: get user: %w", service, err)
		}
		child.user = childUser

		// Save the forked session.
		if err := SessionSave(store, child, childKeyPass); err != nil {
			return nil, fmt.Errorf("restore service session %q: save fork: %w", service, err)
		}

		// Unlock the child session.
		addrs, err := child.Client.GetAddresses(ctx)
		if err != nil {
			return nil, fmt.Errorf("restore service session %q: get addresses: %w", service, err)
		}
		slog.Debug("fork.unlock", "service", service, "child_uid", child.Auth.UID, "keypass_len", len(childKeyPass), "num_addresses", len(addrs))
		if err := child.Unlock(childKeyPass, addrs); err != nil {
			return nil, fmt.Errorf("restore service session %q: unlock: %w", service, err)
		}

		child.AddAuthHandler(NewAuthHandler(store, child))
		child.AddDeauthHandler(NewDeauthHandler())

		return child, nil
	}

	// Restore existing service session.
	session, err := SessionFromCredentials(ctx, options, svcConfig, managerHook)
	if err != nil {
		return nil, fmt.Errorf("restore service session %q: credentials: %w", service, err)
	}

	loadCookies(session.cookieJar, svcConfig.Cookies, apiCookieURL())

	keypass, err := Base64Decode(svcConfig.SaltedKeyPass)
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

	// Set service-specific BaseURL and AppVersion.
	session.BaseURL = svc.Host
	session.AppVersion = svc.AppVersion("")

	session.AddAuthHandler(NewAuthHandler(store, session))
	session.AddDeauthHandler(NewDeauthHandler())

	return session, nil
}

// SessionSave persists session credentials, cookie jar state, and a refresh
// timestamp to the store.
func SessionSave(store SessionStore, session *Session, keypass []byte) error {
	apiURL := apiCookieURL()
	config := &SessionConfig{
		UID:           session.Auth.UID,
		AccessToken:   session.Auth.AccessToken,
		RefreshToken:  session.Auth.RefreshToken,
		SaltedKeyPass: Base64Encode(keypass),
		Cookies:       serializeCookies(session.cookieJar, apiURL),
		LastRefresh:   time.Now(),
	}
	return store.Save(config)
}

// SessionRevoke revokes the API session and deletes it from the store.
// If force is true, store deletion proceeds even when the API revoke fails.
func SessionRevoke(ctx context.Context, session *Session, store SessionStore, force bool) error {
	if session != nil {
		slog.Debug("SessionRevoke", "uid", session.Auth.UID)
		if err := session.Client.AuthRevoke(ctx, session.Auth.UID); err != nil {
			if !force {
				return err
			}
			slog.Error("SessionRevoke", "error", err)
		}
	}
	return store.Delete()
}

// SessionList returns account names from the session store.
func SessionList(store SessionStore) ([]string, error) {
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
func SessionFromLogin(ctx context.Context, options []proton.Option, username string, password string, hvDetails *proton.APIHVDetails, managerHook func(*proton.Manager)) (*Session, error) {
	session, manager := sessionFromLogin(ctx, options, managerHook)

	slog.Debug("session.login", "username", username, "password", "<hidden>")

	// Fetch AuthInfo separately so we can cache it for HV retries.
	// The SRP session in AuthInfo is bound to the CAPTCHA token — reusing
	// it on retry is required for the solved token to be accepted.
	info, err := manager.AuthInfo(ctx, proton.AuthInfoReq{Username: username})
	if err != nil {
		return session, err
	}
	session.cachedAuthInfo = &info

	session.Client, session.Auth, err = manager.NewClientWithLoginWithCachedInfo(ctx, info, username, []byte(password), hvDetails)
	logCookies("session.login.done", session)
	slog.Debug("session.login.done", "error", err)
	if err != nil {
		return session, err
	}

	return session, nil
}

// SessionRetryWithHV retries login on an existing session (reusing its
// manager and cookie jar) with HV details after the user solved the CAPTCHA.
// A fresh AuthInfo is fetched because the original SRP session is invalidated
// by the 9001 response. The solved CAPTCHA composite token is NOT bound to
// the SRP session — it's bound to the HumanVerificationToken.
func SessionRetryWithHV(ctx context.Context, session *Session, username, password string, hv *proton.APIHVDetails) error {
	logCookies("session.login.hv.before", session)
	slog.Debug("session.login.hv", "username", username, "password", "<hidden>")

	var err error
	session.Client, session.Auth, err = session.manager.NewClientWithLoginWithHVToken(ctx, username, []byte(password), hv)
	logCookies("session.login.hv.after", session)
	return err
}

// logCookies logs the current cookie names in the session's jar for debugging.
// Only names are logged — values are sensitive and must not appear in logs.
func logCookies(label string, session *Session) {
	apiURL := apiCookieURL()
	cookies := session.cookieJar.Cookies(apiURL)
	names := make([]string, len(cookies))
	for i, c := range cookies {
		names[i] = c.Name
	}
	slog.Debug(label, "cookies", names)
}
