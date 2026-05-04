package account

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/major0/proton-cli/api"

	"github.com/ProtonMail/go-proton-api"
)

// savingStore records the last saved config and can optionally return an error.
type savingStore struct {
	saved   *api.SessionCredentials
	saveErr error
}

func (s *savingStore) Load() (*api.SessionCredentials, error) { return nil, api.ErrKeyNotFound }
func (s *savingStore) Save(cfg *api.SessionCredentials) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	c := *cfg
	s.saved = &c
	return nil
}
func (s *savingStore) Delete() error           { return nil }
func (s *savingStore) List() ([]string, error) { return nil, nil }
func (s *savingStore) Switch(string) error     { return nil }

// TestCookieLoginSave_CookieStoreFields verifies that the cookie store
// receives UID, serialized cookies, SaltedKeyPass, LastRefresh, and
// CookieAuth=true.
func TestCookieLoginSave_CookieStoreFields(t *testing.T) {
	cookieStore := &savingStore{}
	accountStore := &savingStore{}

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://account.proton.me/api/auth/refresh")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "AUTH-uid1", Value: "auth-val"},
		{Name: "REFRESH-uid1", Value: "refresh-val"},
	})

	session := &api.Session{
		Auth: proton.Auth{UID: "uid1"},
	}
	cookieSess := &CookieSession{
		UID:       "uid1",
		BaseURL:   "https://account.proton.me/api",
		cookieJar: jar,
	}

	err := CookieLoginSave(cookieStore, accountStore, session, cookieSess, []byte("keypass"))
	if err != nil {
		t.Fatalf("CookieLoginSave: %v", err)
	}

	cfg := cookieStore.saved
	if cfg == nil {
		t.Fatal("cookie store was not saved")
	}
	if cfg.UID != "uid1" {
		t.Fatalf("UID = %q, want %q", cfg.UID, "uid1")
	}
	if cfg.SaltedKeyPass == "" {
		t.Fatal("SaltedKeyPass should not be empty")
	}
	if cfg.LastRefresh.IsZero() {
		t.Fatal("LastRefresh should be set")
	}
	if !cfg.CookieAuth {
		t.Fatal("CookieAuth should be true")
	}
	if len(cfg.Cookies) == 0 {
		t.Fatal("Cookies should be persisted")
	}
}

// TestCookieLoginSave_AccountStoreFields verifies that the account store
// receives CookieAuth=true, empty tokens, and SaltedKeyPass.
func TestCookieLoginSave_AccountStoreFields(t *testing.T) {
	cookieStore := &savingStore{}
	accountStore := &savingStore{}

	jar, _ := cookiejar.New(nil)
	session := &api.Session{
		Auth: proton.Auth{UID: "uid2"},
	}
	cookieSess := &CookieSession{
		UID:       "uid2",
		BaseURL:   "https://account.proton.me/api",
		cookieJar: jar,
	}

	err := CookieLoginSave(cookieStore, accountStore, session, cookieSess, []byte("keypass"))
	if err != nil {
		t.Fatalf("CookieLoginSave: %v", err)
	}

	cfg := accountStore.saved
	if cfg == nil {
		t.Fatal("account store was not saved")
	}
	if cfg.UID != "uid2" {
		t.Fatalf("UID = %q, want %q", cfg.UID, "uid2")
	}
	if !cfg.CookieAuth {
		t.Fatal("CookieAuth should be true")
	}
	if cfg.AccessToken != "" {
		t.Fatalf("AccessToken = %q, want empty", cfg.AccessToken)
	}
	if cfg.RefreshToken != "" {
		t.Fatalf("RefreshToken = %q, want empty", cfg.RefreshToken)
	}
	if cfg.SaltedKeyPass == "" {
		t.Fatal("SaltedKeyPass should not be empty")
	}
}

// TestCookieLoginSave_CookieStoreError verifies that a cookie store save
// error is returned.
func TestCookieLoginSave_CookieStoreError(t *testing.T) {
	cookieStore := &savingStore{saveErr: fmt.Errorf("cookie disk full")}
	accountStore := &savingStore{}

	jar, _ := cookiejar.New(nil)
	session := &api.Session{Auth: proton.Auth{UID: "uid3"}}
	cookieSess := &CookieSession{UID: "uid3", cookieJar: jar}

	err := CookieLoginSave(cookieStore, accountStore, session, cookieSess, []byte("kp"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if accountStore.saved != nil {
		t.Fatal("account store should not be saved when cookie store fails")
	}
}

// TestCookieLoginSave_AccountStoreError verifies that an account store save
// error is returned.
func TestCookieLoginSave_AccountStoreError(t *testing.T) {
	cookieStore := &savingStore{}
	accountStore := &savingStore{saveErr: fmt.Errorf("account disk full")}

	jar, _ := cookiejar.New(nil)
	session := &api.Session{Auth: proton.Auth{UID: "uid4"}}
	cookieSess := &CookieSession{UID: "uid4", cookieJar: jar}

	err := CookieLoginSave(cookieStore, accountStore, session, cookieSess, []byte("kp"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Cookie store should have been saved before the account store error.
	if cookieStore.saved == nil {
		t.Fatal("cookie store should have been saved")
	}
}
