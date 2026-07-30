package accountCmd

import (
	"context"
	"errors"
	"log/slog"

	common "github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/account"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

// logoutRestoreFn restores the real account (parent) session by loading the
// "account" store slot directly, bypassing the service-fork path entirely.
// rc.ProtonOpts are already account-host options (set by SetServiceCmd
// "account"), and ReadySession → SessionRestore loads the "account" slot
// without forking or overwriting it. It is a package var so tests can inject a
// session/error without a network round-trip (mirrors logoutCookieDeleteFn).
var logoutRestoreFn = func(ctx context.Context, rc *cli.RuntimeContext) (*common.Session, error) {
	return account.ReadySession(ctx, rc.ProtonOpts, rc.AccountStore, rc.CookieStore, nil)
}

// logoutRevokeFn revokes the restored account session. It is a package var
// wrapping account.SessionRevoke (the direct call from design Change 1/2) so
// tests can drive the revoke outcome — success or a 9101-style failure under
// the force gate — without a live client, mirroring logoutRestoreFn. Its
// production behavior is identical to calling account.SessionRevoke directly.
var logoutRevokeFn = account.SessionRevoke

// logoutCookieDeleteFn deletes the cookie store entry.
// It is a variable so tests can replace it.
var logoutCookieDeleteFn = func(store common.SessionStore) error {
	return store.Delete()
}

// accountPurger removes every local session entry for the account (account,
// drive, lumo, cookie, wildcard). It is consumed via a local interface
// (accept-interfaces) so api.SessionStore and its mocks stay unchanged; the
// concrete keyring.SessionIndex satisfies it with DeleteAllSessions.
type accountPurger interface {
	DeleteAllSessions() error
}

var authLogoutForce = false
var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout of Proton",
	Long:  `Logout of Proton`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		rc := cli.GetContext(cmd)
		ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
		defer cancel()

		// Restore the real account (parent) session without forking or
		// overwriting it (Req 2.1). Not-logged-in is tolerated so cleanup can
		// still run (Req 3.3); any other restore error aborts unless --force.
		session, err := logoutRestoreFn(ctx, rc)
		if err != nil && !errors.Is(err, common.ErrNotLoggedIn) && !authLogoutForce {
			return err
		}

		// Revoke via the self-delete endpoint before any local cleanup. A
		// no-force revoke failure returns here, leaving local state intact
		// (Req 2.6); with --force (or success) cleanup always runs.
		if err := logoutRevokeFn(ctx, session, authLogoutForce); err != nil {
			return err
		}

		// Clean up the cookie store. Log a warning on failure — don't fail the
		// logout (Req 3.4).
		if err := logoutCookieDeleteFn(rc.CookieStore); err != nil {
			slog.Warn("logout: cookie store delete failed", "error", err)
		}

		// Purge every local session entry for the account (Req 2.4). Residual
		// local-delete errors are non-fatal warnings once the server session
		// is revoked, generalizing the non-fatal cookie rule (Req 3.4).
		if purger, ok := rc.AccountStore.(accountPurger); ok {
			if err := purger.DeleteAllSessions(); err != nil {
				slog.Warn("logout: local session cleanup incomplete", "error", err)
			}
		}

		return nil
	},
}

func init() {
	accountCmd.AddCommand(authLogoutCmd)
	cli.BoolFlagP(authLogoutCmd.Flags(), &authLogoutForce, "force", "f", false, "Force logout of Proton")
}
