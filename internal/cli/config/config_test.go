package configCmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/config"
	"github.com/major0/proton-utils/api/drive"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

// saveAndRestore saves the current function variables and restores them
// after the test. It also sets up a RuntimeContext on all config commands.
func saveAndRestore(t *testing.T) {
	t.Helper()
	origSetup := setupSessionFn
	origNewClient := newDriveClientFn
	origResolve := resolveShareFn
	origList := listSharesFn
	t.Cleanup(func() {
		setupSessionFn = origSetup
		newDriveClientFn = origNewClient
		resolveShareFn = origResolve
		listSharesFn = origList
	})

	rc := &cli.RuntimeContext{
		Config: config.DefaultConfig(),
	}
	cmds := []*cobra.Command{
		configCmd, getCmd, setCmd, unsetCmd, listCmd, showCmd,
	}
	for _, cmd := range cmds {
		cli.SetContext(cmd, rc)
	}
}

// injectSessionError sets setupSessionFn to return the given error.
func injectSessionError(err error) {
	setupSessionFn = func(_ context.Context, _ *cobra.Command) (*api.Session, error) {
		return nil, err
	}
}

// TestConfigCmdRegistration verifies that the config command has the
// expected subcommands registered.
func TestConfigCmdRegistration(t *testing.T) {
	expected := map[string]bool{
		"get":   false,
		"set":   false,
		"unset": false,
		"list":  false,
		"show":  false,
	}

	for _, sub := range configCmd.Commands() {
		if _, ok := expected[sub.Name()]; ok {
			expected[sub.Name()] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("config command missing subcommand %q", name)
		}
	}
}

// TestListAlias verifies that the list command has the "ls" alias.
func TestListAlias(t *testing.T) {
	found := false
	for _, alias := range listCmd.Aliases {
		if alias == "ls" {
			found = true
			break
		}
	}
	if !found {
		t.Error("list command missing 'ls' alias")
	}
}

// TestGetCmd_MissingArgs verifies that get without args returns an error.
func TestGetCmd_MissingArgs(t *testing.T) {
	saveAndRestore(t)
	err := getCmd.Args(getCmd, []string{})
	if err == nil {
		t.Error("expected error for missing args")
	}
}

// TestSetCmd_MissingArgs verifies that set with fewer than 2 args returns an error.
func TestSetCmd_MissingArgs(t *testing.T) {
	saveAndRestore(t)
	err := setCmd.Args(setCmd, []string{"core.max_jobs"})
	if err == nil {
		t.Error("expected error for missing value arg")
	}
	err = setCmd.Args(setCmd, []string{})
	if err == nil {
		t.Error("expected error for no args")
	}
}

// TestUnsetCmd_MissingArgs verifies that unset without args returns an error.
func TestUnsetCmd_MissingArgs(t *testing.T) {
	saveAndRestore(t)
	err := unsetCmd.Args(unsetCmd, []string{})
	if err == nil {
		t.Error("expected error for missing args")
	}
}

// TestGetCmd_UnknownSelector verifies that get with an unknown selector
// returns an appropriate error.
func TestGetCmd_UnknownSelector(t *testing.T) {
	saveAndRestore(t)
	err := runGet(getCmd, []string{"unknown.field"})
	if err == nil {
		t.Fatal("expected error for unknown selector")
	}
	if !strings.Contains(err.Error(), "unknown namespace") {
		t.Errorf("error = %v, want 'unknown namespace'", err)
	}
}

