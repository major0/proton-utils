package cli

import (
	"path/filepath"
	"testing"
)

func TestXdgConfigPath(t *testing.T) {
	tests := []struct {
		name       string
		xdgConfig  string // value for XDG_CONFIG_HOME ("" = unset)
		setXDG     bool   // whether to set the env var at all
		unsetHome  bool   // whether to unset HOME
		inputName  string
		wantSuffix string // expected suffix of the returned path
	}{
		{
			name:       "XDG_CONFIG_HOME set",
			xdgConfig:  "/custom/config",
			setXDG:     true,
			inputName:  "config.yaml",
			wantSuffix: filepath.Join("/custom/config", appName, "config.yaml"),
		},
		{
			name:       "XDG_CONFIG_HOME unset falls back to home",
			xdgConfig:  "",
			setXDG:     false,
			inputName:  "sessions.db",
			wantSuffix: filepath.Join(".config", appName, "sessions.db"),
		},
		{
			name:       "XDG_CONFIG_HOME empty string falls back to home",
			xdgConfig:  "",
			setXDG:     true,
			inputName:  "sessions.db",
			wantSuffix: filepath.Join(".config", appName, "sessions.db"),
		},
		{
			name:       "HOME unset falls back to cwd",
			xdgConfig:  "",
			setXDG:     true,
			unsetHome:  true,
			inputName:  "config.yaml",
			wantSuffix: filepath.Join(appName, "config.yaml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setXDG {
				t.Setenv("XDG_CONFIG_HOME", tt.xdgConfig)
			} else {
				// Ensure it's unset.
				t.Setenv("XDG_CONFIG_HOME", "")
			}

			if tt.unsetHome {
				t.Setenv("HOME", "")
			}

			got := xdgConfigPath(tt.inputName)

			switch {
			case tt.unsetHome:
				// Last-resort fallback: just appName/name.
				if got != tt.wantSuffix {
					t.Errorf("got %q, want %q", got, tt.wantSuffix)
				}
			case tt.xdgConfig != "":
				// Exact match when XDG is explicitly set.
				if got != tt.wantSuffix {
					t.Errorf("got %q, want %q", got, tt.wantSuffix)
				}
			default:
				// When falling back to home, just check the suffix.
				if !hasSuffix(got, tt.wantSuffix) {
					t.Errorf("got %q, want suffix %q", got, tt.wantSuffix)
				}
			}
		})
	}
}

// hasSuffix checks if path ends with the given suffix path components.
func hasSuffix(path, suffix string) bool {
	return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
}
