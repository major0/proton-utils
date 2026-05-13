package configCmd

import (
	"fmt"

	"github.com/major0/proton-utils/api/config"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

var setCmd = &cobra.Command{
	Use:   "set <selector> <value>",
	Short: "Set a config value by selector",
	Args:  cobra.ExactArgs(2),
	RunE:  runSet,
}

func init() {
	configCmd.AddCommand(setCmd)
}

func runSet(cmd *cobra.Command, args []string) error {
	sel, err := config.Parse(args[0])
	if err != nil {
		return err
	}

	// Keep the original selector string for confirmation output.
	displaySel := args[0]

	// Resolve share name → ID if needed.
	sel, err = resolveShareSelector(cmd, sel)
	if err != nil {
		return err
	}

	// Load fresh config from file (not RuntimeContext) for modification.
	configPath := cli.ConfigFilePath()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("config set: %w", err)
	}

	if err := config.Set(cfg, sel, args[1]); err != nil {
		return err
	}

	// Best-effort stale share cleanup when session is available.
	if sel.Segments[0].Name == "share" {
		cleanupStaleShares(cmd, cfg)
	}

	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("config set: %w", err)
	}

	fmt.Printf("%s=%s\n", displaySel, args[1])
	return nil
}
