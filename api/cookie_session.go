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
	"strings"
	"sync"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/pool"
)

// AuthCookiesReq is the request body for POST /core/v4/auth/cookies.
type AuthCookiesReq struct {
	UID          string `json:"UID"`
	RefreshToken string `json:"RefreshToken"`
	GrantType    string `json:"GrantType"`    // always "refresh_token"
	RedirectURI  string `json:"RedirectURI"`  // "https://proton.me"
	ResponseType string `json:"ResponseType"` // "token"
	State        string `json:"State"`        // random state string
}

// CookieSession holds cookie-based authentication state for services that
// require cookie auth (e.g., Lumo). Created from a Bearer session via
// TransitionToCookies, or restored from persisted cookies.
type CookieSession struct {
	UID        string               // Proton UID
	BaseURL    string               // API base URL (e.g., "https://account.proton.me/api")
	AppVersion string               // x-pm-appversion header value
	UserAgent  string               // User-Agent header value
	HVDetails  *proton.APIHVDetails // when non-nil, HV headers are added to requests
	Store      SessionStore         // when non-nil, cookies are persisted after refresh
	cookieJar  http.CookieJar       // contains AUTH-<uid>, REFRESH-<uid>, Session-Id
	mu         sync.Mutex           // serializes cookie refresh
}

// NewCookieSession creates a CookieSession with the given parameters.
// Used by tests and callers that need to construct a CookieSession directly
// rather than via TransitionToCookies or CookieSessionFromConfig.
func NewCookieSession(uid, baseURL string, jar http.CookieJar) *CookieSession {
	return &CookieSession{
		UID:       uid,
		BaseURL:   baseURL,
		cookieJar: jar,
	}
}

// CookieDomain is the domain used for all Proton session cookies.
// The server sets Domain=proton.me on Set-Cookie headers, making cookies
// valid for all *.proton.me subdomains. We use this constant when loading
// persisted cookies back into a jar so the domain scoping is preserved.
const CookieDomain = "proton.me"

// loadProtonCookies injects persisted cookies into the jar with correct
// domain scoping. For Proton domains (*.proton.me), Domain is set to
// proton.me so cookies match all subdomains. For other domains (e.g.,
// localhost in tests), the original domain is preserved.
func loadProtonCookies(jar http.CookieJar, cookies []serialCookie, baseURL string) {
	if len(cookies) == 0 {
		return
	}

	// Determine if this is a Proton domain.
	isProton := false
	u, err := url.Parse(baseURL)
	if err == nil {
		host := u.Hostname()
		isProton = host == CookieDomain || strings.HasSuffix(host, "."+CookieDomain)
	}

	// For Proton domains, use proton.me as the SetCookies URL so the jar
	// accepts Domain=proton.me cookies. For other domains, use the baseURL.
	setCookieURL := u
	if isProton {
		setCookieURL, _ = url.Parse("https://proton.me/")
	}

	httpCookies := make([]*http.Cookie, len(cookies))
	for i, c := range cookies {
		path := c.Path
		if path == "" && isProton {
			switch {
			case strings.HasPrefix(c.Name, "REFRESH-"):
				path = "/api/auth/refresh"
			case strings.HasPrefix(c.Name, "AUTH-"):
				path = "/api/"
			default:
				path = "/"
			}
		}
		domain := c.Domain
		if isProton && (domain == "" || domain == CookieDomain || strings.HasSuffix(domain, "."+CookieDomain)) {
			domain = CookieDomain
		}
		httpCookies[i] = &http.Cookie{
			Name:   c.Name,
			Value:  c.Value,
			Domain: domain,
			Path:   path,
		}
	}
	jar.SetCookies(setCookieURL, httpCookies)
}

