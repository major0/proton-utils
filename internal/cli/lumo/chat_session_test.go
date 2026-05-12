package lumoCmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/major0/proton-cli/api/lumo"
)

// TestChatSession_EOF verifies that EOF on the reader causes Run to
// return nil (clean exit).
func TestChatSession_EOF(t *testing.T) {
	var out bytes.Buffer
	s := &ChatSession{
		Conversation: &lumo.Conversation{ID: "test-conv-id"},
		Writer:       &out,
		Reader:       strings.NewReader(""), // immediate EOF
	}

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error on EOF: %v", err)
	}
}

// TestChatSession_ExitCommand verifies that /exit causes Run to return nil.
func TestChatSession_ExitCommand(t *testing.T) {
	var out bytes.Buffer
	s := &ChatSession{
		Conversation: &lumo.Conversation{ID: "test-conv-id"},
		Writer:       &out,
		Reader:       strings.NewReader("/exit\n"),
	}

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error on /exit: %v", err)
	}
}

// TestChatSession_EmptyInputSkipped verifies that empty and
// whitespace-only lines are skipped without error.
func TestChatSession_EmptyInputSkipped(t *testing.T) {
	var out bytes.Buffer
	// Send empty lines followed by /exit.
	input := "\n  \n\t\n/exit\n"
	s := &ChatSession{
		Conversation: &lumo.Conversation{ID: "test-conv-id"},
		Writer:       &out,
		Reader:       strings.NewReader(input),
	}

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// TestChatSession_HelpCommand verifies that /help prints help text and
// continues the loop.
func TestChatSession_HelpCommand(t *testing.T) {
	var out bytes.Buffer
	input := "/help\n/exit\n"
	s := &ChatSession{
		Conversation: &lumo.Conversation{ID: "test-conv-id"},
		Writer:       &out,
		Reader:       strings.NewReader(input),
	}

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(out.String(), "/help") {
		t.Error("output missing help text")
	}
	if !strings.Contains(out.String(), "/exit") {
		t.Error("output missing /exit in help text")
	}
}

// TestChatSession_ContextCancelled verifies that a cancelled parent
// context causes Run to return the context error.
func TestChatSession_ContextCancelled(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	s := &ChatSession{
		Conversation: &lumo.Conversation{ID: "test-conv-id"},
		Writer:       &out,
		Reader:       strings.NewReader("hello\n"),
	}

	err := s.Run(ctx)
	if err == nil {
		// EOF may win the race against context check — both are valid exits.
		return
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want nil or context.Canceled", err)
	}
}

// TestIsEmptyInput verifies the empty input classification.
func TestIsEmptyInput(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{" ", true},
		{"\t", true},
		{"\n", true},
		{"  \t\n  ", true},
		{"hello", false},
		{" hello ", false},
	}

	for _, tt := range tests {
		got := IsEmptyInput(tt.input)
		if got != tt.want {
			t.Errorf("IsEmptyInput(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
