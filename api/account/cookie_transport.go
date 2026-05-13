package account

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/major0/proton-utils/api"
)

// CookieTransport is an http.RoundTripper that converts Bearer auth to
// cookie auth. It strips the Authorization header added by Resty and relies
// on the cookie jar (set on the http.Client by Resty) to send AUTH-<uid>
// cookies instead.
//
// When a CookieSession is attached (via SetCookieSession), the transport
// intercepts 401 responses, triggers a cookie refresh, and retries the
// request once. This handles auth expiry transparently without relying on
// go-proton-api's Bearer-based authRefresh (which fails for cookie sessions).
//
// Usage:
//
//	ct := &CookieTransport{Base: http.DefaultTransport}
//	manager := proton.New(
//	    proton.WithTransport(ct),
//	    proton.WithCookieJar(jar),  // jar has AUTH-<uid> cookie
//	    ...
//	)
type CookieTransport struct {
	// Base is the underlying transport. If nil, http.DefaultTransport is used.
	Base http.RoundTripper

	// cookieSess is the CookieSession used for 401 refresh. When set,
	// 401 responses trigger RefreshCookies and a single retry.
	cookieSess *CookieSession

	// cookieStore persists updated cookies after a successful refresh.
	cookieStore api.SessionStore

	// mu serializes 401 refresh attempts.
	mu sync.Mutex
}

// SetCookieSession attaches a CookieSession and store for 401 refresh.
// When set, 401 responses trigger RefreshCookies and a retry.
func (ct *CookieTransport) SetCookieSession(cs *CookieSession, store api.SessionStore) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.cookieSess = cs
	ct.cookieStore = store
}

// RoundTrip strips the Authorization header and delegates to the base
// transport. The cookie jar on the http.Client sends AUTH-<uid> cookies
// automatically. If a CookieSession is attached and the response is 401,
// triggers a cookie refresh and retries once.
func (ct *CookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Strip Bearer auth — cookie auth only after transition.
	if auth := req.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			slog.Debug("cookieTransport: stripping Bearer header")
			req = req.Clone(req.Context())
			req.Header.Del("Authorization")
		}
	}

	// Log outgoing cookie names for debugging (never values).
	if cookieHeader := req.Header.Get("Cookie"); cookieHeader != "" {
		var names []string
		for _, part := range strings.Split(cookieHeader, ";") {
			if name, _, ok := strings.Cut(strings.TrimSpace(part), "="); ok {
				names = append(names, name)
			}
		}
		slog.Debug("cookieTransport: outgoing cookies", "url", req.URL.String(), "cookies", names) //nolint:gosec // G706: URL from request
	} else {
		slog.Debug("cookieTransport: no cookies on request", "url", req.URL.String()) //nolint:gosec // G706: URL from request
	}

	base := ct.Base
	if base == nil {
		base = http.DefaultTransport
	}

	resp, err := base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// 401 retry: refresh cookies and retry once.
	if resp.StatusCode == http.StatusUnauthorized && ct.cookieSess != nil {
		ct.mu.Lock()
		cs := ct.cookieSess
		store := ct.cookieStore
		ct.mu.Unlock()

		if cs != nil {
			slog.Debug("cookieTransport: 401 → refreshing cookies", "url", req.URL.String()) //nolint:gosec // G706: URL from request
			if refreshErr := cs.RefreshCookies(req.Context()); refreshErr != nil {
				slog.Error("cookieTransport: cookie refresh failed", "error", refreshErr)
				return resp, nil // return original 401
			}

			// Persist updated cookies.
			if store != nil {
				persistCookieRefresh(cs, store)
			}

			// Close the original response body before retry.
			_ = resp.Body.Close()

			// Retry the request. Clone to get a fresh body.
			retryReq := req.Clone(req.Context())
			// Re-strip Bearer in case it was re-added.
			retryReq.Header.Del("Authorization")

			return base.RoundTrip(retryReq)
		}
	}

	return resp, err
}

// persistCookieRefresh saves updated cookies to the store after a refresh.
func persistCookieRefresh(cs *CookieSession, store api.SessionStore) {
	cfg, err := store.Load()
	if err != nil {
		slog.Error("cookieTransport: load store for persist", "error", err)
		return
	}
	u := cookieQueryURL(cs.BaseURL)
	cfg.Cookies = api.SerializeCookies(cs.cookieJar, u)
	cfg.LastRefresh = time.Now()
	if err := store.Save(cfg); err != nil {
		slog.Error("cookieTransport: persist refreshed cookies", "error", err)
	}
}

// NewCookieAuthHandler creates a CookieSession for 401 refresh and wires it
// into the CookieTransport. Returns the CookieSession so callers can use it
// for other cookie operations. The cookieStore is used to persist updated
// cookies after refresh.
func NewCookieAuthHandler(cookieConfig *api.SessionCredentials, baseURL string, transport *CookieTransport, cookieStore api.SessionStore) *CookieSession {
	cs := CookieSessionFromConfig(&CookieSessionConfig{
		UID:         cookieConfig.UID,
		Cookies:     cookieConfig.Cookies,
		LastRefresh: cookieConfig.LastRefresh,
	}, baseURL)

	acctSvc, _ := api.LookupService("account")
	cs.AppVersion = acctSvc.AppVersion("")

	transport.SetCookieSession(cs, cookieStore)
	return cs
}

// attachCookieRefresh creates a CookieSession from a SessionCredentials and
// attaches it to the given CookieTransport for 401 refresh handling. This is
// a convenience function for CookieSessionRestore.
func attachCookieRefresh(ctx context.Context, cookieConfig *api.SessionCredentials, jar http.CookieJar, transport *CookieTransport, cookieStore api.SessionStore) {
	_ = ctx // reserved for future use
	cs := &CookieSession{
		UID:       cookieConfig.UID,
		Store:     cookieStore,
		cookieJar: jar,
	}
	acctSvc, _ := api.LookupService("account")
	cs.BaseURL = acctSvc.Host
	cs.AppVersion = acctSvc.AppVersion("")

	transport.SetCookieSession(cs, cookieStore)
}
