package accountCmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	common "github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/account"
	"github.com/major0/proton-utils/api/config"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
	"pgregory.net/rapid"
)

// testCmdWithRC creates a cobra.Command with a RuntimeContext that has
// the given ProtonOpts. Used by tests that call attemptLogin directly.
func testCmdWithRC() *cobra.Command {
	cmd := &cobra.Command{}
	cli.SetContext(cmd, &cli.RuntimeContext{})
	return cmd
}

// setRCOnCmd sets a RuntimeContext with the given session store on a command.
func setRCOnCmd(cmd *cobra.Command, store common.SessionStore) {
	cli.SetContext(cmd, &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
	})
}

// hvDetailsJSON creates a JSON-encoded ErrDetails for HV test errors.
func hvDetailsJSON(methods []string, token string) proton.ErrDetails {
	d := proton.APIHVDetails{
		Methods: methods,
		Token:   token,
	}
	b, _ := json.Marshal(d)
	return proton.ErrDetails(b)
}

func TestHasCaptchaMethod(t *testing.T) {
	tests := []struct {
		name    string
		methods []string
		want    bool
	}{
		{"captcha only", []string{"captcha"}, true},
		{"captcha and sms", []string{"captcha", "sms"}, true},
		{"captcha last", []string{"email", "sms", "captcha"}, true},
		{"sms only", []string{"sms"}, false},
		{"email only", []string{"email"}, false},
		{"empty", []string{}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasCaptchaMethod(tt.methods); got != tt.want {
				t.Errorf("hasCaptchaMethod(%v) = %v, want %v", tt.methods, got, tt.want)
			}
		})
	}
}

func TestHasCaptchaMethodProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random list of methods.
		methods := rapid.SliceOf(rapid.StringMatching(`[a-z]{3,10}`)).Draw(t, "methods")

		// If "captcha" is in the list, hasCaptchaMethod must return true.
		hasCaptcha := false
		for _, m := range methods {
			if m == "captcha" {
				hasCaptcha = true
				break
			}
		}

		got := hasCaptchaMethod(methods)
		if got != hasCaptcha {
			t.Fatalf("hasCaptchaMethod(%v) = %v, want %v", methods, got, hasCaptcha)
		}
	})
}

func TestHVErrorDetection(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		isHV     bool
		isAPIErr bool
	}{
		{
			"APIError 9001 is HV error",
			&proton.APIError{Code: proton.HumanVerificationRequired},
			true,
			true,
		},
		{
			"APIError 8002 is not HV error",
			&proton.APIError{Code: proton.PasswordWrong},
			false,
			true,
		},
		{
			"plain error is not APIError",
			fmt.Errorf("some error"),
			false,
			false,
		},
		{
			"wrapped APIError 9001 detected via errors.As",
			fmt.Errorf("login failed: %w", &proton.APIError{Code: proton.HumanVerificationRequired, Message: "HV required"}),
			true,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target *proton.APIError
			gotIsAPI := errors.As(tt.err, &target)
			if gotIsAPI != tt.isAPIErr {
				t.Fatalf("errors.As = %v, want %v", gotIsAPI, tt.isAPIErr)
			}
			if gotIsAPI {
				gotIsHV := target.IsHVError()
				if gotIsHV != tt.isHV {
					t.Errorf("IsHVError() = %v, want %v", gotIsHV, tt.isHV)
				}
			}
		})
	}
}

func TestPromptCredentials(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		wantUser string
		wantPass string
	}{
		{
			"both provided via flags",
			"alice",
			"secret123",
			"alice",
			"secret123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := authLoginParams
			t.Cleanup(func() { authLoginParams = orig })

			authLoginParams.username = tt.username
			authLoginParams.password = tt.password

			user, pass, err := promptCredentials()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if user != tt.wantUser {
				t.Errorf("username = %q, want %q", user, tt.wantUser)
			}
			if pass != tt.wantPass {
				t.Errorf("password = %q, want %q", pass, tt.wantPass)
			}
		})
	}
}

func TestPromptCredentials_Prompted(t *testing.T) {
	tests := []struct {
		name       string
		flagUser   string
		flagPass   string
		promptResp map[string]string
		wantUser   string
		wantPass   string
	}{
		{
			"username prompted",
			"",
			"flagpass",
			map[string]string{"Username": "prompted-user"},
			"prompted-user",
			"flagpass",
		},
		{
			"password prompted",
			"flaguser",
			"",
			map[string]string{"Password": "prompted-pass"},
			"flaguser",
			"prompted-pass",
		},
		{
			"both prompted",
			"",
			"",
			map[string]string{"Username": "u", "Password": "p"},
			"u",
			"p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origParams := authLoginParams
			origPrompt := userPromptFn
			t.Cleanup(func() {
				authLoginParams = origParams
				userPromptFn = origPrompt
			})

			authLoginParams.username = tt.flagUser
			authLoginParams.password = tt.flagPass

			userPromptFn = func(prompt string, _ bool) (string, error) {
				if resp, ok := tt.promptResp[prompt]; ok {
					return resp, nil
				}
				return "", fmt.Errorf("unexpected prompt: %q", prompt)
			}

			user, pass, err := promptCredentials()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if user != tt.wantUser {
				t.Errorf("username = %q, want %q", user, tt.wantUser)
			}
			if pass != tt.wantPass {
				t.Errorf("password = %q, want %q", pass, tt.wantPass)
			}
		})
	}
}

