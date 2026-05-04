package account

import (
	"fmt"
	"net/http/cookiejar"
	"sync"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"pgregory.net/rapid"
)

// TestAuthHandlerConcurrency verifies no data race when multiple goroutines
// invoke the auth handler simultaneously. Run with: go test -race ./api/account/...
func TestAuthHandlerConcurrency(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	session := &api.Session{}
	session.SetCookieJar(jar)
	store := &mockStore{}

	handler := NewAuthHandler(store, session)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			handler(proton.Auth{
				UID:          fmt.Sprintf("uid-%d", n),
				AccessToken:  fmt.Sprintf("at-%d", n),
				RefreshToken: fmt.Sprintf("rt-%d", n),
			})
		}(i)
	}
	wg.Wait()

	// Verify session.Auth has one of the values (last writer wins).
	if session.Auth.UID == "" {
		t.Fatal("session.Auth.UID is empty after concurrent updates")
	}
}

// TestPropertyAuthHandlerTokenPropagation verifies that for any proton.Auth
// value delivered to the auth handler, Session.Auth reflects those exact
// tokens in memory, and the store holds a matching persisted config.
//
// **Validates: Requirements 3.1, 3.2**
func TestPropertyAuthHandlerTokenPropagation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.String().Draw(t, "uid")
		at := rapid.String().Draw(t, "accessToken")
		rt := rapid.String().Draw(t, "refreshToken")

		jar, _ := cookiejar.New(nil)
		session := &api.Session{}
		session.SetCookieJar(jar)
		store := &mockStore{}
		handler := NewAuthHandler(store, session)

		handler(proton.Auth{
			UID:          uid,
			AccessToken:  at,
			RefreshToken: rt,
		})

		// Verify in-memory state matches.
		if session.Auth.UID != uid {
			t.Fatalf("UID: got %q, want %q", session.Auth.UID, uid)
		}
		if session.Auth.AccessToken != at {
			t.Fatalf("AccessToken: got %q, want %q", session.Auth.AccessToken, at)
		}
		if session.Auth.RefreshToken != rt {
			t.Fatalf("RefreshToken: got %q, want %q", session.Auth.RefreshToken, rt)
		}

		// Verify persisted state matches.
		cfg, err := store.Load()
		if err != nil {
			t.Fatalf("store.Load: %v", err)
		}
		if cfg.UID != uid || cfg.AccessToken != at || cfg.RefreshToken != rt {
			t.Fatalf("persisted config mismatch: %+v", cfg)
		}
	})
}
