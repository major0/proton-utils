package api

import "time"

// SessionCredentials holds the minimum data to restore and unlock a session:
// credentials (UID, tokens) plus the SaltedKeyPass for Unlock.
type SessionCredentials struct {
	UID           string         `json:"uid"`
	AccessToken   string         `json:"access_token"`
	RefreshToken  string         `json:"refresh_token"`
	SaltedKeyPass string         `json:"salted_key_pass"`
	Cookies       []SerialCookie `json:"cookies,omitempty"`
	LastRefresh   time.Time      `json:"last_refresh,omitempty"`
	Service       string         `json:"service,omitempty"`
	CookieAuth    bool           `json:"cookie_auth,omitempty"`
}
