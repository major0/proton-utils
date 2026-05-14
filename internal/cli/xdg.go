package cli

import "github.com/major0/proton-utils/internal/keyring"

// xdgConfigPath delegates to keyring.XDGConfigPath.
func xdgConfigPath(name string) string {
	return keyring.XDGConfigPath(name)
}
