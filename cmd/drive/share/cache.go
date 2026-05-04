package shareCmd

import (
	"context"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/config"
	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var cacheFlags struct {
	memoryCache string
	diskCache   string
}

var shareCacheCmd = &cobra.Command{
	Use:   "cache <share-name>",
	Short: "View or modify per-share cache settings",
	Long:  "View or modify the memory and disk cache settings for a share",
	Args:  cobra.ExactArgs(1),
	RunE:  runShareCache,
}

func init() {
	shareCmd.AddCommand(shareCacheCmd)
	f := shareCacheCmd.Flags()
	f.StringVar(&cacheFlags.memoryCache, "memory-cache", "", "Set memory cache level (disabled, linkname, metadata)")
	f.StringVar(&cacheFlags.diskCache, "disk-cache", "", "Set disk cache level (disabled, objectstore)")
}

// prohibitedShareType returns true for any share type that is not
// ShareTypeStandard. Only user-created shared folders may have caching.
func prohibitedShareType(st proton.ShareType) bool {
	return st != proton.ShareTypeStandard
}

func runShareCache(cmd *cobra.Command, args []string) error {
	name := args[0]

	rc := cli.GetContext(cmd)
	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, cmd, session)
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
		cfg = config.DefaultConfig()
	}

	hasToggle := cacheFlags.memoryCache != "" || cacheFlags.diskCache != ""

	if !hasToggle {
		// Show current state.
		sc := cfg.Shares[name]
		printCacheState(name, sc)
		return nil
	}

	// Update settings.
	sc := cfg.Shares[name] // zero value if absent

	if cacheFlags.memoryCache != "" {
		switch cacheFlags.memoryCache {
		case "disabled":
			sc.MemoryCache = api.CacheDisabled
		case "linkname":
			sc.MemoryCache = api.CacheLinkName
		case "metadata":
			sc.MemoryCache = api.CacheMetadata
		default:
			return fmt.Errorf("share cache: invalid memory-cache value %q (use disabled, linkname, metadata)", cacheFlags.memoryCache)
		}
	}

	if cacheFlags.diskCache != "" {
		switch cacheFlags.diskCache {
		case "disabled":
			sc.DiskCache = api.DiskCacheDisabled
		case "objectstore":
			sc.DiskCache = api.DiskCacheObjectStore
		default:
			return fmt.Errorf("share cache: invalid disk-cache value %q (use disabled, objectstore)", cacheFlags.diskCache)
		}
	}

	if cfg.Shares == nil {
		cfg.Shares = make(map[string]api.ShareConfig)
	}
	cfg.Shares[name] = sc

	// Save config.
	configPath := cli.ConfigFilePath()
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("share cache: save config: %w", err)
	}

	printCacheState(name, sc)
	return nil
}

func printCacheState(name string, sc api.ShareConfig) {
	fmt.Printf("Share: %s\n", name)
	fmt.Printf("  memory:   %s\n", sc.MemoryCache)
	fmt.Printf("  disk:     %s\n", sc.DiskCache)
}
