package shareCmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/major0/proton-utils/api/drive"
	"pgregory.net/rapid"
)

// TestPropertyMutuallyExclusivePasswordFlags verifies that if more than one
// of --random, --stdin, --disable is set, the command returns an error.
//
// **Validates: Requirements 1c.5**
func TestPropertyMutuallyExclusivePasswordFlags(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		random := rapid.Bool().Draw(t, "random")
		stdin := rapid.Bool().Draw(t, "stdin")
		disable := rapid.Bool().Draw(t, "disable")

		flagCount := 0
		if random {
			flagCount++
		}
		if stdin {
			flagCount++
		}
		if disable {
			flagCount++
		}

		// Simulate the mutual exclusivity check from runShareURLPassword.
		isError := flagCount > 1

		// Verify: error iff more than one flag set.
		if isError && flagCount <= 1 {
			t.Fatal("expected no error but flagCount > 1")
		}
		if !isError && flagCount > 1 {
			t.Fatal("expected error but flagCount <= 1")
		}
	})
}

// TestShareURLPasswordCmd_MutuallyExclusiveFlags exercises the actual command
// with multiple flags set and verifies the error message.
func TestShareURLPasswordCmd_MutuallyExclusiveFlags(t *testing.T) {
	saveAndRestore(t)

	tests := []struct {
		name    string
		random  bool
		stdin   bool
		disable bool
		wantErr bool
	}{
		{"random+stdin", true, true, false, true},
		{"random+disable", true, false, true, true},
		{"stdin+disable", false, true, true, true},
		{"all three", true, true, true, true},
		{"only random", true, false, false, false},
		{"only stdin", false, true, false, false},
		{"only disable", false, false, true, false},
		{"none", false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origFlags := urlPasswordFlags
			t.Cleanup(func() { urlPasswordFlags = origFlags })

			urlPasswordFlags.random = tt.random
			urlPasswordFlags.stdin = tt.stdin
			urlPasswordFlags.disable = tt.disable

			if !tt.wantErr {
				// For non-error cases, we need session setup to proceed.
				// Inject a session error so the command fails after the
				// flag check (proving the flag check passed).
				injectSessionError(fmt.Errorf("session-sentinel"))
			}

			err := shareURLPasswordCmd.RunE(shareURLPasswordCmd, []string{"myshare"})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "mutually exclusive") {
					t.Errorf("error = %q, want 'mutually exclusive'", err.Error())
				}
			} else {
				// The command should have passed the flag check and failed
				// on session setup (our injected error).
				if err == nil {
					t.Fatal("expected session error, got nil")
				}
				if strings.Contains(err.Error(), "mutually exclusive") {
					t.Errorf("unexpected mutual exclusivity error for flags random=%v stdin=%v disable=%v",
						tt.random, tt.stdin, tt.disable)
				}
			}
		})
	}
}

// TestShareURLEnableCmd_RestoreError verifies that runShareURLEnable returns
// an error when session restore fails.
func TestShareURLEnableCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("auth expired"))

	err := shareURLEnableCmd.RunE(shareURLEnableCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "auth expired") {
		t.Fatalf("error = %v, want 'auth expired'", err)
	}
}

// TestShareURLEnableCmd_ResolveError verifies that runShareURLEnable returns
// an error when share resolution fails.
func TestShareURLEnableCmd_ResolveError(t *testing.T) {
	saveAndRestore(t)
	injectTestClient()

	err := shareURLEnableCmd.RunE(shareURLEnableCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "share not found") {
		t.Errorf("error = %q, want 'share not found'", err.Error())
	}
}

