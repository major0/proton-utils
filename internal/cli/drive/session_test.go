package driveCmd

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/major0/proton-utils/internal/cli/testutil"
	"github.com/spf13/cobra"
)

// withMockSession sets up a mock session store that always fails,
// allowing us to test the error paths of session-dependent functions.
func withMockSession(t *testing.T) func() {
	t.Helper()
	mockStore := &testutil.MockSessionStore{LoadErr: errors.New("mock: no session available")}
	rc := &cli.RuntimeContext{
		Timeout:      5 * time.Second,
		SessionStore: mockStore,
		AccountStore: mockStore,
		CookieStore:  mockStore,
		ServiceName:  "drive",
	}
	// Set context on all drive commands that tests call.
	cmds := []*cobra.Command{
		driveListCmd, driveFindCmd, driveDfCmd,
		driveMkdirCmd, driveMvCmd, driveRmCmd,
		driveRmdirCmd, driveTrashEmptyCmd,
	}
	for _, cmd := range cmds {
		cli.SetContext(cmd, rc)
	}
	return func() {}
}

func TestRunListSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveListCmd.RunE(driveListCmd, []string{"proton:///Documents"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "session") && !strings.Contains(err.Error(), "mock") {
		t.Logf("error: %v", err)
	}
}

func TestRunFindSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveFindCmd.RunE(driveFindCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunDfSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveDfCmd.RunE(driveDfCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunMkdirSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveMkdirCmd.RunE(driveMkdirCmd, []string{"proton:///Drive/newdir"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunMvSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveMvCmd.RunE(driveMvCmd, []string{"proton:///src", "proton:///dst"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunRmSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveRmCmd.RunE(driveRmCmd, []string{"proton:///file.txt"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunRmdirSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveRmdirCmd.RunE(driveRmdirCmd, []string{"proton:///dir"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunEmptyTrashSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveTrashEmptyCmd.RunE(driveTrashEmptyCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunCpProtonDestSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()
	resetFlags()

	tmp := t.TempDir()
	src := tmp + "/src.txt"
	_ = os.WriteFile(src, []byte("data"), 0600)

	// Create a command with RuntimeContext for runCp.
	cmd := &cobra.Command{}
	mockStore := &testutil.MockSessionStore{LoadErr: errors.New("mock: no session available")}
	rc := &cli.RuntimeContext{
		Timeout:      5 * time.Second,
		SessionStore: mockStore,
		AccountStore: mockStore,
		CookieStore:  mockStore,
		ServiceName:  "drive",
	}
	cli.SetContext(cmd, rc)

	err := runCp(cmd, []string{src, "proton:///dest.txt"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunCpProtonSourceSessionError(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()
	resetFlags()

	tmp := t.TempDir()
	dst := tmp + "/dst.txt"

	// Create a command with RuntimeContext for runCp.
	cmd := &cobra.Command{}
	mockStore2 := &testutil.MockSessionStore{LoadErr: errors.New("mock: no session available")}
	rc := &cli.RuntimeContext{
		Timeout:      5 * time.Second,
		SessionStore: mockStore2,
		AccountStore: mockStore2,
		CookieStore:  mockStore2,
		ServiceName:  "drive",
	}
	cli.SetContext(cmd, rc)

	err := runCp(cmd, []string{"proton:///src.txt", dst})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunListResolveOptsError(t *testing.T) {
	// Set invalid format to trigger resolveOpts error before session.
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{format: "invalid-format", color: "never"}

	err := driveListCmd.RunE(driveListCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --format") {
		t.Errorf("error = %q, want 'invalid --format'", err)
	}
}

func TestRunMkdirOneInvalidPath(t *testing.T) {
	// mkdirOne with non-proton path — tested directly, not through runMkdir.
	// runMkdir calls RestoreSession first, so we test mkdirOne directly.
	err := mkdirOne(context.Background(), nil, "/local/path")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("error = %q, want 'invalid path'", err)
	}
}

func TestRunRmdirOneInvalidPath(t *testing.T) {
	err := rmdirOne(context.Background(), nil, "/local/path")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("error = %q, want 'invalid path'", err)
	}
}

func TestRunRmOneInvalidPath(t *testing.T) {
	err := rmOne(context.Background(), nil, "/local/path")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("error = %q, want 'invalid path'", err)
	}
}

func TestRunListSessionErrorWithOpts(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	// Set various list flags to exercise resolveOpts paths before session.
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{
		long:      true,
		human:     true,
		recursive: true,
		sortTime:  true,
		color:     "always",
		classify:  true,
		inode:     true,
		timeStyle: "full-iso",
	}

	err := driveListCmd.RunE(driveListCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunFindWithFlags(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	// Set find flags to exercise buildPredicates.
	findFlags = struct {
		name     string
		iname    string
		findType string
		minSize  int64
		maxSize  int64
		mtime    int
		newer    string
		maxDepth int
		print0   bool
		print    bool
		depth    bool
		trashed  bool
	}{
		name:     "*.txt",
		findType: "f",
		minSize:  100,
		maxDepth: 3,
		print0:   true,
	}

	err := driveFindCmd.RunE(driveFindCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
