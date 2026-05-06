package configCmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/major0/proton-cli/api/config"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show [pattern]",
	Short: "Show all config values with source annotations",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runShow,
}

func init() {
	configCmd.AddCommand(showCmd)
}

func runShow(cmd *cobra.Command, args []string) error {
	rc := cli.GetContext(cmd)
	cfg := rc.Config
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	entries := config.Show(cfg)

	var pattern string
	if len(args) > 0 {
		pattern = args[0]
	}

	// Attempt to build a share ID→name map for display transposition.
	idToName := resolveShareNames(cmd, cfg)

	for _, entry := range entries {
		selector := entry.Selector
		if pattern != "" && !config.MatchPattern(selector, pattern) {
			continue
		}

		// Transpose share[id=X] → share[name=Y] if possible.
		if idToName != nil && strings.HasPrefix(selector, "share[id=") {
			selector = transposeShareSelector(selector, idToName)
		}

		fmt.Printf("%s=%s (%s)\n", selector, entry.Value, entry.Source.String())
	}
	return nil
}

// resolveShareNames attempts to build a map of share ID → share name.
// Returns nil if no share entries exist in the config (avoids unnecessary
// session restore) or if no authenticated session is available (best-effort).
func resolveShareNames(cmd *cobra.Command, cfg *config.Config) map[string]string {
	if len(cfg.Shares) == 0 {
		return nil
	}

	cli.SetServiceCmd(cmd, "drive")
	ctx := context.Background()
	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return nil
	}
	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return nil
	}
	shares, err := listSharesFn(ctx, dc)
	if err != nil {
		return nil
	}

	idToName := make(map[string]string, len(shares))
	for _, s := range shares {
		name, err := s.GetName(ctx)
		if err != nil {
			continue
		}
		idToName[s.Metadata().ShareID] = name
	}
	return idToName
}

// transposeShareSelector replaces share[id=X] with share[name=Y] in a
// selector string if the ID is found in the map.
func transposeShareSelector(selector string, idToName map[string]string) string {
	// Extract the ID from share[id=X].field
	const prefix = "share[id="
	if !strings.HasPrefix(selector, prefix) {
		return selector
	}
	rest := selector[len(prefix):]
	closeBracket := strings.IndexByte(rest, ']')
	if closeBracket < 0 {
		return selector
	}
	id := rest[:closeBracket]
	suffix := rest[closeBracket+1:] // e.g., ".memory_cache"

	name, ok := idToName[id]
	if !ok {
		return selector
	}
	return fmt.Sprintf("share[name=%s]%s", name, suffix)
}
