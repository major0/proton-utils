package accountCmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ProtonMail/go-proton-api"
	common "github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/account"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/major0/proton-cli/internal"
	"github.com/spf13/cobra"
)

// userPromptFn is the function used to prompt the user for input.
// It is a variable so tests can replace it without reading stdin.
var userPromptFn = internal.UserPrompt

var authLoginParams = struct {
	username      string
	password      string
	mboxpass      string
	twoFA         string
	noBrowser     bool
	cookieSession bool
}{}

// hasCaptchaMethod reports whether "captcha" is among the HV methods.
func hasCaptchaMethod(methods []string) bool {
	for _, m := range methods {
		if m == "captcha" {
			return true
		}
	}
	return false
}

var authLoginCmd = &cobra.Command{
	Use:   "login [options]",
	Short: "login to Proton",
	Long:  `login to Proton`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		rc := cli.GetContext(cmd)
		username, password, err := promptCredentials()
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
		defer cancel()

		if authLoginParams.cookieSession {
			return cookieLogin(ctx, username, password, authLoginParams.mboxpass)
		}

		session, err := attemptLogin(ctx, username, password)
		if err != nil {
			return err
		}

		session.AddAuthHandler(common.NewAuthHandler(cli.SessionStoreVar, session))
		session.AddDeauthHandler(common.NewDeauthHandler())

		if err := handleTwoFA(ctx, session); err != nil {
			return err
		}

		if err := deriveAndSave(ctx, session, password, authLoginParams.mboxpass, false); err != nil {
			return err
		}

		// Verbose login diagnostics.
		logLoginDiagnostics()

		return nil
	},
}

// promptCredentials prompts for username/password if not provided via flags.
func promptCredentials() (username, password string, err error) {
	username = authLoginParams.username
	password = authLoginParams.password

	if username == "" {
		username, err = userPromptFn("Username", false)
		if err != nil {
			return "", "", err
		}
	}

	if password == "" {
		password, err = userPromptFn("Password", true)
		if err != nil {
			return "", "", err
		}
	}

	return username, password, nil
}

// sessionFromLoginFn is the function used to create a session from login credentials.
// It is a variable so tests can replace it without making real API calls.
var sessionFromLoginFn = common.SessionFromLogin

// sessionRetryWithHVFn is the function used to retry login after HV.
// It is a variable so tests can replace it without making real API calls.
var sessionRetryWithHVFn = common.SessionRetryWithHV

// attemptLogin performs the initial login, handling HV/CAPTCHA if needed.
func attemptLogin(ctx context.Context, username, password string) (*common.Session, error) {
	session, err := sessionFromLoginFn(ctx, cli.ProtonOpts, username, password, nil, nil)
	if err != nil {
		// Check for HV error (code 9001).
		apiErr := new(proton.APIError)
		if !errors.As(err, &apiErr) || !apiErr.IsHVError() {
			return nil, err
		}

		hv, hvErr := apiErr.GetHVDetails()
		if hvErr != nil {
			return nil, fmt.Errorf("extracting HV details: %w", hvErr)
		}

		if !hasCaptchaMethod(hv.Methods) {
			return nil, fmt.Errorf("unsupported HV methods: %v", hv.Methods)
		}

		solvedToken, solveErr := SolveCaptcha(hv, authLoginParams.noBrowser)
		if solveErr != nil {
			return nil, fmt.Errorf("CAPTCHA: %w", solveErr)
		}

		hv.Token = solvedToken
		fmt.Println("Authenticating ...")

		if err := sessionRetryWithHVFn(ctx, session, username, password, hv); err != nil {
			return nil, err
		}
	}

	return session, nil
}

// handleTwoFA prompts for and submits the 2FA code if TOTP is enabled.
func handleTwoFA(ctx context.Context, session *common.Session) error {
	if session.Auth.TwoFA.Enabled&proton.HasTOTP == 0 {
		return nil
	}

	twoFA := authLoginParams.twoFA
	if twoFA == "" {
		var err error
		twoFA, err = userPromptFn("2FA code", false)
		if err != nil {
			return err
		}
	}

	return session.Client.Auth2FA(ctx, proton.Auth2FAReq{
		TwoFactorCode: twoFA,
	})
}

// deriveAndSave derives the key passphrase and saves the session.
// selectKeyPassword determines which password to use for key derivation
// based on the password mode. Returns the password bytes to salt.
func selectKeyPassword(passwordMode proton.PasswordMode, password, mboxpass string) ([]byte, error) {
	if passwordMode == proton.TwoPasswordMode {
		if mboxpass == "" {
			var err error
			mboxpass, err = userPromptFn("Mailbox password", true)
			if err != nil {
				return nil, err
			}
		}
		return []byte(mboxpass), nil
	}
	return []byte(password), nil
}

// saltKeyPassFn is the function used to salt the key password.
// It is a variable so tests can replace it without making real API calls.
var saltKeyPassFn = func(ctx context.Context, session *common.Session, password []byte) ([]byte, error) {
	return common.SaltKeyPass(ctx, session.Client, password)
}

