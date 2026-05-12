package lumoCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-cli/api/lumo"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/spf13/cobra"
)

var lumoCmd = &cobra.Command{
	Use:   "lumo",
	Short: "Proton Lumo AI assistant",
	Long:  "Proton Lumo AI assistant",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if p := cmd.Root(); p != nil && p.PersistentPreRunE != nil {
			if err := p.PersistentPreRunE(p, args); err != nil {
				return err
			}
		}
		cli.SetServiceCmd(cmd, "lumo")
		return nil
	},
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

func init() {
	cli.AddCommand(lumoCmd)
}

// restoreClient restores the session and creates a Lumo client.
// Lumo requires cookie-based authentication; Bearer sessions are rejected.
func restoreClient(cmd *cobra.Command) (*lumo.Client, error) {
	rc := cli.GetContext(cmd)

	// Lumo requires cookie auth. Check the account config before
	// attempting the (expensive) session restore + fork.
	acctCfg, err := rc.AccountStore.Load()
	if err != nil {
		return nil, fmt.Errorf("no active session (run 'proton account login' first): %w", err)
	}
	if !acctCfg.CookieAuth {
		return nil, fmt.Errorf("lumo requires cookie-based authentication; current session uses Bearer auth (re-login with 'proton account login --cookie-session')")
	}

	session, err := cli.SetupSession(cmd.Context(), cmd)
	if err != nil {
		return nil, fmt.Errorf("no active session (run 'proton account login' first): %w", err)
	}
	return lumo.NewClient(session), nil
}

// AddCommand registers a subcommand under the lumo command group.
func AddCommand(cmd *cobra.Command) {
	lumoCmd.AddCommand(cmd)
}

// resolveSpaceAndDEK loads a space by ID and derives its decryption key.
func resolveSpaceAndDEK(ctx context.Context, client *lumo.Client, spaceID string) (*lumo.Space, []byte, error) {
	space, err := client.GetSpace(ctx, spaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading space: %w", err)
	}
	dek, err := client.DeriveSpaceDEK(ctx, space)
	if err != nil {
		return nil, nil, fmt.Errorf("deriving decryption key: %w", err)
	}
	return space, dek, nil
}
