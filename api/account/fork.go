package account

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

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// ForkPushReq is the request body for POST /auth/v4/sessions/forks.
type ForkPushReq struct {
	ChildClientID string `json:"ChildClientID"`
	Independent   int    `json:"Independent"`
	Payload       string `json:"Payload,omitempty"`
}

// ForkPushResp is the response from POST /auth/v4/sessions/forks.
type ForkPushResp struct {
	Code     int    `json:"Code"`
	Selector string `json:"Selector"`
}

// ForkPullResp is the response from GET /auth/v4/sessions/forks/<selector>.
type ForkPullResp struct {
	Code         int      `json:"Code"`
	UID          string   `json:"UID"`
	AccessToken  string   `json:"AccessToken"`
	RefreshToken string   `json:"RefreshToken"`
	Payload      string   `json:"Payload,omitempty"`
	Scopes       []string `json:"Scopes,omitempty"`
}

// ForkSessionWithKeyPass creates a child session, encrypting the given
// SaltedKeyPass in the fork blob instead of using the parent's UID.
func ForkSessionWithKeyPass(ctx context.Context, parent *api.Session, targetService api.ServiceConfig, version string, keyPass []byte) (*api.Session, []byte, error) {
	blob := &ForkBlob{
		Type:        "default",
		KeyPassword: string(keyPass),
	}

	ciphertext, blobKey, err := EncryptForkBlob(blob)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: encrypt blob: %w", ErrForkFailed, err)
	}

	pushReq := ForkPushReq{
		ChildClientID: targetService.ClientID,
		Independent:   0,
		Payload:       ciphertext,
	}
	var pushResp ForkPushResp

	// Log AUTH-* cookie presence for debugging.
	hasAuthCookie := false
	if pushURL, err := url.Parse(parent.BaseURL); err == nil {
		for _, c := range parent.CookieJar().Cookies(pushURL) {
			if strings.HasPrefix(c.Name, "AUTH-") {
				hasAuthCookie = true
				break
			}
		}
	}
	slog.Debug("cookieFork.push.jar-cookies", "url", parent.BaseURL+"/auth/v4/sessions/forks", "hasAuth", hasAuthCookie)

	if err := parent.DoJSONCookie(ctx, "POST", "/auth/v4/sessions/forks", pushReq, &pushResp); err != nil {
		return nil, nil, fmt.Errorf("%w: push: %w", ErrForkFailed, err)
	}

	slog.Debug("fork.push", "selector", pushResp.Selector[:min(8, len(pushResp.Selector))]+"…", "service", targetService.Name, "child_client_id", targetService.ClientID, "push_host", parent.BaseURL)

	// Pull from the target service host.
	pullResp, err := forkPull(ctx, parent, targetService.Host, pushResp.Selector, targetService.AppVersion(""))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: pull: %w", ErrForkFailed, err)
	}

	slog.Debug("fork.pull", "uid", pullResp.UID, "service", targetService.Name, "scopes", pullResp.Scopes)

	decryptedBlob, err := DecryptForkBlob(pullResp.Payload, blobKey)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: decrypt blob: %w", ErrForkFailed, err)
	}

	child := SessionFromForkPull(ctx, pullResp, targetService, version)

	return child, []byte(decryptedBlob.KeyPassword), nil
}

