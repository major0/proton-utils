package shareCmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/major0/proton-cli/api/drive"
)

func TestParsePermissions(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr string
	}{
		{"read", "read", drive.PermViewer, ""},
		{"viewer", "viewer", drive.PermViewer, ""},
		{"write", "write", drive.PermEditor, ""},
		{"editor", "editor", drive.PermEditor, ""},
		{"admin invalid", "admin", 0, "invalid permissions"},
		{"empty", "", 0, "invalid permissions"},
		{"rw invalid", "rw", 0, "invalid permissions"},
		{"uppercase invalid", "Read", 0, "invalid permissions"},
		{"mixed case invalid", "WRITE", 0, "invalid permissions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePermissions(tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parsePermissions(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestShareInviteCmd_RestoreError verifies that runShareInvite returns
// an error when session restore fails.
func TestShareInviteCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	origPerms := inviteFlags.permissions
	t.Cleanup(func() { inviteFlags.permissions = origPerms })

	inviteFlags.permissions = "read"
	injectSessionError(fmt.Errorf("auth expired"))

	err := shareInviteCmd.RunE(shareInviteCmd, []string{"myshare", "user@test.local"})
	if err == nil || !strings.Contains(err.Error(), "auth expired") {
		t.Fatalf("error = %v, want 'auth expired'", err)
	}
}

// TestShareInviteCmd_InvalidPermissions verifies that invalid permissions
// are rejected before session restore.
func TestShareInviteCmd_InvalidPermissions(t *testing.T) {
	origPerms := inviteFlags.permissions
	t.Cleanup(func() { inviteFlags.permissions = origPerms })

	inviteFlags.permissions = "admin"

	err := shareInviteCmd.RunE(shareInviteCmd, []string{"myshare", "user@test.local"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid permissions") {
		t.Errorf("error = %q, want substring %q", err.Error(), "invalid permissions")
	}
}
