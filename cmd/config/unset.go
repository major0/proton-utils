package configCmd

import (
	"fmt"

	"github.com/major0/proton-cli/api/config"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var unsetFlags struct {
	all bool
}

var unsetCmd = &cobra.Command{
	Use:   "unset <selector>",
	Short: "Unset a config value (revert to default)",
	Args:  cobra.ExactArgs(1),
	RunE:  runUnset,
}

func init() {
	configCmd.AddCommand(unsetCmd)
	unsetCmd.Flags().BoolVar(&unsetFlags.all, "all", false, "Remove all entries matching the given prefix")
}

func runUnset(cmd *cobra.Command, args []string) error {
	configPath := cli.ConfigFilePath()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("config unset: %w", err)
	}

	if unsetFlags.all {
		return runUnsetAll(cmd, cfg, configPath, args[0])
	}

	sel, err := config.Parse(args[0])
	if err != nil {
		return err
	}

	// Keep the original selector string for confirmation output.
	displaySel := args[0]

	// Require a fully-qualified leaf selector (at least 2 segments).
	if len(sel.Segments) < 2 {
		return fmt.Errorf("config unset: selector %q is not a leaf (use --all for prefix removal)", sel.String())
	}

	// Resolve share name → ID if needed.
	sel, err = resolveShareSelector(cmd, sel)
	if err != nil {
		return err
	}

	if err := config.UnsetField(cfg, sel); err != nil {
		return err
	}

	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("config unset: %w", err)
	}

	fmt.Println(displaySel)
	return nil
}

// runUnsetAll removes all config entries whose selectors match the given prefix.
func runUnsetAll(_ *cobra.Command, cfg *config.Config, configPath, prefix string) error {
	entries := config.List(cfg)
	var removed []string

	for _, entry := range entries {
		if config.MatchPrefix(entry.Selector, prefix) {
			sel, err := config.Parse(entry.Selector)
			if err != nil {
				continue
			}
			if err := config.UnsetField(cfg, sel); err != nil {
				continue
			}
			removed = append(removed, entry.Selector)
		}
	}

	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("config unset: %w", err)
	}

	for _, s := range removed {
		fmt.Println(s)
	}
	return nil
}