// forkPull executes GET /auth/v4/sessions/forks/<selector> on the target
// service host. The pull is unauthenticated (no Bearer token) — the
// selector in the URL path is the credential. Session cookies from the
// parent's jar are propagated to the target host for correlation.
func forkPull(ctx context.Context, parent *api.Session, host, selector, appVersion string) (*ForkPullResp, error) {
	pullURL := host + "/auth/v4/sessions/forks/" + selector

	slog.Debug("fork.pull.request", "url", pullURL, "appversion", appVersion)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	if appVersion != "" {
		req.Header.Set("x-pm-appversion", appVersion)
	}
	if parent.UserAgent != "" {
		req.Header.Set("User-Agent", parent.UserAgent)
	}
	req.Header.Set("Accept", api.ProtonAccept)

	// Propagate session cookies from the parent's jar to the pull jar.
	pullJar, _ := cookiejar.New(nil)
	var protonHosts []*url.URL
	for _, svc := range api.Services {
		if u, err := url.Parse(svc.Host); err == nil {
			protonHosts = append(protonHosts, &url.URL{Scheme: u.Scheme, Host: u.Host})
		}
	}
	targetURL, _ := url.Parse(host)
	for _, srcURL := range protonHosts {
		for _, c := range parent.CookieJar().Cookies(srcURL) {
			// Skip auth cookies — only session/metadata cookies.
			if strings.HasPrefix(c.Name, "AUTH-") || strings.HasPrefix(c.Name, "REFRESH-") {
				continue
			}
			pullJar.SetCookies(targetURL, []*http.Cookie{c})
		}
	}

	pullCookies := pullJar.Cookies(targetURL)
	cookieNames := make([]string, len(pullCookies))
	for i, c := range pullCookies {
		cookieNames[i] = c.Name
	}
	slog.Debug("fork.pull.cookies", "host", host, "cookies", cookieNames)

	httpClient := &http.Client{Jar: pullJar}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", pullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, api.MaxJSONResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}

	if envelope.Code != 1000 {
		slog.Debug("fork.pull.error", "url", pullURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
		return nil, &api.Error{
			Status:  resp.StatusCode,
			Code:    envelope.Code,
			Message: envelope.Error,
			Details: envelope.Details,
		}
	}

	var pullResp ForkPullResp
	if err := json.Unmarshal(body, &pullResp); err != nil {
		return nil, fmt.Errorf("unmarshal pull response: %w", err)
	}

	return &pullResp, nil
}

// SessionFromForkPull constructs a Session from a ForkPullResp and
// ServiceConfig. The version string is passed through for backward
// compatibility but the service's own app version is used for all requests.
func SessionFromForkPull(ctx context.Context, pull *ForkPullResp, svc api.ServiceConfig, _ string) *api.Session {
	jar, _ := cookiejar.New(nil)
	appVersion := svc.AppVersion("")

	managerOpts := []proton.Option{
		proton.WithHostURL(svc.Host),
		proton.WithAppVersion(appVersion),
		proton.WithCookieJar(jar),
	}

	session := api.InitSessionWithJar(ctx, jar, managerOpts, nil)
	session.Client = session.Manager().NewClient(pull.UID, pull.AccessToken, pull.RefreshToken)
	session.Auth = proton.Auth{
		UID:          pull.UID,
		AccessToken:  pull.AccessToken,
		RefreshToken: pull.RefreshToken,
	}
	session.BaseURL = svc.Host
	session.AppVersion = appVersion

	return session
}

// CookieSessionFromForkPull constructs a Session that uses cookie auth
// instead of Bearer auth. The provided cookie jar must contain the AUTH-<uid>
// cookie. CookieTransport strips the Bearer header that Resty adds, so the
// server only sees cookie auth.
func CookieSessionFromForkPull(ctx context.Context, pull *ForkPullResp, svc api.ServiceConfig, cookieJar http.CookieJar) *api.Session {
	appVersion := svc.AppVersion("")

	managerOpts := []proton.Option{
		proton.WithHostURL(svc.Host),
		proton.WithAppVersion(appVersion),
		proton.WithTransport(&CookieTransport{Base: http.DefaultTransport}),
		proton.WithCookieJar(cookieJar),
	}

	session := api.InitSessionWithJar(ctx, cookieJar, managerOpts, nil)
	session.Client = session.Manager().NewClient(pull.UID, pull.AccessToken, pull.RefreshToken)
	session.Auth = proton.Auth{
		UID:          pull.UID,
		AccessToken:  pull.AccessToken,
		RefreshToken: pull.RefreshToken,
	}
	session.BaseURL = svc.Host
	session.AppVersion = appVersion

	return session
}

// CookieFork performs a cookie-aware fork for CookieAuth services.
//
// The flow:
//  1. Load or create a CookieSession from cookieStore.
//  2. If no valid cookie session exists, fork a TEMPORARY session from
//     account (Bearer), transition it to cookies, and save.
//  3. Use CookieSession.DoJSON for the fork push (AUTH cookie → full scopes).
//  4. Fork pull is unchanged (unauthenticated, Session-Id only).
//  5. Build child Session from fork pull response.
//
// CRITICAL: The account Bearer session is never passed to TransitionToCookies.
// A temporary forked session is transitioned instead, preserving the account
// session for Drive operations.
func CookieFork(ctx context.Context, acctSession *api.Session, acctConfig *api.SessionCredentials, targetService api.ServiceConfig, _ string, keyPass []byte, cookieStore api.SessionStore) (*api.Session, []byte, error) {
	acctSvc, _ := api.LookupService("account")

	// Try to load an existing cookie session.
	cookieSess, err := loadOrCreateCookieSession(ctx, acctSession, acctConfig, acctSvc, cookieStore)
	if err != nil {
		return nil, nil, fmt.Errorf("cookie fork: %w", err)
	}

	// Encrypt keypass into a fork blob.
	blob := &ForkBlob{
		Type:        "default",
		KeyPassword: string(keyPass),
	}
	ciphertext, blobKey, err := EncryptForkBlob(blob)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: encrypt blob: %w", ErrForkFailed, err)
	}

	// Fork push via CookieSession's cookie jar.
	pushReq := ForkPushReq{
		ChildClientID: targetService.ClientID,
		Independent:   0,
		Payload:       ciphertext,
	}

	// The push goes to the account host.
	pushURL := acctSvc.Host + "/auth/v4/sessions/forks"
	slog.Debug("cookieFork.push", "url", pushURL, "service", targetService.Name, "child_client_id", targetService.ClientID)

	pushData, err := json.Marshal(pushReq)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: marshal push: %w", ErrForkFailed, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, pushURL, bytes.NewReader(pushData))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: new push request: %w", ErrForkFailed, err)
	}

	// Match the browser's headers exactly (from HAR analysis).
	httpReq.Header.Set("accept", "application/vnd.protonmail.v1+json")
	httpReq.Header.Set("accept-language", "en-US,en;q=0.9")
	httpReq.Header.Set("cache-control", "no-cache")
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("origin", "https://account.proton.me")
	httpReq.Header.Set("pragma", "no-cache")
	httpReq.Header.Set("referer", "https://account.proton.me/authorize?app=proton-"+targetService.Name)
	httpReq.Header.Set("user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36")
	httpReq.Header.Set("x-pm-appversion", acctSvc.AppVersion(""))
	httpReq.Header.Set("x-pm-locale", "en_US")
	httpReq.Header.Set("x-pm-uid", cookieSess.UID)

	// Log what cookies the jar will send for this URL.
	if pushParsed, parseErr := url.Parse(pushURL); parseErr == nil {
		jarCookies := cookieSess.cookieJar.Cookies(pushParsed)
		names := make([]string, len(jarCookies))
		for i, c := range jarCookies {
			names[i] = c.Name
		}
		slog.Debug("cookieFork.push.jar-cookies", "url", pushURL, "cookies", names)
	}

	httpClient := &http.Client{Jar: cookieSess.cookieJar}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: cookie push: %w", ErrForkFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, api.MaxJSONResponseSize))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: read push response: %w", ErrForkFailed, err)
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, nil, fmt.Errorf("%w: unmarshal push envelope: %w", ErrForkFailed, err)
	}
	if envelope.Code != 1000 {
		return nil, nil, &api.Error{Status: resp.StatusCode, Code: envelope.Code, Message: envelope.Error, Details: envelope.Details}
	}

	var pushResp ForkPushResp
	if err := json.Unmarshal(respBody, &pushResp); err != nil {
		return nil, nil, fmt.Errorf("%w: unmarshal push response: %w", ErrForkFailed, err)
	}

	slog.Debug("cookieFork.push.done", "selector", pushResp.Selector[:min(8, len(pushResp.Selector))]+"…", "service", targetService.Name)

	// Fork pull from the target service host (unauthenticated).
	pullResp, err := forkPull(ctx, acctSession, targetService.Host, pushResp.Selector, targetService.AppVersion(""))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: pull: %w", ErrForkFailed, err)
	}

	slog.Debug("cookieFork.pull", "uid", pullResp.UID, "service", targetService.Name, "scopes", pullResp.Scopes)

	// Decrypt the fork blob.
	decryptedBlob, err := DecryptForkBlob(pullResp.Payload, blobKey)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: decrypt blob: %w", ErrForkFailed, err)
	}

	// Transition the child to cookies.
	childBearer := SessionFromForkPull(ctx, pullResp, targetService, "")
	childBearer.BaseURL = targetService.Host
	childBearer.AppVersion = targetService.AppVersion("")

	slog.Debug("cookieFork.child.transition", "uid", pullResp.UID, "host", targetService.Host)

	childCookieSess, err := TransitionToCookies(ctx, childBearer)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: child cookie transition: %w", ErrForkFailed, err)
	}

	// Build the final child session with CookieTransport.
	child := CookieSessionFromForkPull(ctx, pullResp, targetService, childCookieSess.cookieJar)

	// Clear Bearer tokens — after cookie transition, auth is provided
	// exclusively via cookies.
	child.Auth.AccessToken = ""
	child.Auth.RefreshToken = ""

	return child, []byte(decryptedBlob.KeyPassword), nil
}

