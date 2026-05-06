package configCmd

import (
	"fmt"

	"github.com/major0/proton-cli/api/config"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "list [pattern]",
	Aliases: []string{"ls"},
	Short:   "List explicitly-set config values",
	Args:    cobra.MaximumNArgs(1),
	RunE:    runList,
}

func init() {
	configCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	rc := cli.GetContext(cmd)
	cfg := rc.Config
	if cfg == nil {
		return nil // no config file → no output
	}

	entries := config.List(cfg)

	var pattern string
	if len(args) > 0 {
		pattern = args[0]
	}

	for _, entry := range entries {
		if pattern != "" && !config.MatchPattern(entry.Selector, pattern) {
			continue
		}
		fmt.Printf("%s=%s\n", entry.Selector, entry.Value)
	}
	return nil
}