func TestPromptCredentials_Error(t *testing.T) {
	tests := []struct {
		name     string
		flagUser string
		flagPass string
		errOn    string
		wantErr  string
	}{
		{
			"username prompt error",
			"",
			"pass",
			"Username",
			"input failed",
		},
		{
			"password prompt error",
			"user",
			"",
			"Password",
			"input failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origParams := authLoginParams
			origPrompt := userPromptFn
			t.Cleanup(func() {
				authLoginParams = origParams
				userPromptFn = origPrompt
			})

			authLoginParams.username = tt.flagUser
			authLoginParams.password = tt.flagPass

			userPromptFn = func(prompt string, _ bool) (string, error) {
				if prompt == tt.errOn {
					return "", fmt.Errorf("input failed")
				}
				return "value", nil
			}

			_, _, err := promptCredentials()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestHandleTwoFA_NotEnabled(t *testing.T) {
	// When TOTP is not enabled, handleTwoFA should return nil immediately.
	session := &common.Session{
		Auth: proton.Auth{
			TwoFA: proton.TwoFAInfo{
				Enabled: 0, // TOTP not enabled
			},
		},
	}

	ctx := context.Background()
	if err := handleTwoFA(ctx, session); err != nil {
		t.Fatalf("handleTwoFA() with TOTP disabled: unexpected error: %v", err)
	}
}

func TestHandleTwoFA_EnabledWithFlag(t *testing.T) {
	// When TOTP is enabled and 2FA code is provided via flag,
	// handleTwoFA will try to call session.Client.Auth2FA which requires
	// a real client. We verify the function correctly detects TOTP is
	// enabled by confirming it panics on nil client (proving the early
	// return was NOT taken).
	session := &common.Session{
		Auth: proton.Auth{
			TwoFA: proton.TwoFAInfo{
				Enabled: proton.HasTOTP,
			},
		},
	}

	// Save and restore the package-level params.
	orig := authLoginParams
	t.Cleanup(func() { authLoginParams = orig })
	authLoginParams.twoFA = "123456"

	ctx := context.Background()

	// The nil client will panic — recover to confirm the code path was reached.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("handleTwoFA() with TOTP enabled and nil client: expected panic, got none")
		}
	}()

	_ = handleTwoFA(ctx, session)
	t.Fatal("handleTwoFA() should have panicked on nil client")
}

func TestHandleTwoFA_PromptedCode(t *testing.T) {
	// When TOTP is enabled and no flag is set, handleTwoFA prompts for the code.
	session := &common.Session{
		Auth: proton.Auth{
			TwoFA: proton.TwoFAInfo{
				Enabled: proton.HasTOTP,
			},
		},
	}

	origParams := authLoginParams
	origPrompt := userPromptFn
	t.Cleanup(func() {
		authLoginParams = origParams
		userPromptFn = origPrompt
	})
	authLoginParams.twoFA = "" // force prompt

	userPromptFn = func(prompt string, _ bool) (string, error) {
		if prompt == "2FA code" {
			return "654321", nil
		}
		return "", fmt.Errorf("unexpected prompt: %q", prompt)
	}

	ctx := context.Background()

	// Will panic on nil client after getting the code — confirms prompt path works.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil client after prompt")
		}
	}()

	_ = handleTwoFA(ctx, session)
}

func TestHandleTwoFA_PromptError(t *testing.T) {
	session := &common.Session{
		Auth: proton.Auth{
			TwoFA: proton.TwoFAInfo{
				Enabled: proton.HasTOTP,
			},
		},
	}

	origParams := authLoginParams
	origPrompt := userPromptFn
	t.Cleanup(func() {
		authLoginParams = origParams
		userPromptFn = origPrompt
	})
	authLoginParams.twoFA = ""

	userPromptFn = func(_ string, _ bool) (string, error) {
		return "", fmt.Errorf("terminal closed")
	}

	ctx := context.Background()
	err := handleTwoFA(ctx, session)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "terminal closed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "terminal closed")
	}
}

