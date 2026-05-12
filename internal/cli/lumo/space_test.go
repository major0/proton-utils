package lumoCmd

import "testing"

// TestSpaceCommandRegistration verifies that the space command group
// has all expected subcommands registered.
func TestSpaceCommandRegistration(t *testing.T) {
	subs := spaceCmd.Commands()
	if len(subs) == 0 {
		t.Fatal("space command has no subcommands")
	}

	want := map[string]bool{
		"create": false,
		"list":   false,
		"delete": false,
		"config": false,
	}

	for _, sub := range subs {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand: space %s", name)
		}
	}
}

// TestSpaceListHasAlias verifies that "ls" is an alias for "list".
func TestSpaceListHasAlias(t *testing.T) {
	if !spaceListCmd.HasAlias("ls") {
		t.Error("space list missing 'ls' alias")
	}
}

// TestSpaceCreateRequiresExactlyOneArg verifies that space create
// requires exactly 1 argument.
func TestSpaceCreateRequiresExactlyOneArg(t *testing.T) {
	cmd := spaceCreateCmd
	if cmd.Args == nil {
		t.Fatal("space create has no Args validator")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("space create accepted 0 args, want error")
	}
	if err := cmd.Args(cmd, []string{"name"}); err != nil {
		t.Errorf("space create rejected 1 arg: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("space create accepted 2 args, want error")
	}
}

// TestSpaceDeleteRequiresAtLeastOneArg verifies that space delete
// requires at least 1 argument.
func TestSpaceDeleteRequiresAtLeastOneArg(t *testing.T) {
	cmd := spaceDeleteCmd
	if cmd.Args == nil {
		t.Fatal("space delete has no Args validator")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("space delete accepted 0 args, want error")
	}
	if err := cmd.Args(cmd, []string{"id1"}); err != nil {
		t.Errorf("space delete rejected 1 arg: %v", err)
	}
	if err := cmd.Args(cmd, []string{"id1", "id2"}); err != nil {
		t.Errorf("space delete rejected 2 args: %v", err)
	}
}

// TestSpaceConfigRequiresExactlyOneArg verifies that space config
// requires exactly 1 argument.
func TestSpaceConfigRequiresExactlyOneArg(t *testing.T) {
	cmd := spaceConfigCmd
	if cmd.Args == nil {
		t.Fatal("space config has no Args validator")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("space config accepted 0 args, want error")
	}
	if err := cmd.Args(cmd, []string{"space-id"}); err != nil {
		t.Errorf("space config rejected 1 arg: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("space config accepted 2 args, want error")
	}
}
