package api

import (
	"bytes"
	"context"
	"encoding/json"
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
)

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

// CookieURL returns the parsed Proton API base URL used for cookie scoping.
func CookieURL() *url.URL {
	u, _ := url.Parse(proton.DefaultHostURL)
	return u
}

// SerializeCookies extracts cookies from the jar for the given API URL.
func SerializeCookies(jar http.CookieJar, apiURL *url.URL) []SerialCookie {
	cookies := jar.Cookies(apiURL)
	if len(cookies) == 0 {
		return nil
	}
	out := make([]SerialCookie, len(cookies))
	for i, c := range cookies {
		out[i] = SerialCookie{
			Name:   c.Name,
			Value:  c.Value,
			Domain: c.Domain,
			Path:   c.Path,
		}
	}
	return out
}

// LoadCookies injects persisted cookies into the jar for the given API URL.
func LoadCookies(jar http.CookieJar, cookies []SerialCookie, apiURL *url.URL) {
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
	BaseURL    string         // override for DoJSON; defaults to proton.DefaultHostURL
	AppVersion string         // x-pm-appversion header value for DoJSON requests
	UserAgent  string         // User-Agent header value for DoJSON requests
	Config     *SessionConfig // resolved application config; set by consumer via api/config/
	manager    *proton.Manager

	cookieJar  http.CookieJar
	httpClient *http.Client // reused across doRequest/DoSSE calls; see initHTTPClient
	authMu     sync.Mutex   // serializes auth handler updates

	// cachedAuthInfo holds the AuthInfo from the initial login attempt.
	// It is reused on HV retry so the SRP session matches the solved CAPTCHA.
	cachedAuthInfo *proton.AuthInfo

	Sem      *Semaphore
	Throttle *Throttle

	addresses       map[string]proton.Address
	addressKeyRings map[string]*crypto.KeyRing

	user        proton.User
	UserKeyRing *crypto.KeyRing
}

// initHTTPClient returns the session's shared http.Client, creating it
// on first call. The client uses the session's cookie jar and no explicit
// Transport — it inherits http.DefaultTransport, which is shared with
// Resty (via go-proton-api). This is critical: Go's h2 transport
// multiplexes all requests to the same host over a single TCP connection.
// Creating a separate http.Transport would fork the connection pool and
// break HTTP/2 stream multiplexing between our DoJSON calls and Resty's
// block upload/download calls.
func (s *Session) initHTTPClient() *http.Client {
	if s.httpClient == nil {
		s.httpClient = &http.Client{Jar: s.cookieJar}
	}
	return s.httpClient
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

// Addresses returns the session's addresses. If addresses were already
// loaded during Unlock, returns the cached copy. Otherwise fetches from
// the API. This avoids a redundant API call in drive.NewClient when the
// session was restored with cached addresses.
func (s *Session) Addresses(ctx context.Context) ([]proton.Address, error) {
	if len(s.addresses) > 0 {
		addrs := make([]proton.Address, 0, len(s.addresses))
		for _, addr := range s.addresses {
			addrs = append(addrs, addr)
		}
		return addrs, nil
	}
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

// Manager returns the underlying proton.Manager. Used by api/account/
// for session operations that need direct manager access (login, fork).
func (s *Session) Manager() *proton.Manager { return s.manager }

// SetManager sets the underlying proton.Manager.
func (s *Session) SetManager(m *proton.Manager) { s.manager = m }

// CachedAuthInfo returns the cached AuthInfo from the initial login attempt.
func (s *Session) CachedAuthInfo() *proton.AuthInfo { return s.cachedAuthInfo }

// SetCachedAuthInfo stores the AuthInfo for HV retry reuse.
func (s *Session) SetCachedAuthInfo(info *proton.AuthInfo) { s.cachedAuthInfo = info }

// AuthMu returns a reference to the session's auth mutex for serializing
// auth handler updates. Used by api/account/ auth handlers.
func (s *Session) AuthMu() *sync.Mutex { return &s.authMu }

// InitSession initializes a new empty Session with Throttle, Semaphore,
// cookie jar, and proton.Manager. This is the shared setup used by
// SessionFromCredentials and sessionFromLogin. Returns the Session and
// the Manager for callers that need direct manager access.
func InitSession(ctx context.Context, options []proton.Option, managerHook func(*proton.Manager)) (*Session, *proton.Manager) {
	session := &Session{}
	session.Throttle = NewThrottle(DefaultThrottleBackoff, DefaultThrottleMaxDelay)
	session.Sem = NewSemaphore(ctx, DefaultMaxWorkers(), session.Throttle)

	jar, _ := cookiejar.New(nil)
	session.cookieJar = jar

	session.manager = proton.New(append(options, proton.WithCookieJar(jar))...)

	if managerHook != nil {
		managerHook(session.manager)
	}

	return session, session.manager
}

// InitSessionWithJar initializes a new empty Session with a pre-built
// cookie jar and manager options. Used by restoreExistingCookieService
// where the jar and manager options are constructed externally.
func InitSessionWithJar(ctx context.Context, jar http.CookieJar, managerOpts []proton.Option, managerHook func(*proton.Manager)) *Session {
	session := &Session{}
	session.Throttle = NewThrottle(DefaultThrottleBackoff, DefaultThrottleMaxDelay)
	session.Sem = NewSemaphore(ctx, DefaultMaxWorkers(), session.Throttle)
	session.cookieJar = jar

	session.manager = proton.New(managerOpts...)
	if managerHook != nil {
		managerHook(session.manager)
	}

	return session
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
	return s.doRequest(ctx, method, path, body, result, "doJSON")
}

// DoJSONCookie executes an authenticated JSON API request using cookie-based
// auth. Instead of the Authorization: Bearer header, auth is provided via the
// AUTH-<uid>=<token> cookie in the session's cookie jar. The x-pm-uid header
// is still sent. The x-pm-appversion is resolved from the target URL's host.
func (s *Session) DoJSONCookie(ctx context.Context, method, path string, body, result any) error {
	return s.doRequest(ctx, method, path, body, result, "doJSONCookie")
}

// doRequest is the shared implementation for DoJSON and DoJSONCookie.
// The label parameter is used in error messages and log prefixes to
// distinguish callers in debug output.
func (s *Session) doRequest(ctx context.Context, method, path string, body, result any, label string) error {
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
			return fmt.Errorf("%s: marshal body: %w", label, err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("%s: new request: %w", label, err)
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

	slog.Debug(label+".request", "method", method, "url", reqURL, "appversion", appVer)

	// Log outgoing cookie names for debugging (never values).
	if s.cookieJar != nil {
		if reqParsed, parseErr := url.Parse(reqURL); parseErr == nil {
			outCookies := s.cookieJar.Cookies(reqParsed)
			names := make([]string, len(outCookies))
			for i, c := range outCookies {
				names[i] = c.Name
			}
			slog.Debug(label+".sending-cookies", "url", reqURL, "cookies", names)
		}
	}

	httpClient := s.initHTTPClient()
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %s %s: %w", label, method, path, err)
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
		slog.Debug(label+".set-cookie", "url", reqURL, "cookies", names)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response: %w", label, err)
	}

	// Parse the envelope to check the API-level error code.
	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("%s: unmarshal envelope: %w", label, err)
	}

	if envelope.Code != 1000 && envelope.Code != 1001 {
		slog.Debug(label+".error", "method", method, "url", reqURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
		return &Error{
			Status:  resp.StatusCode,
			Code:    envelope.Code,
			Message: envelope.Error,
			Details: envelope.Details,
		}
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("%s: unmarshal result: %w", label, err)
		}
	}

	return nil
}