// sessionSaveFn is the function used to save the session.
// It is a variable so tests can replace it without real persistence.
var sessionSaveFn = func(session *common.Session, keypass []byte) error {
	return common.SessionSave(cli.SessionStoreVar, session, keypass)
}

// transitionToCookiesFn is the function used to transition a Bearer session to cookie auth.
// It is a variable so tests can replace it without making real API calls.
var transitionToCookiesFn = common.TransitionToCookies

// cookieLoginSaveFn is the function used to save a cookie session after login.
// It is a variable so tests can replace it without real persistence.
var cookieLoginSaveFn = func(session *common.Session, cookieSess *common.CookieSession, keypass []byte) error {
	return common.CookieLoginSave(cli.CookieStoreVar, cli.AccountStoreVar, session, cookieSess, keypass)
}

// cookieStoreDeleteFn deletes the cookie store entry. Used during re-login
// with cookieAuth=false to clean up stale cookie sessions.
// It is a variable so tests can replace it.
var cookieStoreDeleteFn = func() error {
	return cli.CookieStoreVar.Delete()
}

func deriveAndSave(ctx context.Context, session *common.Session, password, mboxpass string, cookieAuth bool) error {
	passBytes, err := selectKeyPassword(session.Auth.PasswordMode, password, mboxpass)
	if err != nil {
		return err
	}

	keypass, err := saltKeyPassFn(ctx, session, passBytes)
	if err != nil {
		return err
	}

	if cookieAuth {
		// Set BaseURL and AppVersion for the account service — the session
		// from SessionFromLogin uses proton.DefaultHostURL (mail.proton.me)
		// and has no AppVersion set. TransitionToCookies needs the account
		// host and a valid app version for the auth/cookies POST.
		acctSvc, _ := common.LookupService("account")
		session.BaseURL = acctSvc.Host
		session.AppVersion = acctSvc.AppVersion("")

		cookieSess, err := transitionToCookiesFn(ctx, session)
		if err != nil {
			return fmt.Errorf("cookie transition: %w", err)
		}
		return cookieLoginSaveFn(session, cookieSess, keypass)
	}

	// When switching from cookie→bearer, clean up any stale cookie session.
	// Ignore errors — the cookie store may not exist if this is a fresh login.
	_ = cookieStoreDeleteFn()

	return sessionSaveFn(session, keypass)
}

// createAnonSessionFn is the function used to create an anonymous session.
// It is a variable so tests can replace it.
var createAnonSessionFn = common.CreateAnonSession

// cookieSRPAuthFn is the function used to perform SRP authentication within a cookie session.
// It is a variable so tests can replace it without making real API calls.
var cookieSRPAuthFn = common.CookieSRPAuth

// cookieTwoFAFn is the function used to submit a TOTP 2FA code within a cookie session.
// It is a variable so tests can replace it without making real API calls.
var cookieTwoFAFn = common.CookieTwoFA

