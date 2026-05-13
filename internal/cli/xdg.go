package cli

import (
	"os"
	"path/filepath"
)

const appName = "proton-utils"

// xdgConfigPath returns a path under $XDG_CONFIG_HOME/proton-utils/.
// Defaults to ~/.config/proton-utils/ if XDG_CONFIG_HOME is unset.
func xdgConfigPath(name string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// Last resort: use current directory.
			return filepath.Join(appName, name)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appName, name)
}
