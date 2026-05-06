package lumoCmd

import (
	"testing"

	"github.com/major0/proton-cli/api/lumo"
)

// TestChatCommandRegistration verifies that the chat command group and
// all subcommands are properly registered.
func TestChatCommandRegistration(t *testing.T) {
	subs := chatCmd.Commands()
	if len(subs) == 0 {
		t.Fatal("chat command has no subcommands")
	}

	want := map[string]bool{
		"create": false,
		"resume": false,
		"list":   false,
		"delete": false,
	}

	for _, sub := range subs {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand: chat %s", name)
		}
	}
}

// TestChatSpaceFlag verifies the --space persistent flag is registered.
func TestChatSpaceFlag(t *testing.T) {
	f := chatCmd.PersistentFlags().Lookup("space")
	if f == nil {
		t.Fatal("--space flag not registered on chat command")
	}
	if f.DefValue != "" {
		t.Errorf("--space default = %q, want empty", f.DefValue)
	}
}

// TestChatResumeRequiresArg verifies that chat resume requires exactly
// one argument.
func TestChatResumeRequiresArg(t *testing.T) {
	cmd := chatResumeCmd
	if cmd.Args == nil {
		t.Fatal("chat resume has no Args validator")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("chat resume accepted 0 args, want error")
	}
	if err := cmd.Args(cmd, []string{"conv-id"}); err != nil {
		t.Errorf("chat resume rejected 1 arg: %v", err)
	}
}

// TestChatDeleteRequiresArg verifies that chat delete requires exactly
// one argument.
func TestChatDeleteRequiresArg(t *testing.T) {
	cmd := chatDeleteCmd
	if cmd.Args == nil {
		t.Fatal("chat delete has no Args validator")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("chat delete accepted 0 args, want error")
	}
	if err := cmd.Args(cmd, []string{"conv-id"}); err != nil {
		t.Errorf("chat delete rejected 1 arg: %v", err)
	}
}

// TestDecryptMessageContent_EmptyEncrypted verifies that an empty
// Encrypted field returns an empty string without error.
func TestDecryptMessageContent_EmptyEncrypted(t *testing.T) {
	msg := lumo.Message{
		Role:       1,
		MessageTag: "tag",
	}
	result := decryptMessageContent(msg, nil, "conv-tag", nil)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

// TestDecryptConversationTitle_EmptyEncrypted verifies that an empty
// Encrypted field returns an empty string without error.
func TestDecryptConversationTitle_EmptyEncrypted(t *testing.T) {
	conv := lumo.Conversation{
		ConversationTag: "tag",
	}
	result := decryptConversationTitle(conv, nil, "space-tag")
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}