// cookieLogin performs the browser-matching cookie login flow:
// 1. Create anonymous session on account.proton.me
// 2. Build temp Session from anon response, transition to cookies
// 3. SRP auth within cookie session
// 4. 2FA if needed
// 5. Account ops (GetUser, GetAddresses, keys/salts) via cookie session
// 6. Key derivation and save
func cookieLogin(ctx context.Context, username, password, mboxpass string) error {
	// Step 1: Create anonymous session on account.proton.me.
	anon, jar, err := createAnonSessionFn(ctx)
	if err != nil {
		return fmt.Errorf("cookie login: %w", err)
	}

	slog.Debug("cookieLogin: anonymous session created", "uid", anon.UID)

	// Step 2: Build a temporary Session for TransitionToCookies.
	// TransitionToCookies needs a Session with Bearer tokens and a cookie jar.
	acctSvc, _ := common.LookupService("account")
	tempSession := &common.Session{
		Auth: proton.Auth{
			UID:          anon.UID,
			AccessToken:  anon.AccessToken,
			RefreshToken: anon.RefreshToken,
		},
		BaseURL:    acctSvc.Host,
		AppVersion: acctSvc.AppVersion(""),
		UserAgent:  cli.UserAgent,
	}
	tempSession.SetCookieJar(jar)

	// Step 3: Transition anonymous session to cookies.
	// Bearer tokens are now INVALID. All subsequent calls use cookieSess.DoJSON.
	cookieSess, err := transitionToCookiesFn(ctx, tempSession)
	if err != nil {
		return fmt.Errorf("cookie login: transition: %w", err)
	}

	// Step 4: SRP login within cookie session.
	auth, err := cookieSRPAuthFn(ctx, cookieSess, username, []byte(password))
	if err != nil {
		// Check for HV error (code 9001).
		var apiErr *common.Error
		if !errors.As(err, &apiErr) || !apiErr.IsHVError() {
			return fmt.Errorf("cookie login: SRP: %w", err)
		}

		hv, hvErr := apiErr.GetHVDetails()
		if hvErr != nil {
			return fmt.Errorf("cookie login: extracting HV details: %w", hvErr)
		}

		if !hasCaptchaMethod(hv.Methods) {
			return fmt.Errorf("cookie login: unsupported HV methods: %v", hv.Methods)
		}

		solvedToken, solveErr := SolveCaptcha(hv, authLoginParams.noBrowser)
		if solveErr != nil {
			return fmt.Errorf("cookie login: CAPTCHA: %w", solveErr)
		}

		hv.Token = solvedToken
		fmt.Println("Authenticating ...")

		// Set HV details on the cookie session for the retry.
		cookieSess.HVDetails = hv
		auth, err = cookieSRPAuthFn(ctx, cookieSess, username, []byte(password))
		cookieSess.HVDetails = nil // Clear after use.
		if err != nil {
			return fmt.Errorf("cookie login: SRP retry: %w", err)
		}
	}

	// Step 5: 2FA if needed.
	if auth.TwoFA.Enabled&proton.HasTOTP != 0 {
		code := authLoginParams.twoFA
		if code == "" {
			code, err = userPromptFn("2FA code", false)
			if err != nil {
				return fmt.Errorf("cookie login: 2FA prompt: %w", err)
			}
		}
		if err := cookieTwoFAFn(ctx, cookieSess, code); err != nil {
			return fmt.Errorf("cookie login: 2FA: %w", err)
		}
	}

	// Step 6: Account operations via cookie session.
	var userResp struct{ User proton.User }
	if err := cookieSess.DoJSON(ctx, "GET", "/core/v4/users", nil, &userResp); err != nil {
		return fmt.Errorf("cookie login: get user: %w", err)
	}

	var addrResp struct{ Addresses []proton.Address }
	if err := cookieSess.DoJSON(ctx, "GET", "/core/v4/addresses", nil, &addrResp); err != nil {
		return fmt.Errorf("cookie login: get addresses: %w", err)
	}

	// Populate account cache with fresh data from login.
	account.PopulateAccountCache(cookieSess.UID, userResp.User, addrResp.Addresses)

	var saltsResp struct{ KeySalts []proton.Salt }
	if err := cookieSess.DoJSON(ctx, "GET", "/core/v4/keys/salts", nil, &saltsResp); err != nil {
		return fmt.Errorf("cookie login: get salts: %w", err)
	}

	// Step 7: Key derivation.
	passBytes, err := selectKeyPassword(auth.PasswordMode, password, mboxpass)
	if err != nil {
		return err
	}

	saltedKeypass, err := proton.Salts(saltsResp.KeySalts).SaltForKey(passBytes, userResp.User.Keys.Primary().ID)
	if err != nil {
		return fmt.Errorf("cookie login: salt key: %w", err)
	}

	// Build a Session for Unlock and CookieLoginSave.
	session := &common.Session{
		Auth: *auth,
	}
	session.SetUser(userResp.User)
	if err := session.Unlock(saltedKeypass, addrResp.Addresses); err != nil {
		return fmt.Errorf("cookie login: unlock: %w", err)
	}

	if err := cookieLoginSaveFn(session, cookieSess, saltedKeypass); err != nil {
		return err
	}

	logLoginDiagnostics()
	return nil
}

// logLoginDiagnostics prints session diagnostic info when verbose mode is enabled.
// Logs token age, LastRefresh timestamp, expiry estimate, and service config.
func logLoginDiagnostics() {
	svc, err := common.LookupService("account")
	if err != nil {
		return
	}

	slog.Info("login.diagnostics",
		"service", svc.Name,
		"host", svc.Host,
		"clientID", svc.ClientID,
		"appVersion", svc.AppVersion(common.DefaultVersion),
		"tokenLifetime", common.TokenExpireAge,
		"refreshThreshold", common.ProactiveRefreshAge,
		"lastRefresh", time.Now().Truncate(time.Second),
		"expiresAt", time.Now().Add(common.TokenExpireAge).Truncate(time.Second),
	)
}

func init() {
	accountCmd.AddCommand(authLoginCmd)
	authLoginCmd.Flags().StringVarP(&authLoginParams.username, "username", "u", "", "Proton username")
	authLoginCmd.Flags().StringVarP(&authLoginParams.password, "password", "p", "", "Proton password")
	authLoginCmd.Flags().StringVarP(&authLoginParams.mboxpass, "mboxpass", "m", "", "Required of 2 password mode is enabled.")
	authLoginCmd.Flags().StringVarP(&authLoginParams.twoFA, "2fa", "2", "", "2FA code")
	cli.BoolFlag(authLoginCmd.Flags(), &authLoginParams.noBrowser, "no-browser", false, "Do not open browser for CAPTCHA; print URL and prompt for token")
	cli.BoolFlag(authLoginCmd.Flags(), &authLoginParams.cookieSession, "cookie-session", false, "Use cookie-based auth instead of Bearer tokens")
}