func TestAttemptLogin_ErrorPaths(t *testing.T) {
	origLogin := sessionFromLoginFn
	origRetry := sessionRetryWithHVFn
	origManual := manualSolver
	origChrome := captchaSolver
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		sessionRetryWithHVFn = origRetry
		manualSolver = origManual
		captchaSolver = origChrome
	})

	tests := []struct {
		name     string
		loginErr error
		wantErr  string
	}{
		{
			"non-API error",
			fmt.Errorf("network timeout"),
			"network timeout",
		},
		{
			"non-HV API error",
			&proton.APIError{Code: proton.PasswordWrong, Message: "wrong password"},
			"wrong password",
		},
		{
			"HV error without captcha method",
			&proton.APIError{
				Code:    proton.HumanVerificationRequired,
				Message: "HV required",
				Details: hvDetailsJSON([]string{"sms", "email"}, "tok123"),
			},
			"unsupported HV methods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
				return nil, tt.loginErr
			}

			ctx := context.Background()
			_, err := attemptLogin(ctx, testCmdWithRC(), "user", "pass")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestAttemptLogin_Success(t *testing.T) {
	origLogin := sessionFromLoginFn
	t.Cleanup(func() { sessionFromLoginFn = origLogin })

	wantSession := &common.Session{
		Auth: proton.Auth{UserID: "test-user"},
	}

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return wantSession, nil
	}

	ctx := context.Background()
	got, err := attemptLogin(ctx, testCmdWithRC(), "user", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantSession {
		t.Error("returned session does not match expected")
	}
}

func TestAttemptLogin_HVWithCaptcha(t *testing.T) {
	origLogin := sessionFromLoginFn
	origRetry := sessionRetryWithHVFn
	origManual := manualSolver
	origParams := authLoginParams
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		sessionRetryWithHVFn = origRetry
		manualSolver = origManual
		authLoginParams = origParams
	})

	authLoginParams.noBrowser = true

	wantSession := &common.Session{
		Auth: proton.Auth{UserID: "hv-user"},
	}

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return wantSession, &proton.APIError{
			Code:    proton.HumanVerificationRequired,
			Message: "HV required",
			Details: hvDetailsJSON([]string{"captcha"}, "hv-tok"),
		}
	}

	manualSolver = func(hv *proton.APIHVDetails) (string, error) {
		return "solved-" + hv.Token, nil
	}

	sessionRetryWithHVFn = func(_ context.Context, _ *common.Session, _, _ string, hv *proton.APIHVDetails) error {
		if !strings.HasPrefix(hv.Token, "solved-") {
			return fmt.Errorf("expected solved token, got %q", hv.Token)
		}
		return nil
	}

	ctx := context.Background()
	got, err := attemptLogin(ctx, testCmdWithRC(), "user", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantSession {
		t.Error("returned session does not match expected")
	}
}

func TestAttemptLogin_CaptchaSolveError(t *testing.T) {
	origLogin := sessionFromLoginFn
	origManual := manualSolver
	origParams := authLoginParams
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		manualSolver = origManual
		authLoginParams = origParams
	})

	authLoginParams.noBrowser = true

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return nil, &proton.APIError{
			Code:    proton.HumanVerificationRequired,
			Message: "HV required",
			Details: hvDetailsJSON([]string{"captcha"}, "hv-tok"),
		}
	}

	manualSolver = func(_ *proton.APIHVDetails) (string, error) {
		return "", fmt.Errorf("user cancelled")
	}

	ctx := context.Background()
	_, err := attemptLogin(ctx, testCmdWithRC(), "user", "pass")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "user cancelled") {
		t.Errorf("error = %q, want substring %q", err.Error(), "user cancelled")
	}
}

func TestAttemptLogin_RetryError(t *testing.T) {
	origLogin := sessionFromLoginFn
	origRetry := sessionRetryWithHVFn
	origManual := manualSolver
	origParams := authLoginParams
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		sessionRetryWithHVFn = origRetry
		manualSolver = origManual
		authLoginParams = origParams
	})

	authLoginParams.noBrowser = true

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return &common.Session{}, &proton.APIError{
			Code:    proton.HumanVerificationRequired,
			Message: "HV required",
			Details: hvDetailsJSON([]string{"captcha"}, "hv-tok"),
		}
	}

	manualSolver = func(_ *proton.APIHVDetails) (string, error) {
		return "solved", nil
	}

	sessionRetryWithHVFn = func(_ context.Context, _ *common.Session, _, _ string, _ *proton.APIHVDetails) error {
		return fmt.Errorf("retry failed: bad credentials")
	}

	ctx := context.Background()
	_, err := attemptLogin(ctx, testCmdWithRC(), "user", "pass")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "retry failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "retry failed")
	}
}

func TestSelectKeyPassword(t *testing.T) {
	tests := []struct {
		name         string
		passwordMode proton.PasswordMode
		password     string
		mboxpass     string
		want         string
	}{
		{
			"one password mode uses login password",
			proton.OnePasswordMode,
			"loginpass",
			"",
			"loginpass",
		},
		{
			"one password mode ignores mboxpass",
			proton.OnePasswordMode,
			"loginpass",
			"mboxpass",
			"loginpass",
		},
		{
			"two password mode uses mboxpass",
			proton.TwoPasswordMode,
			"loginpass",
			"mboxpass",
			"mboxpass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectKeyPassword(tt.passwordMode, tt.password, tt.mboxpass)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("selectKeyPassword() = %q, want %q", string(got), tt.want)
			}
		})
	}
}