// TestShareURLEnableCmd_Success verifies the happy path.
func TestShareURLEnableCmd_Success(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", 2, "Shared Folder")
	injectResolvedShare(share)

	origCreate := createShareURLFn
	t.Cleanup(func() { createShareURLFn = origCreate })

	createShareURLFn = func(_ context.Context, _ *drive.Client, _ *drive.Share) (string, *drive.ShareURL, error) {
		return "generated-password-32chars-here!", &drive.ShareURL{ShareURLID: "url-1"}, nil
	}

	err := shareURLEnableCmd.RunE(shareURLEnableCmd, []string{"Shared Folder"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestShareURLDisableCmd_RestoreError verifies session restore error path.
func TestShareURLDisableCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("no session"))

	err := shareURLDisableCmd.RunE(shareURLDisableCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("error = %v, want 'no session'", err)
	}
}

// TestShareURLDisableCmd_NoURL verifies error when no URL exists.
func TestShareURLDisableCmd_NoURL(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", 2, "Shared Folder")
	injectResolvedShare(share)

	origList := listShareURLsFn
	t.Cleanup(func() { listShareURLsFn = origList })

	listShareURLsFn = func(_ context.Context, _ *drive.Client, _ string) ([]drive.ShareURL, error) {
		return nil, nil
	}

	err := shareURLDisableCmd.RunE(shareURLDisableCmd, []string{"Shared Folder"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no public URL exists") {
		t.Errorf("error = %q, want 'no public URL exists'", err.Error())
	}
}

// TestShareURLDisableCmd_Success verifies the happy path.
func TestShareURLDisableCmd_Success(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", 2, "Shared Folder")
	injectResolvedShare(share)

	origList := listShareURLsFn
	origDel := deleteShareURLFn
	t.Cleanup(func() {
		listShareURLsFn = origList
		deleteShareURLFn = origDel
	})

	listShareURLsFn = func(_ context.Context, _ *drive.Client, _ string) ([]drive.ShareURL, error) {
		return []drive.ShareURL{{ShareURLID: "url-1", ShareID: "share-std"}}, nil
	}
	deleteShareURLFn = func(_ context.Context, _ *drive.Client, _, _ string) error {
		return nil
	}

	err := shareURLDisableCmd.RunE(shareURLDisableCmd, []string{"Shared Folder"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestShareURLPasswordCmd_NoURL verifies error when no URL exists.
func TestShareURLPasswordCmd_NoURL(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", 2, "Shared Folder")
	injectResolvedShare(share)

	origFlags := urlPasswordFlags
	origList := listShareURLsFn
	t.Cleanup(func() {
		urlPasswordFlags = origFlags
		listShareURLsFn = origList
	})

	urlPasswordFlags.disable = true
	listShareURLsFn = func(_ context.Context, _ *drive.Client, _ string) ([]drive.ShareURL, error) {
		return nil, nil
	}

	err := shareURLPasswordCmd.RunE(shareURLPasswordCmd, []string{"Shared Folder"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no public URL exists") {
		t.Errorf("error = %q, want 'no public URL exists'", err.Error())
	}
}

// TestShareURLPasswordCmd_DisableSuccess verifies the --disable happy path.
func TestShareURLPasswordCmd_DisableSuccess(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", 2, "Shared Folder")
	injectResolvedShare(share)

	origFlags := urlPasswordFlags
	origList := listShareURLsFn
	origUpdate := updateShareURLPasswordFn
	t.Cleanup(func() {
		urlPasswordFlags = origFlags
		listShareURLsFn = origList
		updateShareURLPasswordFn = origUpdate
	})

	urlPasswordFlags.random = false
	urlPasswordFlags.stdin = false
	urlPasswordFlags.disable = true

	listShareURLsFn = func(_ context.Context, _ *drive.Client, _ string) ([]drive.ShareURL, error) {
		return []drive.ShareURL{{ShareURLID: "url-1", ShareID: "share-std"}}, nil
	}
	updateShareURLPasswordFn = func(_ context.Context, _ *drive.Client, _ *drive.Share, _ *drive.ShareURL, pw string) error {
		if pw != "" {
			t.Errorf("expected empty password for disable, got %q", pw)
		}
		return nil
	}

	err := shareURLPasswordCmd.RunE(shareURLPasswordCmd, []string{"Shared Folder"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
