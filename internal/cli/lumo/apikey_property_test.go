package lumoCmd

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestGenerateAPIKey_Validity_Property verifies that generated API keys are
// exactly 64 hex characters (encoding 32 random bytes).
//
// Feature: lumo-serve, Property 6: API key generation validity
//
// **Validates: Requirements 2.1**
func TestGenerateAPIKey_Validity_Property(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		key, err := GenerateAPIKey(dir)
		if err != nil {
			rt.Fatalf("GenerateAPIKey: %v", err)
		}
		if len(key) != 64 {
			rt.Fatalf("key length = %d, want 64", len(key))
		}
		if _, err := hex.DecodeString(key); err != nil {
			rt.Fatalf("key is not valid hex: %v", err)
		}
	})
}

// TestAPIKey_PersistenceRoundTrip_Property verifies that writing an arbitrary
// valid hex key string to a temp dir then reading it back returns the
// identical string.
//
// Feature: lumo-serve, Property 7: API key persistence round-trip
//
// **Validates: Requirements 2.2**
func TestAPIKey_PersistenceRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()

		// Generate a key, then load it back.
		key, err := GenerateAPIKey(dir)
		if err != nil {
			rt.Fatalf("GenerateAPIKey: %v", err)
		}

		loaded, err := LoadOrGenerateAPIKey(dir)
		if err != nil {
			rt.Fatalf("LoadOrGenerateAPIKey: %v", err)
		}
		if loaded != key {
			rt.Fatalf("round-trip mismatch: wrote %q, read %q", key, loaded)
		}

		// Also verify the file contents directly.
		data, err := os.ReadFile(filepath.Join(dir, apiKeyFile)) //nolint:gosec // test code
		if err != nil {
			rt.Fatalf("ReadFile: %v", err)
		}
		if strings.TrimSpace(string(data)) != key {
			rt.Fatalf("file content mismatch: %q vs %q", strings.TrimSpace(string(data)), key)
		}
	})
}
