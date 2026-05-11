package configCmd

import (
	"fmt"

	"github.com/major0/proton-cli/api/config"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:   "get <selector>",
	Short: "Get a config value by selector",
	Args:  cobra.ExactArgs(1),
	RunE:  runGet,
}

func init() {
	configCmd.AddCommand(getCmd)
}

func runGet(cmd *cobra.Command, args []string) error {
	rc := cli.GetContext(cmd)
	sel, err := config.Parse(args[0])
	if err != nil {
		return err
	}

	cfg := rc.Config
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	value, err := config.Get(cfg, sel)
	if err != nil {
		return err
	}

	fmt.Println(value)
	return nil
}