// loadOrCreateCookieSession loads a CookieSession from the cookie store,
// or creates one by forking a temporary session from the account and
// transitioning it to cookies.
func loadOrCreateCookieSession(ctx context.Context, acctSession *api.Session, acctConfig *api.SessionCredentials, acctSvc api.ServiceConfig, cookieStore api.SessionStore) (*CookieSession, error) {
	cookieConfig, loadErr := cookieStore.Load()

	needsCreate := false
	switch {
	case loadErr != nil:
		if !errors.Is(loadErr, api.ErrKeyNotFound) {
			return nil, fmt.Errorf("load cookie session: %w", loadErr)
		}
		slog.Debug("cookie session not found, will create", "uid", acctConfig.UID)
		needsCreate = true
	case cookieConfig.UID == "":
		slog.Debug("cookie session has no UID, will create")
		needsCreate = true
	case IsStale(acctConfig.LastRefresh, cookieConfig.LastRefresh):
		slog.Debug("cookie session is stale, will re-create",
			"acct_refresh", acctConfig.LastRefresh,
			"cookie_refresh", cookieConfig.LastRefresh)
		needsCreate = true
	default:
		slog.Debug("cookie session is fresh", "uid", cookieConfig.UID,
			"cookie_refresh", cookieConfig.LastRefresh,
			"acct_refresh", acctConfig.LastRefresh)
	}

	if !needsCreate {
		// Restore CookieSession from persisted config.
		csc := &CookieSessionConfig{
			UID:         cookieConfig.UID,
			Cookies:     cookieConfig.Cookies,
			LastRefresh: cookieConfig.LastRefresh,
		}
		cs := CookieSessionFromConfig(csc, acctSvc.Host)
		cs.AppVersion = acctSvc.AppVersion("")
		return cs, nil
	}

	// Create a new cookie session by transitioning the account session
	// directly to cookies.
	slog.Debug("cookie session: transitioning account session to cookies", "uid", acctSession.Auth.UID)

	cookieSess, err := TransitionToCookies(ctx, acctSession)
	if err != nil {
		return nil, fmt.Errorf("cookie session: transition: %w", err)
	}

	// Save the cookie session to the store.
	csc := cookieSess.Config()
	csc.Service = "cookie"
	saveCfg := &api.SessionCredentials{
		UID:         csc.UID,
		Cookies:     csc.Cookies,
		LastRefresh: csc.LastRefresh,
		Service:     csc.Service,
	}
	if err := cookieStore.Save(saveCfg); err != nil {
		return nil, fmt.Errorf("cookie session: save: %w", err)
	}

	slog.Debug("cookie session: created and saved", "uid", csc.UID)

	return cookieSess, nil
}
