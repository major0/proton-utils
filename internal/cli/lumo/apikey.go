package lumoCmd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const apiKeyFile = "lumo-api-key" //nolint:gosec // not a credential

// LoadOrGenerateAPIKey reads the persisted key from dir. If none exists,
// generates a new 32-byte random token (hex-encoded, 64 chars), persists
// it with mode 0600, and returns it.
func LoadOrGenerateAPIKey(dir string) (string, error) {
	path := filepath.Join(dir, apiKeyFile)
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from trusted dir
	if err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			return key, nil
		}
	}
	return GenerateAPIKey(dir)
}

// GenerateAPIKey generates a new random API key and persists it,
// replacing any existing key. Returns the hex-encoded key (64 chars).
func GenerateAPIKey(dir string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating API key: %w", err)
	}
	key := hex.EncodeToString(buf)

	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating key directory: %w", err)
	}

	path := filepath.Join(dir, apiKeyFile)
	if err := os.WriteFile(path, []byte(key+"\n"), 0600); err != nil {
		return "", fmt.Errorf("writing API key: %w", err)
	}
	return key, nil
}