// cookieQueryURL returns a URL suitable for querying the cookie jar.
// Proton's Set-Cookie headers use different paths for different cookies:
//   - AUTH-<uid>:    path=/api/
//   - REFRESH-<uid>: path=/api/auth/refresh
//
// To retrieve ALL cookies from the jar in a single query, the URL path
// must be within the most restrictive cookie path. We use /api/auth/refresh
// which matches both AUTH (parent path /api/) and REFRESH (exact path).
func cookieQueryURL(baseURL string) *url.URL {
	if baseURL == "" {
		baseURL = AccountHost()
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		u, _ = url.Parse(AccountHost() + "/auth/refresh")
	}
	// Override path to match the most restrictive cookie path.
	u.Path = "/api/auth/refresh"
	return u
}

// logCookieSession logs the state of a CookieSession for debugging. Logs
// cookie names only — values are sensitive and must not appear in logs.
// Consistent with logCookies for Bearer sessions.
func logCookieSession(cs *CookieSession, msg string) {
	u := cookieQueryURL(cs.BaseURL)
	cookies := cs.cookieJar.Cookies(u)
	names := make([]string, len(cookies))
	for i, c := range cookies {
		names[i] = c.Name
	}
	slog.Debug(msg, "uid", cs.UID, "baseURL", cs.BaseURL, "appversion", cs.AppVersion, "cookies", names)
}

// resolveAppVersion returns the x-pm-appversion value for the given request
// URL. If the URL targets a known service host, returns that service's app
// version. Otherwise falls back to cs.AppVersion.
func (cs *CookieSession) resolveAppVersion(reqURL string) string {
	u, err := url.Parse(reqURL)
	if err != nil || u.Host == "" {
		return cs.AppVersion
	}
	svc, err := LookupServiceByHost(u.Hostname())
	if err != nil {
		return cs.AppVersion
	}
	return svc.AppVersion("")
}

// buildURL resolves a path against BaseURL. If path is already an absolute
// URL (starts with "http"), it is returned as-is.
func (cs *CookieSession) buildURL(path string) string {
	if strings.HasPrefix(path, "http") {
		return path
	}
	base := cs.BaseURL
	if base == "" {
		base = AccountHost()
	}
	return base + path
}

// DoJSON executes an authenticated JSON API request using cookie auth.
// No Authorization: Bearer header is sent — auth is provided via the
// AUTH-<uid> cookie in the cookie jar. The x-pm-uid header is always set.
//
// On success (Code 1000), result is populated if non-nil. On API error,
// returns *Error with Status, Code, and Message. On 401, attempts a cookie
// refresh via RefreshCookies and retries the request once.
func (cs *CookieSession) DoJSON(ctx context.Context, method, path string, body, result any) error {
	reqURL := cs.buildURL(path)

	// First attempt.
	err := cs.doJSONOnce(ctx, method, reqURL, body, result)
	if err == nil {
		return nil
	}

	// On 401: attempt cookie refresh and retry once.
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status == 401 {
		slog.Debug("cookieSession.DoJSON.401-retry", "method", method, "url", reqURL)
		if refreshErr := cs.RefreshCookies(ctx); refreshErr != nil {
			return fmt.Errorf("cookie refresh after 401: %w", refreshErr)
		}
		return cs.doJSONOnce(ctx, method, reqURL, body, result)
	}

	return err
}