func TestSelectKeyPassword_Prompted(t *testing.T) {
	origPrompt := userPromptFn
	t.Cleanup(func() { userPromptFn = origPrompt })

	userPromptFn = func(prompt string, _ bool) (string, error) {
		if prompt == "Mailbox password" {
			return "prompted-mbox", nil
		}
		return "", fmt.Errorf("unexpected prompt: %q", prompt)
	}

	got, err := selectKeyPassword(proton.TwoPasswordMode, "loginpass", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "prompted-mbox" {
		t.Errorf("got %q, want %q", string(got), "prompted-mbox")
	}
}

func TestSelectKeyPassword_PromptError(t *testing.T) {
	origPrompt := userPromptFn
	t.Cleanup(func() { userPromptFn = origPrompt })

	userPromptFn = func(_ string, _ bool) (string, error) {
		return "", fmt.Errorf("no terminal")
	}

	_, err := selectKeyPassword(proton.TwoPasswordMode, "loginpass", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no terminal") {
		t.Errorf("error = %q, want substring %q", err.Error(), "no terminal")
	}
}

func TestDeriveAndSave(t *testing.T) {
	tests := []struct {
		name       string
		passMode   proton.PasswordMode
		password   string
		mboxpass   string
		saltErr    error
		saveErr    error
		wantErr    string
		wantSalted string
	}{
		{
			"one password mode success",
			proton.OnePasswordMode,
			"loginpass",
			"",
			nil,
			nil,
			"",
			"loginpass",
		},
		{
			"two password mode success",
			proton.TwoPasswordMode,
			"loginpass",
			"mboxpass",
			nil,
			nil,
			"",
			"mboxpass",
		},
		{
			"salt error",
			proton.OnePasswordMode,
			"pass",
			"",
			fmt.Errorf("salt failed"),
			nil,
			"salt failed",
			"",
		},
		{
			"save error",
			proton.OnePasswordMode,
			"pass",
			"",
			nil,
			fmt.Errorf("save failed"),
			"save failed",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origSalt := saltKeyPassFn
			origSave := sessionSaveFn
			origCookieDelete := cookieStoreDeleteFn
			t.Cleanup(func() {
				saltKeyPassFn = origSalt
				sessionSaveFn = origSave
				cookieStoreDeleteFn = origCookieDelete
			})

			var gotSalted string
			saltKeyPassFn = func(_ context.Context, _ *common.Session, password []byte) ([]byte, error) {
				gotSalted = string(password)
				if tt.saltErr != nil {
					return nil, tt.saltErr
				}
				return []byte("derived-key"), nil
			}

			sessionSaveFn = func(_ common.SessionStore, _ *common.Session, keypass []byte) error {
				if string(keypass) != "derived-key" {
					t.Errorf("keypass = %q, want %q", string(keypass), "derived-key")
				}
				return tt.saveErr
			}

			cookieStoreDeleteFn = func(_ common.SessionStore) error { return nil }

			session := &common.Session{
				Auth: proton.Auth{PasswordMode: tt.passMode},
			}

			ctx := context.Background()
			err := deriveAndSave(ctx, &cli.RuntimeContext{}, session, tt.password, tt.mboxpass, false)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotSalted != tt.wantSalted {
				t.Errorf("salted password = %q, want %q", gotSalted, tt.wantSalted)
			}
		})
	}
}

func TestAuthLoginCmd_RunE_LoginError(t *testing.T) {
	origLogin := sessionFromLoginFn
	origParams := authLoginParams
	origPrompt := userPromptFn
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		authLoginParams = origParams
		userPromptFn = origPrompt
	})

	setRCOnCmd(authLoginCmd, &successStore{accounts: []string{}})

	authLoginParams.username = "testuser"
	authLoginParams.password = "testpass"

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return nil, fmt.Errorf("auth failed")
	}

	err := authLoginCmd.RunE(authLoginCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "auth failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "auth failed")
	}
}

func TestAuthLoginCmd_RunE_PromptError(t *testing.T) {
	origParams := authLoginParams
	origPrompt := userPromptFn
	t.Cleanup(func() {
		authLoginParams = origParams
		userPromptFn = origPrompt
	})

	setRCOnCmd(authLoginCmd, &successStore{accounts: []string{}})

	// Force prompting by clearing flags.
	authLoginParams.username = ""
	authLoginParams.password = ""

	userPromptFn = func(_ string, _ bool) (string, error) {
		return "", fmt.Errorf("no tty")
	}

	err := authLoginCmd.RunE(authLoginCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no tty") {
		t.Errorf("error = %q, want substring %q", err.Error(), "no tty")
	}
}

func TestAccountCmd_Run(_ *testing.T) {
	// The account command's Run handler just prints help.
	// Verify it doesn't panic.
	accountCmd.Run(accountCmd, nil)
}

func TestAccountAddressCmd_RestoreError(t *testing.T) {
	store := &failingStore{err: fmt.Errorf("not logged in")}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(accountAddressCmd, rc)

	err := accountAddressCmd.RunE(accountAddressCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want substring %q", err.Error(), "not logged in")
	}
}

func TestAccountInfoCmd_RestoreError(t *testing.T) {
	store := &failingStore{err: fmt.Errorf("session expired")}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(accountInfoCmd, rc)

	err := accountInfoCmd.RunE(accountInfoCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "session expired") {
		t.Errorf("error = %q, want substring %q", err.Error(), "session expired")
	}
}

func TestAccountListCmd_RestoreError(t *testing.T) {
	store := &failingStore{err: fmt.Errorf("store corrupted")}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(accountListCmd, rc)

	err := accountListCmd.RunE(accountListCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "store corrupted") {
		t.Errorf("error = %q, want substring %q", err.Error(), "store corrupted")
	}
}

func TestAuthLogoutCmd_RestoreError(t *testing.T) {
	origForce := authLogoutForce
	t.Cleanup(func() {
		authLogoutForce = origForce
	})

	store := &failingStore{err: fmt.Errorf("disk error")}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false

	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "disk error") {
		t.Errorf("error = %q, want substring %q", err.Error(), "disk error")
	}
}