// TestGetCmd_ValidSelector verifies that get with a valid selector returns
// the default value.
func TestGetCmd_ValidSelector(t *testing.T) {
	saveAndRestore(t)
	// Should not error — returns the default value.
	err := runGet(getCmd, []string{"core.max_jobs"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestUnsetCmd_PartialSelector verifies that unset rejects partial selectors.
func TestUnsetCmd_PartialSelector(t *testing.T) {
	saveAndRestore(t)
	err := runUnset(unsetCmd, []string{"core"})
	if err == nil {
		t.Fatal("expected error for partial selector")
	}
	if !strings.Contains(err.Error(), "not a leaf") {
		t.Errorf("error = %v, want 'not a leaf'", err)
	}
}

// TestResolveShareSelector_ByID verifies that share[id=X] passes through unchanged.
func TestResolveShareSelector_ByID(t *testing.T) {
	saveAndRestore(t)
	sel, err := config.Parse("share[id=abc123].memory_cache")
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveShareSelector(configCmd, sel)
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments[0].IndexKey != "id" || result.Segments[0].IndexVal != "abc123" {
		t.Errorf("expected unchanged id selector, got %s", result.String())
	}
}

// TestResolveShareSelector_ByName verifies that share[name=X] resolves to share[id=Y].
func TestResolveShareSelector_ByName(t *testing.T) {
	saveAndRestore(t)

	// Mock session and drive client to resolve share name.
	setupSessionFn = func(_ context.Context, _ *cobra.Command) (*api.Session, error) {
		return &api.Session{}, nil
	}
	newDriveClientFn = func(_ context.Context, _ *api.Session) (*drive.Client, error) {
		return &drive.Client{}, nil
	}
	resolveShareFn = func(_ context.Context, _ *drive.Client, name string) (*drive.Share, error) {
		if name == "MyShare" {
			return testShareWithID("share-id-123"), nil
		}
		return nil, fmt.Errorf("not found")
	}

	sel, err := config.Parse("share[name=MyShare].memory_cache")
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveShareSelector(configCmd, sel)
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments[0].IndexKey != "id" {
		t.Errorf("expected index key 'id', got %q", result.Segments[0].IndexKey)
	}
	if result.Segments[0].IndexVal != "share-id-123" {
		t.Errorf("expected index val 'share-id-123', got %q", result.Segments[0].IndexVal)
	}
}

// TestResolveShareSelector_NameNotFound verifies error when share name doesn't exist.
func TestResolveShareSelector_NameNotFound(t *testing.T) {
	saveAndRestore(t)

	setupSessionFn = func(_ context.Context, _ *cobra.Command) (*api.Session, error) {
		return &api.Session{}, nil
	}
	newDriveClientFn = func(_ context.Context, _ *api.Session) (*drive.Client, error) {
		return &drive.Client{}, nil
	}
	resolveShareFn = func(_ context.Context, _ *drive.Client, _ string) (*drive.Share, error) {
		return nil, fmt.Errorf("share not found")
	}

	sel, err := config.Parse("share[name=NoSuchShare].memory_cache")
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolveShareSelector(configCmd, sel)
	if err == nil {
		t.Fatal("expected error for nonexistent share")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
}

// TestResolveShareSelector_NoAuth verifies error when auth is unavailable.
func TestResolveShareSelector_NoAuth(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("keyring locked"))

	sel, err := config.Parse("share[name=MyShare].memory_cache")
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolveShareSelector(configCmd, sel)
	if err == nil {
		t.Fatal("expected error when auth unavailable")
	}
	if !strings.Contains(err.Error(), "authenticated session") {
		t.Errorf("error = %v, want 'authenticated session'", err)
	}
}

// TestCleanupStaleShares verifies that stale share IDs are removed.
func TestCleanupStaleShares(t *testing.T) {
	saveAndRestore(t)

	setupSessionFn = func(_ context.Context, _ *cobra.Command) (*api.Session, error) {
		return &api.Session{}, nil
	}
	newDriveClientFn = func(_ context.Context, _ *api.Session) (*drive.Client, error) {
		return &drive.Client{}, nil
	}
	listSharesFn = func(_ context.Context, _ *drive.Client) ([]*drive.Share, error) {
		// Only "valid-id" exists.
		return []*drive.Share{testShareWithID("valid-id")}, nil
	}

	cfg := config.DefaultConfig()
	cfg.Shares["valid-id"] = api.ShareConfig{MemoryCache: api.CacheMetadata}
	cfg.Shares["stale-id"] = api.ShareConfig{MemoryCache: api.CacheLinkName}

	cleanupStaleShares(configCmd, cfg)

	if _, ok := cfg.Shares["valid-id"]; !ok {
		t.Error("valid-id should not be removed")
	}
	if _, ok := cfg.Shares["stale-id"]; ok {
		t.Error("stale-id should be removed")
	}
}

// TestCleanupStaleShares_NoSession verifies cleanup is skipped when no session.
func TestCleanupStaleShares_NoSession(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("no session"))

	cfg := config.DefaultConfig()
	cfg.Shares["some-id"] = api.ShareConfig{MemoryCache: api.CacheMetadata}

	cleanupStaleShares(configCmd, cfg)

	// Should not remove anything when session is unavailable.
	if _, ok := cfg.Shares["some-id"]; !ok {
		t.Error("shares should not be modified when session unavailable")
	}
}

// TestTransposeShareSelector verifies ID→name transposition for display.
func TestTransposeShareSelector(t *testing.T) {
	idToName := map[string]string{
		"abc123": "MyShare",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"share[id=abc123].memory_cache", "share[name=MyShare].memory_cache"},
		{"share[id=unknown].memory_cache", "share[id=unknown].memory_cache"},
		{"core.max_jobs", "core.max_jobs"},
	}

	for _, tt := range tests {
		got := transposeShareSelector(tt.input, idToName)
		if got != tt.want {
			t.Errorf("transposeShareSelector(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
