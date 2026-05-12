package lumoCmd

import (
	"strings"
	"testing"
)

// TestSpaceList_RequiresSession verifies that space list returns an
// error when no cookie session is available.
func TestSpaceList_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)

	err := runSpaceList(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSpaceCreate_RequiresSession verifies that space create returns an
// error when no cookie session is available.
func TestSpaceCreate_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)

	err := runSpaceCreate(cmd, []string{"my-space"})
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSpaceDelete_RequiresSession verifies that space delete returns an
// error when no cookie session is available.
func TestSpaceDelete_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)

	err := runSpaceDelete(cmd, []string{"space-id-1"})
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSpaceConfig_RequiresSession verifies that space config returns an
// error when no cookie session is available.
func TestSpaceConfig_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)

	err := runSpaceConfig(cmd, []string{"space-id-1"})
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSpaceDeleteForceFlag verifies the --force / -f flag is registered.
func TestSpaceDeleteForceFlag(t *testing.T) {
	f := spaceDeleteCmd.Flags().Lookup("force")
	if f == nil {
		t.Fatal("--force flag not registered on space delete")
	}
	if f.Shorthand != "f" {
		t.Errorf("force shorthand = %q, want 'f'", f.Shorthand)
	}
}

// TestSpaceCreateProjectFlag verifies the --project flag is registered.
func TestSpaceCreateProjectFlag(t *testing.T) {
	f := spaceCreateCmd.Flags().Lookup("project")
	if f == nil {
		t.Fatal("--project flag not registered on space create")
	}
}
