package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"

	"github.com/major0/proton-utils/api"
)

// AnonSessionResp is the response from POST /auth/v4/sessions.
type AnonSessionResp struct {
	Code         int    `json:"Code"`
	UID          string `json:"UID"`
	AccessToken  string `json:"AccessToken"`
	RefreshToken string `json:"RefreshToken"`
}

// CreateAnonSession creates an anonymous session on the account host.
// This is the first step in the browser's login flow — it creates a
// session with no credentials, returning UID + tokens that can be used
// for subsequent SRP login. The session is created on account.proton.me
// matching the browser's flow exactly.
func CreateAnonSession(ctx context.Context) (*AnonSessionResp, http.CookieJar, error) {
	acctSvc, _ := api.LookupService("account")
	sessionURL := acctSvc.Host + "/auth/v4/sessions"

	slog.Debug("createAnonSession", "url", sessionURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionURL, bytes.NewReader(nil))
	if err != nil {
		return nil, nil, fmt.Errorf("anon session: new request: %w", err)
	}

	// Match the browser's headers exactly.
	req.Header.Set("accept", api.ProtonAccept)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-enforce-unauthsession", "true")
	req.Header.Set("x-pm-appversion", acctSvc.AppVersion(""))
	req.Header.Set("x-pm-locale", "en_US")
	req.Header.Set("origin", "https://account.proton.me")
	req.Header.Set("referer", "https://account.proton.me/")
	req.ContentLength = 0

	jar, _ := cookiejar.New(nil)

	client := &http.Client{Jar: jar}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("anon session: POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, api.MaxJSONResponseSize))
	if err != nil {
		return nil, nil, fmt.Errorf("anon session: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var envelope api.Envelope
		if json.Unmarshal(body, &envelope) == nil && envelope.Code != 0 {
			return nil, nil, &api.Error{Status: resp.StatusCode, Code: envelope.Code, Message: envelope.Error, Details: envelope.Details}
		}
		return nil, nil, fmt.Errorf("anon session: HTTP %d", resp.StatusCode)
	}

	var result AnonSessionResp
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, nil, fmt.Errorf("anon session: unmarshal: %w", err)
	}

	slog.Debug("createAnonSession.done", "uid", result.UID)

	return &result, jar, nil
}