// doJSONOnce executes a single authenticated JSON API request using cookie
// auth. This is the inner implementation used by DoJSON; it does not retry.
func (cs *CookieSession) doJSONOnce(ctx context.Context, method, reqURL string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cookieSession.DoJSON: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("cookieSession.DoJSON: new request: %w", err)
	}

	req.Header.Set("x-pm-uid", cs.UID)
	// No Authorization: Bearer header — cookie auth only.
	appVer := cs.resolveAppVersion(reqURL)
	if appVer != "" {
		req.Header.Set("x-pm-appversion", appVer)
	}
	if cs.UserAgent != "" {
		req.Header.Set("User-Agent", cs.UserAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", ProtonAccept)

	// Add HV headers when a solved CAPTCHA token is present.
	if cs.HVDetails != nil {
		req.Header.Set("x-pm-human-verification-token", cs.HVDetails.Token)
		req.Header.Set("x-pm-human-verification-token-type", "captcha")
	}

	slog.Debug("cookieSession.doJSONOnce.request", "method", method, "url", reqURL, "appversion", appVer)

	// Log cookies being sent.
	if reqParsed, parseErr := url.Parse(reqURL); parseErr == nil {
		outCookies := cs.cookieJar.Cookies(reqParsed)
		names := make([]string, len(outCookies))
		for i, c := range outCookies {
			names[i] = c.Name
		}
		slog.Debug("cookieSession.doJSONOnce.sending-cookies", "url", reqURL, "cookies", names)
	}

	httpClient := &http.Client{Jar: cs.cookieJar}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cookieSession.DoJSON: %s %s: %w", method, reqURL, err)
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
		slog.Debug("cookieSession.doJSONOnce.set-cookie", "url", reqURL, "cookies", names)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cookieSession.DoJSON: read response: %w", err)
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("cookieSession.DoJSON: unmarshal envelope: %w", err)
	}

	if envelope.Code != 1000 {
		slog.Debug("cookieSession.doJSONOnce.error", "method", method, "url", reqURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
		return &Error{
			Status:  resp.StatusCode,
			Code:    envelope.Code,
			Message: envelope.Error,
			Details: envelope.Details,
		}
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("cookieSession.DoJSON: unmarshal result: %w", err)
		}
	}

	return nil
}

// extractRefreshCookie finds the REFRESH-<uid> cookie in the jar for the
// CookieSession's BaseURL. Returns the UID (from the cookie name after the
// "REFRESH-" prefix) and the cookie value (refresh token). Returns an error
// if no REFRESH cookie is found.
func (cs *CookieSession) extractRefreshCookie() (uid, token string, err error) {
	u := cookieQueryURL(cs.BaseURL)

	for _, c := range cs.cookieJar.Cookies(u) {
		if strings.HasPrefix(c.Name, "REFRESH-") {
			uid = strings.TrimPrefix(c.Name, "REFRESH-")
			return uid, c.Value, nil
		}
	}
	return "", "", fmt.Errorf("extractRefreshCookie: no REFRESH cookie in jar")
}