func TestAuthLogoutCmd_NotLoggedIn(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})

	store := &failingStore{err: common.ErrKeyNotFound}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = false
	logoutCookieDeleteFn = func(_ common.SessionStore) error { return nil }

	// When not logged in and not forced, it proceeds to SessionRevoke
	// with nil session, which calls store.Delete().
	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	// store.Delete() returns nil in our mock.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAuthLogoutCmd_ForceWithError(t *testing.T) {
	origForce := authLogoutForce
	origCookieDelete := logoutCookieDeleteFn
	t.Cleanup(func() {
		authLogoutForce = origForce
		logoutCookieDeleteFn = origCookieDelete
	})

	store := &failingStore{err: fmt.Errorf("some error")}
	rc := &cli.RuntimeContext{
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "account",
		Timeout:      5,
	}
	cli.SetContext(authLogoutCmd, rc)
	authLogoutForce = true
	logoutCookieDeleteFn = func(_ common.SessionStore) error { return nil }

	// With force=true, even non-ErrNotLoggedIn errors are ignored
	// and SessionRevoke is called with nil session.
	err := authLogoutCmd.RunE(authLogoutCmd, nil)
	// store.Delete() returns nil in our mock.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// failingStore is a SessionStore that always returns an error on Load.
type failingStore struct {
	err error
}

func (f *failingStore) Load() (*common.SessionCredentials, error) { return nil, f.err }
func (f *failingStore) Save(_ *common.SessionCredentials) error   { return nil }
func (f *failingStore) Delete() error                             { return nil }
func (f *failingStore) List() ([]string, error)                   { return nil, f.err }
func (f *failingStore) Switch(_ string) error                     { return nil }

// successStore is a SessionStore that returns test data.
type successStore struct {
	accounts []string
}

func (s *successStore) Load() (*common.SessionCredentials, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *successStore) Save(_ *common.SessionCredentials) error { return nil }
func (s *successStore) Delete() error                           { return nil }
func (s *successStore) List() ([]string, error)                 { return s.accounts, nil }
func (s *successStore) Switch(_ string) error                   { return nil }

func TestAccountListCmd_Success(t *testing.T) {

	setRCOnCmd(accountListCmd, &successStore{accounts: []string{"alice@proton.me", "bob@proton.me"}})

	err := accountListCmd.RunE(accountListCmd, nil)
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
}

func TestAccountListCmd_Empty(t *testing.T) {

	setRCOnCmd(accountListCmd, &successStore{accounts: []string{}})

	err := accountListCmd.RunE(accountListCmd, nil)
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
}

func TestAttemptLogin_HVDetailsParseError(t *testing.T) {
	origLogin := sessionFromLoginFn
	t.Cleanup(func() { sessionFromLoginFn = origLogin })

	// Return an HV error with invalid JSON in Details.
	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return nil, &proton.APIError{
			Code:    proton.HumanVerificationRequired,
			Message: "HV required",
			Details: proton.ErrDetails([]byte("not valid json")),
		}
	}

	ctx := context.Background()
	_, err := attemptLogin(ctx, testCmdWithRC(), "user", "pass")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "extracting HV details") {
		t.Errorf("error = %q, want substring %q", err.Error(), "extracting HV details")
	}
}

func TestAuthLoginCmd_RunE_FullPath(t *testing.T) {
	// Test the full RunE path including AddAuthHandler/AddDeauthHandler.
	origLogin := sessionFromLoginFn
	origSalt := saltKeyPassFn
	origSave := sessionSaveFn
	origParams := authLoginParams
	origCookieDelete := cookieStoreDeleteFn
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		saltKeyPassFn = origSalt
		sessionSaveFn = origSave
		authLoginParams = origParams
		cookieStoreDeleteFn = origCookieDelete
	})

	authLoginParams.username = "testuser"
	authLoginParams.password = "testpass"
	authLoginParams.twoFA = ""
	authLoginParams.mboxpass = ""

	// Create a real Manager+Client so AddAuthHandler doesn't panic.
	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return &common.Session{
			Client: client,
			Auth: proton.Auth{
				TwoFA:        proton.TwoFAInfo{Enabled: 0},
				PasswordMode: proton.OnePasswordMode,
			},
		}, nil
	}

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return []byte("salted"), nil
	}

	sessionSaveFn = func(_ common.SessionStore, _ *common.Session, _ []byte) error {
		return nil
	}

	cookieStoreDeleteFn = func(_ common.SessionStore) error { return nil }

	setRCOnCmd(authLoginCmd, &successStore{accounts: []string{}})

	err := authLoginCmd.RunE(authLoginCmd, nil)
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
}

func TestAuthLoginCmd_RunE_TwoFAError(t *testing.T) {
	origLogin := sessionFromLoginFn
	origParams := authLoginParams
	origPrompt := userPromptFn
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		authLoginParams = origParams
		userPromptFn = origPrompt
	})

	authLoginParams.username = "testuser"
	authLoginParams.password = "testpass"
	authLoginParams.twoFA = ""

	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return &common.Session{
			Client: client,
			Auth: proton.Auth{
				TwoFA:        proton.TwoFAInfo{Enabled: proton.HasTOTP},
				PasswordMode: proton.OnePasswordMode,
			},
		}, nil
	}

	userPromptFn = func(_ string, _ bool) (string, error) {
		return "", fmt.Errorf("2fa prompt failed")
	}

	setRCOnCmd(authLoginCmd, &successStore{accounts: []string{}})

	err := authLoginCmd.RunE(authLoginCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "2fa prompt failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "2fa prompt failed")
	}
}

