package account

import (
	"log/slog"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
)

// NewAuthHandler returns a proton.AuthHandler that persists updated tokens
// and cookies to the session store. Uses the session's own cookie jar.
func NewAuthHandler(store api.SessionStore, session *api.Session) proton.AuthHandler {
	return func(auth proton.Auth) {
		session.AuthMu().Lock()
		defer session.AuthMu().Unlock()

		// Update in-memory tokens first.
		session.Auth = auth

		slog.Debug("auth", "uid", auth.UID,
			"access_token", "<redacted>",
			"refresh_token", "<redacted>")

		config, err := store.Load()
		if err != nil {
			slog.Error("auth handler: loading session config", "error", err)
			return
		}

		config.UID = auth.UID
		config.AccessToken = auth.AccessToken
		config.RefreshToken = auth.RefreshToken
		config.Cookies = api.SerializeCookies(session.CookieJar(), cookieQueryURL(session.BaseURL))
		config.LastRefresh = time.Now()

		if err := store.Save(config); err != nil {
			slog.Error("auth handler: saving session config", "error", err)
		}
	}
}

// deauthHandler logs a deauth event. Matches the current behavior from
// cmd/auth.go — no recovery action is taken.
func deauthHandler() {
	slog.Debug("deauth")
}

// NewDeauthHandler returns a proton.Handler that logs deauth events.
func NewDeauthHandler() proton.Handler {
	return deauthHandler
}
