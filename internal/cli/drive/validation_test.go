package driveCmd

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestMkdirOneValidation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"invalid prefix", "/local/path", "invalid path"},
		{"bare proton://", "proton://", "no share specified"},
		{"triple-slash root only", "proton:///", "missing directory name"},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mkdirOne(ctx, nil, tt.path)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestRmdirOneValidation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"invalid prefix", "/local/path", "invalid path"},
		{"bare proton://", "proton://", "no share specified"},
		{"triple-slash root only", "proton:///", "missing directory name"},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rmdirOne(ctx, nil, tt.path)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestRmOneValidation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"invalid prefix", "/local/path", "invalid path"},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rmOne(ctx, nil, tt.path)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestAddCommandNotNil(t *testing.T) {
	// Verify AddCommand is callable and driveCmd is initialized.
	if driveCmd == nil {
		t.Fatal("driveCmd should not be nil")
	}
	// Verify subcommands are registered.
	cmds := driveCmd.Commands()
	if len(cmds) == 0 {
		t.Error("driveCmd should have subcommands")
	}

	// Verify specific subcommands exist.
	names := make(map[string]bool)
	for _, c := range cmds {
		names[c.Name()] = true
	}
	for _, want := range []string{"cp", "list", "find", "df", "mkdir", "mv", "rm", "rmdir"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}

	// Test AddCommand itself.
	testCmd := &cobra.Command{Use: "test-sub", Short: "test"}
	AddCommand(testCmd)
	found := false
	for _, c := range driveCmd.Commands() {
		if c.Name() == "test-sub" {
			found = true
			break
		}
	}
	if !found {
		t.Error("AddCommand should register the subcommand")
	}
	// Clean up.
	driveCmd.RemoveCommand(testCmd)
}