func TestAuthLoginCmd_RunE_DeriveError(t *testing.T) {
	origLogin := sessionFromLoginFn
	origSalt := saltKeyPassFn
	origParams := authLoginParams
	t.Cleanup(func() {
		sessionFromLoginFn = origLogin
		saltKeyPassFn = origSalt
		authLoginParams = origParams
	})

	authLoginParams.username = "testuser"
	authLoginParams.password = "testpass"
	authLoginParams.twoFA = ""
	authLoginParams.mboxpass = ""

	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")

	sessionFromLoginFn = func(_ context.Context, _ []proton.Option, _, _ string, _ *proton.APIHVDetails, _ func(*proton.Manager)) (*common.Session, error) {
		return &common.Session{
			Client: client,
			Auth: proton.Auth{
				TwoFA:        proton.TwoFAInfo{Enabled: 0},
				PasswordMode: proton.OnePasswordMode,
			},
		}, nil
	}

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return nil, fmt.Errorf("salt error")
	}

	setRCOnCmd(authLoginCmd, &successStore{accounts: []string{}})

	err := authLoginCmd.RunE(authLoginCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "salt error") {
		t.Errorf("error = %q, want substring %q", err.Error(), "salt error")
	}
}

func TestDefaultSessionSaveFn(t *testing.T) {

	setRCOnCmd(authLoginCmd, &successStore{accounts: []string{}})

	// Call the real sessionSaveFn default implementation.
	// It calls common.SessionSave which needs a session with Auth data.
	session := &common.Session{
		Auth: proton.Auth{UID: "test-uid"},
	}

	// This will panic because the session has no cookie jar.
	// Recover to confirm the function body was entered.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from nil cookie jar")
		}
	}()

	_ = sessionSaveFn(&successStore{accounts: []string{}}, session, []byte("keypass"))
}

