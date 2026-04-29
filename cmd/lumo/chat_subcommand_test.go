package lumoCmd

import (
	"context"
	"strings"
	"testing"

	common "github.com/major0/proton-cli/api"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

// TestChatCreate_RequiresSession verifies that chat create returns an
// error when no session is available (Bearer session).
func TestChatCreate_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)
	cmd.SetArgs([]string{"test-title"})

	err := runChatCreate(cmd, []string{"test-title"})
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestChatResume_RequiresSession verifies that chat resume returns an
// error when no session is available.
func TestChatResume_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)

	err := runChatResume(cmd, []string{"conv-id-123"})
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestChatList_RequiresSession verifies that chat list returns an
// error when no session is available.
func TestChatList_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)

	err := runChatList(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestChatDelete_RequiresSession verifies that chat delete returns an
// error when no session is available.
func TestChatDelete_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)

	err := runChatDelete(cmd, []string{"conv-id-123"})
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestChatCreate_RequiresArg verifies that chat create requires exactly
// one argument.
func TestChatCreate_RequiresArg(t *testing.T) {
	cmd := chatCreateCmd
	if cmd.Args == nil {
		t.Fatal("chat create has no Args validator")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("chat create accepted 0 args, want error")
	}
	if err := cmd.Args(cmd, []string{"title"}); err != nil {
		t.Errorf("chat create rejected 1 arg: %v", err)
	}
}

// TestChatListHasAlias verifies that "ls" is an alias for "list".
func TestChatListHasAlias(t *testing.T) {
	if !chatListCmd.HasAlias("ls") {
		t.Error("chat list missing 'ls' alias")
	}
}

// newTestCmdWithCookieAuth creates a cobra.Command with a RuntimeContext
// that has a mock account store with the given CookieAuth value.
func newTestCmdWithCookieAuth(cookieAuth bool) *cobra.Command { //nolint:unparam // parameter kept for clarity
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	cli.SetContext(cmd, &cli.RuntimeContext{
		AccountStore: &mockStore{
			config: &common.SessionConfig{
				UID:        "test-uid",
				CookieAuth: cookieAuth,
			},
		},
	})
	return cmd
}
