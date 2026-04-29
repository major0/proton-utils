package shareCmd

import (
	"context"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var cacheFlags struct {
	enableDirent    bool
	disableDirent   bool
	enableMetadata  bool
	disableMetadata bool
	enableOnDisk    bool
	disableOnDisk   bool
}

var shareCacheCmd = &cobra.Command{
	Use:   "cache <share-name>",
	Short: "View or modify per-share cache settings",
	Long:  "View or modify the dirent, metadata, and on-disk cache settings for a share",
	Args:  cobra.ExactArgs(1),
	RunE:  runShareCache,
}

func init() {
	shareCmd.AddCommand(shareCacheCmd)
	f := shareCacheCmd.Flags()
	f.BoolVar(&cacheFlags.enableDirent, "enable-dirent", false, "Enable dirent name cache")
	f.BoolVar(&cacheFlags.disableDirent, "disable-dirent", false, "Disable dirent name cache")
	f.BoolVar(&cacheFlags.enableMetadata, "enable-metadata", false, "Enable metadata cache")
	f.BoolVar(&cacheFlags.disableMetadata, "disable-metadata", false, "Disable metadata cache")
	f.BoolVar(&cacheFlags.enableOnDisk, "enable-on-disk", false, "Enable on-disk block cache")
	f.BoolVar(&cacheFlags.disableOnDisk, "disable-on-disk", false, "Disable on-disk block cache")
}

// prohibitedShareType returns true for any share type that is not
// ShareTypeStandard. Only user-created shared folders may have caching.
func prohibitedShareType(st proton.ShareType) bool {
	return st != proton.ShareTypeStandard
}

func runShareCache(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Resolve the share to check its type.
	rc := cli.GetContext(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
	defer cancel()

	session, err := restoreSessionFn(ctx)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return err
	}

	resolved, err := resolveShareFn(ctx, dc, name)
	if err != nil {
		return fmt.Errorf("share cache: %s: share not found", name)
	}

	if prohibitedShareType(resolved.Metadata().Type) {
		return fmt.Errorf("share cache: %s: caching only allowed on shared folders (type %q)",
			name, drive.FormatShareType(resolved.Metadata().Type))
	}

	cfg := rc.Config
	if cfg == nil {
		cfg = api.DefaultConfig()
	}

	hasToggle := cacheFlags.enableDirent || cacheFlags.disableDirent ||
		cacheFlags.enableMetadata || cacheFlags.disableMetadata ||
		cacheFlags.enableOnDisk || cacheFlags.disableOnDisk

	if !hasToggle {
		// Show current state.
		sc := cfg.Shares[name]
		printCacheState(name, sc)
		return nil
	}

	// Update toggles.
	sc := cfg.Shares[name] // zero value if absent

	if cacheFlags.enableDirent {
		sc.DirentCacheEnabled = true
	}
	if cacheFlags.disableDirent {
		sc.DirentCacheEnabled = false
	}
	if cacheFlags.enableMetadata {
		sc.MetadataCacheEnabled = true
	}
	if cacheFlags.disableMetadata {
		sc.MetadataCacheEnabled = false
	}
	if cacheFlags.enableOnDisk {
		sc.DiskCacheEnabled = true
	}
	if cacheFlags.disableOnDisk {
		sc.DiskCacheEnabled = false
	}

	if cfg.Shares == nil {
		cfg.Shares = make(map[string]api.ShareConfig)
	}
	cfg.Shares[name] = sc

	// Save config.
	configPath := cli.ConfigFilePath()
	if err := api.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("share cache: save config: %w", err)
	}

	printCacheState(name, sc)
	return nil
}

func printCacheState(name string, sc api.ShareConfig) {
	fmt.Printf("Share: %s\n", name)
	fmt.Printf("  dirent:   %s\n", boolState(sc.DirentCacheEnabled))
	fmt.Printf("  metadata: %s\n", boolState(sc.MetadataCacheEnabled))
	fmt.Printf("  on-disk:  %s\n", boolState(sc.DiskCacheEnabled))
}

func boolState(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}