// RefreshCookies calls POST /core/v4/auth/cookies with the REFRESH cookie to
// obtain new AUTH and REFRESH cookies. The cookie jar is updated with the new
// cookies from the response Set-Cookie headers. Concurrent calls are
// serialized via cs.mu — only one refresh HTTP call executes at a time.
//
// CRITICAL: This method uses a raw http.Client, NOT DoJSON. Using DoJSON
// would cause infinite recursion because DoJSON's 401 retry calls
// RefreshCookies.
func (cs *CookieSession) RefreshCookies(ctx context.Context) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	uid, _, err := cs.extractRefreshCookie()
	if err != nil {
		return fmt.Errorf("RefreshCookies: %w", err)
	}

	//nolint:gosec // G706: uid is from our own cookie jar, not user input.
	slog.Debug("cookieSession.RefreshCookies", "uid", uid)

	reqURL := cs.buildURL("/auth/refresh")
	//nolint:gosec // G704: reqURL is built from our own BaseURL, not user input.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("RefreshCookies: new request: %w", err)
	}

	req.Header.Set("x-pm-uid", uid)
	appVer := cs.resolveAppVersion(reqURL)
	if appVer != "" {
		req.Header.Set("x-pm-appversion", appVer)
	}
	if cs.UserAgent != "" {
		req.Header.Set("User-Agent", cs.UserAgent)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", ProtonAccept)
	// No Authorization: Bearer header — cookie auth only.

	httpClient := &http.Client{Jar: cs.cookieJar}
	resp, err := httpClient.Do(req) //nolint:gosec // G704: URL built from our BaseURL
	if err != nil {
		return fmt.Errorf("RefreshCookies: POST auth/cookies: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("RefreshCookies: read response: %w", err)
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("RefreshCookies: unmarshal envelope: %w", err)
	}

	if envelope.Code != 1000 {
		return &Error{
			Status:  resp.StatusCode,
			Code:    envelope.Code,
			Message: envelope.Error,
			Details: envelope.Details,
		}
	}

	// The jar stores the new AUTH and REFRESH cookies from the response's
	// Set-Cookie headers automatically. No manual validation needed — the
	// jar handles path-scoped storage and the http.Client sends the right
	// cookies on subsequent requests.
	slog.Debug("cookieSession.RefreshCookies.complete", "uid", uid) //nolint:gosec // G706: uid from cookie jar

	// Persist refreshed cookies to the store so they survive CLI restarts.
	if cs.Store != nil {
		config, loadErr := cs.Store.Load()
		if loadErr != nil {
			slog.Error("RefreshCookies: load store for persist", "error", loadErr)
		} else {
			config.Cookies = serializeCookies(cs.cookieJar, cookieQueryURL(cs.BaseURL))
			config.LastRefresh = time.Now()
			if saveErr := cs.Store.Save(config); saveErr != nil {
				slog.Error("RefreshCookies: persist cookies", "error", saveErr)
			}
		}
	}

	return nil
}

// DoSSE executes an authenticated POST and returns the raw response body for
// SSE streaming. The caller is responsible for closing the returned
// io.ReadCloser. Sets cookie auth headers (x-pm-uid, x-pm-appversion,
// User-Agent) plus Accept: text/event-stream. No Authorization: Bearer header
// is sent. Returns an *Error on non-2xx HTTP status.
func (cs *CookieSession) DoSSE(ctx context.Context, path string, body any) (io.ReadCloser, error) {
	reqURL := cs.buildURL(path)

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("cookieSession.DoSSE: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("cookieSession.DoSSE: new request: %w", err)
	}

	req.Header.Set("x-pm-uid", cs.UID)
	// No Authorization: Bearer header — cookie auth only.
	appVer := cs.resolveAppVersion(reqURL)
	if appVer != "" {
		req.Header.Set("x-pm-appversion", appVer)
	}
	if cs.UserAgent != "" {
		req.Header.Set("User-Agent", cs.UserAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")

	slog.Debug("cookieSession.DoSSE.request", "url", reqURL, "appversion", appVer)

	httpClient := &http.Client{Jar: cs.cookieJar}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cookieSession.DoSSE: POST %s: %w", path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, &Error{Status: resp.StatusCode}
		}
		var envelope apiEnvelope
		if json.Unmarshal(respBody, &envelope) == nil && envelope.Code != 0 {
			slog.Debug("cookieSession.DoSSE.error", "url", reqURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
			return nil, &Error{
				Status:  resp.StatusCode,
				Code:    envelope.Code,
				Message: envelope.Error,
				Details: envelope.Details,
			}
		}
		return nil, &Error{Status: resp.StatusCode}
	}

	return resp.Body, nil
}

// TransitionToCookies calls POST /core/v4/auth/cookies to transition from
// Bearer auth to cookie auth. After this call, the Bearer tokens in the
// source session are INVALID server-side. Returns a CookieSession with the
// AUTH and REFRESH cookies set in its jar.
func TransitionToCookies(ctx context.Context, session *Session) (*CookieSession, error) {
	req := AuthCookiesReq{
		UID:          session.Auth.UID,
		RefreshToken: session.Auth.RefreshToken,
		GrantType:    "refresh_token",
		ResponseType: "token",
		RedirectURI:  "https://proton.me",
		State:        session.Auth.UID, // use UID as state — unique per session
	}

	slog.Debug("transitionToCookies", "uid", req.UID)

	// POST with Bearer auth via DoJSONCookie — this is the LAST valid Bearer
	// request. The response Set-Cookie headers populate session.cookieJar.
	// The jar stores cookies with the server's path attributes (AUTH uses
	// path=/api/, REFRESH uses path=/api/auth/refresh). We don't manually
	// validate cookie presence — the jar handles path-scoped storage and
	// the http.Client will send the right cookies on subsequent requests.
	if err := session.DoJSONCookie(ctx, "POST", "/core/v4/auth/cookies", req, nil); err != nil {
		return nil, fmt.Errorf("transition to cookies: %w", err)
	}

	// Log what the jar actually holds after the transition. The server
	// sets AUTH with path=/api/ and REFRESH with path=/api/auth/refresh,
	// so we probe both paths to get a complete picture.
	jar := session.CookieJar()
	if base, err := url.Parse(session.BaseURL); err == nil {
		for _, probe := range []string{"/api/", "/api/auth/refresh"} {
			u := *base
			u.Path = probe
			cookies := jar.Cookies(&u)
			names := make([]string, len(cookies))
			for i, c := range cookies {
				names[i] = c.Name
			}
			slog.Debug("transitionToCookies.jar", "uid", req.UID, "probe", probe, "cookies", names)
		}
	}

	slog.Debug("transitionToCookies.complete", "uid", req.UID)

	return &CookieSession{
		UID:        session.Auth.UID,
		BaseURL:    session.BaseURL,
		AppVersion: session.AppVersion,
		UserAgent:  session.UserAgent,
		cookieJar:  session.CookieJar(),
	}, nil
}

// CookieSessionConfig holds the minimal data to restore a CookieSession
// without Resty. Stored separately from the Bearer SessionConfig.
type CookieSessionConfig struct {
	UID         string         `json:"uid"`
	Cookies     []serialCookie `json:"cookies,omitempty"`
	LastRefresh time.Time      `json:"last_refresh,omitempty"`
	Service     string         `json:"service,omitempty"`
}

// Config serializes the CookieSession's cookie jar into a CookieSessionConfig
// suitable for persistence. LastRefresh is set to the current time.
func (cs *CookieSession) Config() *CookieSessionConfig {
	u := cookieQueryURL(cs.BaseURL)

	return &CookieSessionConfig{
		UID:         cs.UID,
		Cookies:     serializeCookies(cs.cookieJar, u),
		LastRefresh: time.Now(),
	}
}

// CookieSessionFromConfig creates a CookieSession from persisted cookies.
// No proton.Manager or Resty client is created — the session uses net/http
// directly with the restored cookie jar. The baseURL parameter determines
// the domain cookies are scoped to in the jar; pass the account service
// host (e.g., "https://account.proton.me/api").
func CookieSessionFromConfig(config *CookieSessionConfig, baseURL string) *CookieSession {
	jar, _ := cookiejar.New(nil)
	loadProtonCookies(jar, config.Cookies, baseURL)

	return &CookieSession{
		UID:       config.UID,
		BaseURL:   baseURL,
		cookieJar: jar,
	}
}

// CookieLoginSave persists both the cookie session and account metadata after
// a login-time cookie transition. The cookieStore receives the serialized
// cookies, and the accountStore receives CookieAuth=true with empty Bearer
// tokens (they are invalid after transition).
func CookieLoginSave(cookieStore, accountStore SessionStore, session *Session, cookieSess *CookieSession, keypass []byte) error {
	cfg := cookieSess.Config()
	saltedKeyPass := Base64Encode(keypass)

	cookieConfig := &SessionConfig{
		UID:           cfg.UID,
		Cookies:       cfg.Cookies,
		SaltedKeyPass: saltedKeyPass,
		LastRefresh:   cfg.LastRefresh,
		CookieAuth:    true,
	}
	if err := cookieStore.Save(cookieConfig); err != nil {
		return fmt.Errorf("cookie login save: cookie store: %w", err)
	}

	accountConfig := &SessionConfig{
		UID:           session.Auth.UID,
		AccessToken:   "",
		RefreshToken:  "",
		SaltedKeyPass: saltedKeyPass,
		LastRefresh:   cfg.LastRefresh,
		CookieAuth:    true,
	}
	if err := accountStore.Save(accountConfig); err != nil {
		return fmt.Errorf("cookie login save: account store: %w", err)
	}

	return nil
}

// CookieSessionRestore restores a cookie-mode session from the cookie store.
// It loads persisted cookies, optionally performs a proactive refresh, builds
// a proton.Manager with CookieTransport, and unlocks keyrings. The returned
// Session uses cookie auth for all Resty-based API calls (GetUser,
// GetAddresses, etc.). Session.Auth holds the UID but empty Bearer tokens.
func CookieSessionRestore(ctx context.Context, options []proton.Option, cookieStore SessionStore, acctConfig *SessionConfig, managerHook func(*proton.Manager)) (*Session, error) {
	cookieConfig, err := cookieStore.Load()
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			return nil, ErrNotLoggedIn
		}
		return nil, fmt.Errorf("cookie session restore: load: %w", err)
	}

	acctSvc, _ := LookupService("account")

	// Build cookie jar and inject persisted cookies.
	jar, _ := cookiejar.New(nil)
	loadProtonCookies(jar, cookieConfig.Cookies, acctSvc.Host)

	// Proactive refresh: if cookies are stale, refresh before building the session.
	if NeedsCookieRefresh(cookieConfig.LastRefresh) {
		slog.Debug("cookieSessionRestore.proactiveRefresh", "age", time.Since(cookieConfig.LastRefresh))

		cs := &CookieSession{
			UID:        cookieConfig.UID,
			BaseURL:    acctSvc.Host,
			AppVersion: acctSvc.AppVersion(""),
			cookieJar:  jar,
		}
		if refreshErr := cs.RefreshCookies(ctx); refreshErr != nil {
			return nil, fmt.Errorf("cookie session restore: proactive refresh (run `proton account login`): %w", refreshErr)
		}

		// Persist updated cookies.
		refreshedCfg := cs.Config()
		cookieConfig.Cookies = refreshedCfg.Cookies
		cookieConfig.LastRefresh = refreshedCfg.LastRefresh
		if saveErr := cookieStore.Save(cookieConfig); saveErr != nil {
			slog.Error("cookie session restore: persist refreshed cookies", "error", saveErr)
		}
	}

	// Build proton.Manager with CookieTransport so Resty-based calls use cookie auth.
	ct := &CookieTransport{Base: http.DefaultTransport}
	managerOpts := []proton.Option{
		proton.WithTransport(ct),
		proton.WithCookieJar(jar),
		proton.WithHostURL(acctSvc.Host),
		proton.WithAppVersion(acctSvc.AppVersion("")),
	}
	managerOpts = append(managerOpts, options...)

	session := &Session{}
	session.Throttle = NewThrottle(DefaultThrottleBackoff, DefaultThrottleMaxDelay)
	session.Pool = pool.New(ctx, DefaultMaxWorkers(), pool.WithThrottle(session.Throttle))
	session.cookieJar = jar

	session.manager = proton.New(managerOpts...)
	if managerHook != nil {
		managerHook(session.manager)
	}

	// Attach cookie refresh handler to the transport for 401 retry.
	attachCookieRefresh(ctx, cookieConfig, jar, ct, cookieStore)

	// Create client with UID for x-pm-uid header, empty tokens (Bearer is dead).
	session.Client = session.manager.NewClient(cookieConfig.UID, "", "")
	session.Auth = proton.Auth{
		UID:          cookieConfig.UID,
		AccessToken:  "",
		RefreshToken: "",
	}

	// Fetch user and addresses via the cookie-authed client.
	user, err := session.Client.GetUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("cookie session restore: get user: %w", err)
	}
	session.user = user

	addrs, err := session.Client.GetAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("cookie session restore: get addresses: %w", err)
	}

	// Unlock keyrings using SaltedKeyPass from the account config.
	keypass, err := Base64Decode(acctConfig.SaltedKeyPass)
	if err != nil {
		return nil, fmt.Errorf("cookie session restore: decode keypass: %w", err)
	}
	if err := session.Unlock(keypass, addrs); err != nil {
		return nil, fmt.Errorf("cookie session restore: unlock: %w", err)
	}

	// Set BaseURL and AppVersion from the account service registry.
	session.BaseURL = acctSvc.Host
	session.AppVersion = acctSvc.AppVersion("")

	return session, nil
}