func TestDefaultSaltKeyPassFn(t *testing.T) {
	// Call the real saltKeyPassFn default implementation.
	// It calls common.SaltKeyPass which needs a real Client.
	m := proton.New()
	client := m.NewClient("uid", "acc", "ref")

	session := &common.Session{
		Client: client,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel to force fast failure

	// This will fail because context is cancelled, but the function body is covered.
	_, err := saltKeyPassFn(ctx, session, []byte("pass"))
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestRenderAddresses(t *testing.T) {
	tests := []struct {
		name      string
		addresses []addressInfo
		wantSubs  []string
	}{
		{
			"empty list",
			[]addressInfo{},
			[]string{"ADDRESS", "TYPE", "STATE"},
		},
		{
			"single address",
			[]addressInfo{
				{Email: "alice@proton.me", Type: 1, Status: 1},
			},
			[]string{"alice@proton.me"},
		},
		{
			"multiple addresses",
			[]addressInfo{
				{Email: "alice@proton.me", Type: 1, Status: 1},
				{Email: "bob@pm.me", Type: 2, Status: 0},
			},
			[]string{"alice@proton.me", "bob@pm.me"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			renderAddresses(&buf, tt.addresses)
			output := buf.String()
			for _, sub := range tt.wantSubs {
				if !strings.Contains(output, sub) {
					t.Errorf("output missing %q:\n%s", sub, output)
				}
			}
		})
	}
}

func TestRenderUserInfo(t *testing.T) {
	tests := []struct {
		name     string
		user     userInfo
		wantSubs []string
	}{
		{
			"basic user with storage",
			userInfo{
				ID:                "user-123",
				DisplayName:       "Alice",
				Name:              "alice",
				Email:             "alice@proton.me",
				MaxSpace:          1073741824, // 1 GB
				UsedSpace:         536870912,  // 512 MB
				MailUsedSpace:     268435456,  // 256 MB
				DriveUsedSpace:    134217728,  // 128 MB
				CalendarUsedSpace: 67108864,   // 64 MB
				PassUsedSpace:     33554432,   // 32 MB
				ContactUsedSpace:  33554432,   // 32 MB
			},
			[]string{"user-123", "Alice", "alice", "alice@proton.me", "Storage:", "Mail", "Drive"},
		},
		{
			"user with zero max space",
			userInfo{
				ID:   "user-456",
				Name: "bob",
			},
			[]string{"user-456", "bob"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			renderUserInfo(&buf, tt.user)
			output := buf.String()
			for _, sub := range tt.wantSubs {
				if !strings.Contains(output, sub) {
					t.Errorf("output missing %q:\n%s", sub, output)
				}
			}
		})
	}
}

// TestLogLoginDiagnostics verifies that logLoginDiagnostics doesn't panic.
func TestLogLoginDiagnostics(_ *testing.T) {
	// Just verify it doesn't panic when called.
	logLoginDiagnostics()
}

// TestLoginUsesAccountService verifies that SetServiceCmd("account")
// configures the RuntimeContext with account service ProtonOpts.
func TestLoginUsesAccountService(t *testing.T) {
	cmd := &cobra.Command{}
	rc := &cli.RuntimeContext{
		Config:      config.DefaultConfig(),
		SessionFile: "/tmp/test-sessions.db",
		Account:     "default",
	}
	cli.SetContext(cmd, rc)

	cli.SetServiceCmd(cmd, "account")

	if rc.ServiceName != "account" {
		t.Errorf("ServiceName = %q, want %q", rc.ServiceName, "account")
	}

	// ProtonOpts should be non-empty after SetServiceCmd.
	if len(rc.ProtonOpts) == 0 {
		t.Error("ProtonOpts is empty after SetServiceCmd(account)")
	}
}

// --- Cookie transition login flow tests (Task 4.1) ---

// TestDeriveAndSave_CookieSession_Success verifies that when cookieAuth=true,
// TransitionToCookies is called and CookieLoginSave is called.
func TestDeriveAndSave_CookieSession_Success(t *testing.T) {
	origSalt := saltKeyPassFn
	origSave := sessionSaveFn
	origTransition := transitionToCookiesFn
	origCookieSave := cookieLoginSaveFn
	t.Cleanup(func() {
		saltKeyPassFn = origSalt
		sessionSaveFn = origSave
		transitionToCookiesFn = origTransition
		cookieLoginSaveFn = origCookieSave
	})

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return []byte("derived-key"), nil
	}

	var bearerSaveCalled bool
	sessionSaveFn = func(_ common.SessionStore, _ *common.Session, _ []byte) error {
		bearerSaveCalled = true
		return nil
	}

	var transitionCalled bool
	wantCookieSess := &account.CookieSession{}
	transitionToCookiesFn = func(_ context.Context, _ *common.Session) (*account.CookieSession, error) {
		transitionCalled = true
		return wantCookieSess, nil
	}

	var cookieSaveCalled bool
	cookieLoginSaveFn = func(_ common.SessionStore, _ common.SessionStore, _ *common.Session, cs *account.CookieSession, keypass []byte) error {
		cookieSaveCalled = true
		if cs != wantCookieSess {
			t.Error("CookieLoginSave received wrong CookieSession")
		}
		if string(keypass) != "derived-key" {
			t.Errorf("keypass = %q, want %q", string(keypass), "derived-key")
		}
		return nil
	}

	session := &common.Session{
		Auth: proton.Auth{PasswordMode: proton.OnePasswordMode},
	}

	err := deriveAndSave(context.Background(), &cli.RuntimeContext{}, session, "pass", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !transitionCalled {
		t.Error("TransitionToCookies was not called")
	}
	if !cookieSaveCalled {
		t.Error("CookieLoginSave was not called")
	}
	if bearerSaveCalled {
		t.Error("Bearer sessionSaveFn should not be called when cookieAuth=true")
	}
}

// TestDeriveAndSave_CookieSession_TransitionError verifies that when
// TransitionToCookies fails, the error is returned and no saves happen.
func TestDeriveAndSave_CookieSession_TransitionError(t *testing.T) {
	origSalt := saltKeyPassFn
	origSave := sessionSaveFn
	origTransition := transitionToCookiesFn
	origCookieSave := cookieLoginSaveFn
	t.Cleanup(func() {
		saltKeyPassFn = origSalt
		sessionSaveFn = origSave
		transitionToCookiesFn = origTransition
		cookieLoginSaveFn = origCookieSave
	})

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return []byte("derived-key"), nil
	}

	var bearerSaveCalled bool
	sessionSaveFn = func(_ common.SessionStore, _ *common.Session, _ []byte) error {
		bearerSaveCalled = true
		return nil
	}

	transitionToCookiesFn = func(_ context.Context, _ *common.Session) (*account.CookieSession, error) {
		return nil, fmt.Errorf("transition failed: server error")
	}

	var cookieSaveCalled bool
	cookieLoginSaveFn = func(_ common.SessionStore, _ common.SessionStore, _ *common.Session, _ *account.CookieSession, _ []byte) error {
		cookieSaveCalled = true
		return nil
	}

	session := &common.Session{
		Auth: proton.Auth{PasswordMode: proton.OnePasswordMode},
	}

	err := deriveAndSave(context.Background(), &cli.RuntimeContext{}, session, "pass", "", true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "transition failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "transition failed")
	}
	if bearerSaveCalled {
		t.Error("Bearer save should not be called on transition failure")
	}
	if cookieSaveCalled {
		t.Error("CookieLoginSave should not be called on transition failure")
	}
}

// TestDeriveAndSave_NoCookieSession_BearerPath verifies that when
// cookieAuth=false, the existing Bearer save path is used unchanged.
func TestDeriveAndSave_NoCookieSession_BearerPath(t *testing.T) {
	origSalt := saltKeyPassFn
	origSave := sessionSaveFn
	origTransition := transitionToCookiesFn
	origCookieSave := cookieLoginSaveFn
	origCookieDelete := cookieStoreDeleteFn
	t.Cleanup(func() {
		saltKeyPassFn = origSalt
		sessionSaveFn = origSave
		transitionToCookiesFn = origTransition
		cookieLoginSaveFn = origCookieSave
		cookieStoreDeleteFn = origCookieDelete
	})

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return []byte("derived-key"), nil
	}

	var bearerSaveCalled bool
	sessionSaveFn = func(_ common.SessionStore, _ *common.Session, keypass []byte) error {
		bearerSaveCalled = true
		if string(keypass) != "derived-key" {
			t.Errorf("keypass = %q, want %q", string(keypass), "derived-key")
		}
		return nil
	}

	var transitionCalled bool
	transitionToCookiesFn = func(_ context.Context, _ *common.Session) (*account.CookieSession, error) {
		transitionCalled = true
		return nil, nil
	}

	var cookieSaveCalled bool
	cookieLoginSaveFn = func(_ common.SessionStore, _ common.SessionStore, _ *common.Session, _ *account.CookieSession, _ []byte) error {
		cookieSaveCalled = true
		return nil
	}

	cookieStoreDeleteFn = func(_ common.SessionStore) error { return nil }

	session := &common.Session{
		Auth: proton.Auth{PasswordMode: proton.OnePasswordMode},
	}

	err := deriveAndSave(context.Background(), &cli.RuntimeContext{}, session, "pass", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bearerSaveCalled {
		t.Error("Bearer sessionSaveFn was not called")
	}
	if transitionCalled {
		t.Error("TransitionToCookies should not be called when cookieAuth=false")
	}
	if cookieSaveCalled {
		t.Error("CookieLoginSave should not be called when cookieAuth=false")
	}
}

// --- Re-login auth mode overwrite tests (Task 13.1) ---

// TestDeriveAndSave_CookieAuthFalse_DeletesCookieStore verifies that when
// cookieAuth=false, the cookie store is deleted to clean up any stale cookie
// session from a previous --cookie-session login.
func TestDeriveAndSave_CookieAuthFalse_DeletesCookieStore(t *testing.T) {
	origSalt := saltKeyPassFn
	origSave := sessionSaveFn
	origCookieDelete := cookieStoreDeleteFn
	t.Cleanup(func() {
		saltKeyPassFn = origSalt
		sessionSaveFn = origSave
		cookieStoreDeleteFn = origCookieDelete
	})

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return []byte("derived-key"), nil
	}

	sessionSaveFn = func(_ common.SessionStore, _ *common.Session, _ []byte) error {
		return nil
	}

	var cookieDeleteCalled bool
	cookieStoreDeleteFn = func(_ common.SessionStore) error {
		cookieDeleteCalled = true
		return nil
	}

	session := &common.Session{
		Auth: proton.Auth{PasswordMode: proton.OnePasswordMode},
	}

	err := deriveAndSave(context.Background(), &cli.RuntimeContext{}, session, "pass", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cookieDeleteCalled {
		t.Error("cookie store Delete was not called when cookieAuth=false")
	}
}

// TestDeriveAndSave_CookieAuthTrue_OverwritesAccountStore verifies that when
// cookieAuth=true, CookieLoginSave is called which overwrites the account
// store with CookieAuth=true (and the cookie store delete is NOT called).
func TestDeriveAndSave_CookieAuthTrue_OverwritesAccountStore(t *testing.T) {
	origSalt := saltKeyPassFn
	origSave := sessionSaveFn
	origTransition := transitionToCookiesFn
	origCookieSave := cookieLoginSaveFn
	origCookieDelete := cookieStoreDeleteFn
	t.Cleanup(func() {
		saltKeyPassFn = origSalt
		sessionSaveFn = origSave
		transitionToCookiesFn = origTransition
		cookieLoginSaveFn = origCookieSave
		cookieStoreDeleteFn = origCookieDelete
	})

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return []byte("derived-key"), nil
	}

	var bearerSaveCalled bool
	sessionSaveFn = func(_ common.SessionStore, _ *common.Session, _ []byte) error {
		bearerSaveCalled = true
		return nil
	}

	transitionToCookiesFn = func(_ context.Context, _ *common.Session) (*account.CookieSession, error) {
		return &account.CookieSession{}, nil
	}

	var cookieSaveCalled bool
	cookieLoginSaveFn = func(_ common.SessionStore, _ common.SessionStore, _ *common.Session, _ *account.CookieSession, _ []byte) error {
		cookieSaveCalled = true
		return nil
	}

	var cookieDeleteCalled bool
	cookieStoreDeleteFn = func(_ common.SessionStore) error {
		cookieDeleteCalled = true
		return nil
	}

	session := &common.Session{
		Auth: proton.Auth{PasswordMode: proton.OnePasswordMode},
	}

	err := deriveAndSave(context.Background(), &cli.RuntimeContext{}, session, "pass", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cookieSaveCalled {
		t.Error("CookieLoginSave was not called when cookieAuth=true")
	}
	if bearerSaveCalled {
		t.Error("Bearer save should not be called when cookieAuth=true")
	}
	if cookieDeleteCalled {
		t.Error("cookie store Delete should not be called when cookieAuth=true")
	}
}

// TestDeriveAndSave_CookieAuthFalse_DeleteErrorIgnored verifies that a
// cookie store delete error during bearer re-login is silently ignored.
func TestDeriveAndSave_CookieAuthFalse_DeleteErrorIgnored(t *testing.T) {
	origSalt := saltKeyPassFn
	origSave := sessionSaveFn
	origCookieDelete := cookieStoreDeleteFn
	t.Cleanup(func() {
		saltKeyPassFn = origSalt
		sessionSaveFn = origSave
		cookieStoreDeleteFn = origCookieDelete
	})

	saltKeyPassFn = func(_ context.Context, _ *common.Session, _ []byte) ([]byte, error) {
		return []byte("derived-key"), nil
	}

	var bearerSaveCalled bool
	sessionSaveFn = func(_ common.SessionStore, _ *common.Session, _ []byte) error {
		bearerSaveCalled = true
		return nil
	}

	cookieStoreDeleteFn = func(_ common.SessionStore) error {
		return fmt.Errorf("cookie store not found")
	}

	session := &common.Session{
		Auth: proton.Auth{PasswordMode: proton.OnePasswordMode},
	}

	err := deriveAndSave(context.Background(), &cli.RuntimeContext{}, session, "pass", "", false)
	if err != nil {
		t.Fatalf("cookie delete error should be ignored, got: %v", err)
	}
	if !bearerSaveCalled {
		t.Error("Bearer save should still be called despite cookie delete error")
	}
}
