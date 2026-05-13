package accountCmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/go-srp"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	common "github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/account"
	cli "github.com/major0/proton-utils/internal/cli"
)

// testKeyData holds pre-computed PGP key material for cookie login tests.
// Generated once per test via generateTestKeyData.
type testKeyData struct {
	keyID         string
	armoredKey    string // armored private key locked with passphrase
	salt          []byte // 16-byte salt
	saltB64       string // base64-encoded salt
	saltedKeyPass []byte // the passphrase that unlocks the key
}

// generateTestKeyData creates a PGP key locked with a passphrase derived
// from the given password and a random salt, matching the Proton key
// derivation chain: srp.MailboxPassword(password, salt) → passphrase → key.
func generateTestKeyData(t *testing.T, password string) testKeyData {
	t.Helper()

	salt, err := crypto.RandomToken(16)
	if err != nil {
		t.Fatalf("generate salt: %v", err)
	}

	// Derive passphrase the same way Proton does.
	passphrase, err := srp.MailboxPassword([]byte(password), salt)
	if err != nil {
		t.Fatalf("mailbox password: %v", err)
	}
	saltedKeyPass := passphrase[len(passphrase)-31:]

	// Generate a PGP key and lock it with the derived passphrase.
	key, err := crypto.GenerateKey("test", "test@test.com", "rsa", 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	locked, err := key.Lock(saltedKeyPass)
	if err != nil {
		t.Fatalf("lock key: %v", err)
	}

	armored, err := locked.Armor()
	if err != nil {
		t.Fatalf("armor key: %v", err)
	}

	return testKeyData{
		keyID:         "test-key-id",
		armoredKey:    armored,
		salt:          salt,
		saltB64:       base64.StdEncoding.EncodeToString(salt),
		saltedKeyPass: saltedKeyPass,
	}
}

// cookieLoginTestServer creates an httptest server that handles the three
// DoJSON endpoints called by cookieLogin after SRP auth:
//   - GET /core/v4/users → User with PGP key
//   - GET /core/v4/addresses → Address with PGP key
//   - GET /core/v4/keys/salts → KeySalts matching the key
func cookieLoginTestServer(t *testing.T, kd testKeyData) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/core/v4/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := map[string]any{
			"Code": 1000,
			"User": map[string]any{
				"ID":          "test-user-id",
				"Name":        "testuser",
				"DisplayName": "Test User",
				"Email":       "test@test.com",
				"Keys": []map[string]any{
					{
						"ID":         kd.keyID,
						"PrivateKey": kd.armoredKey,
						"Primary":    1,
						"Active":     1,
						"Flags":      3,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/core/v4/addresses", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := map[string]any{
			"Code": 1000,
			"Addresses": []map[string]any{
				{
					"ID":          "test-addr-id",
					"Email":       "test@test.com",
					"Send":        1,
					"Receive":     1,
					"Status":      1,
					"Type":        1,
					"Order":       1,
					"DisplayName": "Test User",
					"Keys": []map[string]any{
						{
							"ID":         kd.keyID,
							"PrivateKey": kd.armoredKey,
							"Primary":    1,
							"Active":     1,
							"Flags":      3,
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/core/v4/keys/salts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := map[string]any{
			"Code": 1000,
			"KeySalts": []map[string]any{
				{
					"ID":      kd.keyID,
					"KeySalt": kd.saltB64,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	return httptest.NewServer(mux)
}

// saveFnArgs captures the arguments passed to cookieLoginSaveFn.
type saveFnArgs struct {
	session    *common.Session
	cookieSess *account.CookieSession
	keypass    []byte
}

// TestCookieLogin_FullFlow verifies the complete cookie login sequence:
// anon → transition → SRP → account ops → save. No 2FA.
func TestCookieLogin_FullFlow(t *testing.T) {
	// Generate test PGP key material.
	password := "testpassword"
	kd := generateTestKeyData(t, password)

	// Start httptest server for DoJSON calls.
	ts := cookieLoginTestServer(t, kd)
	defer ts.Close()

	// Save and restore all Fn variables.
	origCreateAnon := createAnonSessionFn
	origTransition := transitionToCookiesFn
	origSRPAuth := cookieSRPAuthFn
	origTwoFA := cookieTwoFAFn
	origSave := cookieLoginSaveFn
	origPrompt := userPromptFn
	origParams := authLoginParams
	t.Cleanup(func() {
		createAnonSessionFn = origCreateAnon
		transitionToCookiesFn = origTransition
		cookieSRPAuthFn = origSRPAuth
		cookieTwoFAFn = origTwoFA
		cookieLoginSaveFn = origSave
		userPromptFn = origPrompt
		authLoginParams = origParams
	})

	// Track call sequence.
	var callOrder []string

	// Mock createAnonSessionFn.
	jar, _ := cookiejar.New(nil)
	createAnonSessionFn = func(_ context.Context) (*account.AnonSessionResp, http.CookieJar, error) {
		callOrder = append(callOrder, "createAnon")
		return &account.AnonSessionResp{
			UID:          "anon-uid",
			AccessToken:  "anon-access",
			RefreshToken: "anon-refresh",
		}, jar, nil
	}

	// Mock transitionToCookiesFn — return a CookieSession pointing at httptest server.
	var mockCookieSess *account.CookieSession
	transitionToCookiesFn = func(_ context.Context, _ *common.Session) (*account.CookieSession, error) {
		callOrder = append(callOrder, "transition")
		testJar, _ := cookiejar.New(nil)
		mockCookieSess = account.NewCookieSession("test-uid", ts.URL, testJar)
		return mockCookieSess, nil
	}

	// Mock cookieSRPAuthFn — return auth with no 2FA.
	cookieSRPAuthFn = func(_ context.Context, _ *account.CookieSession, _ string, _ []byte) (*proton.Auth, error) {
		callOrder = append(callOrder, "srpAuth")
		return &proton.Auth{
			UID:          "auth-uid",
			AccessToken:  "auth-access",
			RefreshToken: "auth-refresh",
			TwoFA:        proton.TwoFAInfo{Enabled: 0},
			PasswordMode: proton.OnePasswordMode,
		}, nil
	}

	// Mock cookieTwoFAFn — should NOT be called.
	cookieTwoFAFn = func(_ context.Context, _ *account.CookieSession, _ string) error {
		callOrder = append(callOrder, "twoFA")
		t.Error("cookieTwoFAFn should not be called when 2FA is disabled")
		return nil
	}

	// Mock cookieLoginSaveFn — capture args.
	var savedArgs saveFnArgs
	cookieLoginSaveFn = func(_ common.SessionStore, _ common.SessionStore, session *common.Session, cs *account.CookieSession, keypass []byte) error {
		callOrder = append(callOrder, "save")
		savedArgs = saveFnArgs{session: session, cookieSess: cs, keypass: keypass}
		return nil
	}

	// No 2FA flag set.
	authLoginParams.twoFA = ""

	ctx := context.Background()
	err := cookieLogin(ctx, &cli.RuntimeContext{}, "testuser", password, "")
	if err != nil {
		t.Fatalf("cookieLogin() error: %v", err)
	}

	// Verify call sequence.
	wantOrder := []string{"createAnon", "transition", "srpAuth", "save"}
	if len(callOrder) != len(wantOrder) {
		t.Fatalf("call order = %v, want %v", callOrder, wantOrder)
	}
	for i, want := range wantOrder {
		if callOrder[i] != want {
			t.Errorf("callOrder[%d] = %q, want %q", i, callOrder[i], want)
		}
	}

	// Verify save was called with the mock cookie session.
	if savedArgs.cookieSess != mockCookieSess {
		t.Error("cookieLoginSaveFn received wrong CookieSession")
	}
	if savedArgs.session == nil {
		t.Fatal("cookieLoginSaveFn received nil session")
	}
	if savedArgs.session.Auth.UID != "auth-uid" {
		t.Errorf("saved session UID = %q, want %q", savedArgs.session.Auth.UID, "auth-uid")
	}
	if len(savedArgs.keypass) == 0 {
		t.Error("saved keypass is empty")
	}
}

// TestCookieLogin_WithTwoFA verifies that when SRP auth returns TOTP-enabled
// auth, cookieLogin prompts for a 2FA code and calls cookieTwoFAFn.
func TestCookieLogin_WithTwoFA(t *testing.T) {
	// Generate test PGP key material.
	password := "testpassword"
	kd := generateTestKeyData(t, password)

	// Start httptest server for DoJSON calls.
	ts := cookieLoginTestServer(t, kd)
	defer ts.Close()

	// Save and restore all Fn variables.
	origCreateAnon := createAnonSessionFn
	origTransition := transitionToCookiesFn
	origSRPAuth := cookieSRPAuthFn
	origTwoFA := cookieTwoFAFn
	origSave := cookieLoginSaveFn
	origPrompt := userPromptFn
	origParams := authLoginParams
	t.Cleanup(func() {
		createAnonSessionFn = origCreateAnon
		transitionToCookiesFn = origTransition
		cookieSRPAuthFn = origSRPAuth
		cookieTwoFAFn = origTwoFA
		cookieLoginSaveFn = origSave
		userPromptFn = origPrompt
		authLoginParams = origParams
	})

	// Track call sequence.
	var callOrder []string

	// Mock createAnonSessionFn.
	jar, _ := cookiejar.New(nil)
	createAnonSessionFn = func(_ context.Context) (*account.AnonSessionResp, http.CookieJar, error) {
		callOrder = append(callOrder, "createAnon")
		return &account.AnonSessionResp{
			UID:          "anon-uid",
			AccessToken:  "anon-access",
			RefreshToken: "anon-refresh",
		}, jar, nil
	}

	// Mock transitionToCookiesFn.
	transitionToCookiesFn = func(_ context.Context, _ *common.Session) (*account.CookieSession, error) {
		callOrder = append(callOrder, "transition")
		testJar, _ := cookiejar.New(nil)
		return account.NewCookieSession("test-uid", ts.URL, testJar), nil
	}

	// Mock cookieSRPAuthFn — return auth WITH 2FA enabled.
	cookieSRPAuthFn = func(_ context.Context, _ *account.CookieSession, _ string, _ []byte) (*proton.Auth, error) {
		callOrder = append(callOrder, "srpAuth")
		return &proton.Auth{
			UID:          "auth-uid",
			AccessToken:  "auth-access",
			RefreshToken: "auth-refresh",
			TwoFA:        proton.TwoFAInfo{Enabled: proton.HasTOTP},
			PasswordMode: proton.OnePasswordMode,
		}, nil
	}

	// Mock cookieTwoFAFn — verify the code.
	var twoFACode string
	cookieTwoFAFn = func(_ context.Context, _ *account.CookieSession, code string) error {
		callOrder = append(callOrder, "twoFA")
		twoFACode = code
		return nil
	}

	// Mock cookieLoginSaveFn.
	cookieLoginSaveFn = func(_ common.SessionStore, _ common.SessionStore, _ *common.Session, _ *account.CookieSession, _ []byte) error {
		callOrder = append(callOrder, "save")
		return nil
	}

	// No 2FA flag — force prompt.
	authLoginParams.twoFA = ""

	// Mock userPromptFn to return a 2FA code.
	var promptCalled bool
	userPromptFn = func(prompt string, _ bool) (string, error) {
		if prompt == "2FA code" {
			promptCalled = true
			return "123456", nil
		}
		return "", fmt.Errorf("unexpected prompt: %q", prompt)
	}

	ctx := context.Background()
	err := cookieLogin(ctx, &cli.RuntimeContext{}, "testuser", password, "")
	if err != nil {
		t.Fatalf("cookieLogin() error: %v", err)
	}

	// Verify call sequence includes 2FA.
	wantOrder := []string{"createAnon", "transition", "srpAuth", "twoFA", "save"}
	if len(callOrder) != len(wantOrder) {
		t.Fatalf("call order = %v, want %v", callOrder, wantOrder)
	}
	for i, want := range wantOrder {
		if callOrder[i] != want {
			t.Errorf("callOrder[%d] = %q, want %q", i, callOrder[i], want)
		}
	}

	// Verify 2FA prompt was called.
	if !promptCalled {
		t.Error("userPromptFn was not called for 2FA code")
	}

	// Verify the code passed to cookieTwoFAFn.
	if twoFACode != "123456" {
		t.Errorf("2FA code = %q, want %q", twoFACode, "123456")
	}
}

// TestCookieLogin_WithTwoFA_FromFlag verifies that when the 2FA code is
// provided via the --2fa flag, userPromptFn is NOT called.
func TestCookieLogin_WithTwoFA_FromFlag(t *testing.T) {
	password := "testpassword"
	kd := generateTestKeyData(t, password)

	ts := cookieLoginTestServer(t, kd)
	defer ts.Close()

	origCreateAnon := createAnonSessionFn
	origTransition := transitionToCookiesFn
	origSRPAuth := cookieSRPAuthFn
	origTwoFA := cookieTwoFAFn
	origSave := cookieLoginSaveFn
	origPrompt := userPromptFn
	origParams := authLoginParams
	t.Cleanup(func() {
		createAnonSessionFn = origCreateAnon
		transitionToCookiesFn = origTransition
		cookieSRPAuthFn = origSRPAuth
		cookieTwoFAFn = origTwoFA
		cookieLoginSaveFn = origSave
		userPromptFn = origPrompt
		authLoginParams = origParams
	})

	jar, _ := cookiejar.New(nil)
	createAnonSessionFn = func(_ context.Context) (*account.AnonSessionResp, http.CookieJar, error) {
		return &account.AnonSessionResp{
			UID: "anon-uid", AccessToken: "anon-access", RefreshToken: "anon-refresh",
		}, jar, nil
	}

	transitionToCookiesFn = func(_ context.Context, _ *common.Session) (*account.CookieSession, error) {
		testJar, _ := cookiejar.New(nil)
		return account.NewCookieSession("test-uid", ts.URL, testJar), nil
	}

	cookieSRPAuthFn = func(_ context.Context, _ *account.CookieSession, _ string, _ []byte) (*proton.Auth, error) {
		return &proton.Auth{
			UID:          "auth-uid",
			AccessToken:  "auth-access",
			RefreshToken: "auth-refresh",
			TwoFA:        proton.TwoFAInfo{Enabled: proton.HasTOTP},
			PasswordMode: proton.OnePasswordMode,
		}, nil
	}

	var twoFACode string
	cookieTwoFAFn = func(_ context.Context, _ *account.CookieSession, code string) error {
		twoFACode = code
		return nil
	}

	cookieLoginSaveFn = func(_ common.SessionStore, _ common.SessionStore, _ *common.Session, _ *account.CookieSession, _ []byte) error {
		return nil
	}

	// Set 2FA code via flag — prompt should NOT be called.
	authLoginParams.twoFA = "654321"

	userPromptFn = func(prompt string, _ bool) (string, error) {
		if strings.Contains(prompt, "2FA") {
			t.Error("userPromptFn should not be called when 2FA code is provided via flag")
		}
		return "", fmt.Errorf("unexpected prompt: %q", prompt)
	}

	ctx := context.Background()
	err := cookieLogin(ctx, &cli.RuntimeContext{}, "testuser", password, "")
	if err != nil {
		t.Fatalf("cookieLogin() error: %v", err)
	}

	if twoFACode != "654321" {
		t.Errorf("2FA code = %q, want %q", twoFACode, "654321")
	}
}
