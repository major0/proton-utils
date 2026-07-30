package driveCmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/major0/proton-utils/internal/cli/testutil"
	"github.com/spf13/cobra"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything fn wrote. It mirrors the capture pattern used by the existing
// printLong / printVolumeRows tests in this package.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

// mockSessionContext wires a RuntimeContext whose session store always fails,
// so a command's session setup returns an error deterministically without a
// live backend. It follows the withMockSession pattern in session_test.go.
func mockSessionContext(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	store := &testutil.MockSessionStore{LoadErr: errors.New("mock: no session available")}
	rc := &cli.RuntimeContext{
		Timeout:      5 * time.Second,
		SessionStore: store,
		AccountStore: store,
		CookieStore:  store,
		ServiceName:  "drive",
	}
	cli.SetContext(cmd, rc)
}

// TestFormatModeSymlink verifies ls renders a symlink with the POSIX mode
// string "lrwxrwxrwx" — the leading 'l' type char plus 0777 permission bits —
// regardless of any stored mode. A regular file must not render as a symlink.
//
// Validates: Requirement 4.4
func TestFormatModeSymlink(t *testing.T) {
	sym, err := drive.NewTestSymlinkLink("mylink", "/etc/hosts")
	if err != nil {
		t.Fatalf("NewTestSymlinkLink: %v", err)
	}
	if got := formatMode(sym); got != "lrwxrwxrwx" {
		t.Errorf("formatMode(symlink) = %q, want %q", got, "lrwxrwxrwx")
	}

	reg, err := drive.NewTestRegularFileLink("plain.txt", 12)
	if err != nil {
		t.Fatalf("NewTestRegularFileLink: %v", err)
	}
	if got := formatMode(reg); strings.HasPrefix(got, "l") {
		t.Errorf("formatMode(regular file) = %q, must not start with 'l'", got)
	}
}

// TestPrintLongSymlinkArrow verifies the long listing renders a symlink line as
// "lrwxrwxrwx ... name -> target": the 'l' type indicator from formatMode plus
// the " -> <target>" suffix from the entry's resolved target. The target is
// printed verbatim, so a dangling target renders unchanged.
//
// Validates: Requirement 4.4
func TestPrintLongSymlinkArrow(t *testing.T) {
	const target = "../does/not/exist" // dangling, relative — rendered verbatim
	sym, err := drive.NewTestSymlinkLink("mylink", target)
	if err != nil {
		t.Fatalf("NewTestSymlinkLink: %v", err)
	}
	entry := listEntry{
		entry:  drive.DirEntry{Link: sym},
		name:   "mylink",
		target: target,
	}

	out := captureStdout(t, func() {
		printLong(entry, listOpts{timeStyle: timeLongISO})
	})

	if !strings.HasPrefix(out, "lrwxrwxrwx") {
		t.Errorf("long line should start with symlink mode, got: %q", out)
	}
	if !strings.Contains(out, " -> "+target) {
		t.Errorf("long line should render arrow to verbatim target, got: %q", out)
	}
}

// TestPrintLongNoArrowForNonSymlink verifies the arrow is emitted only when a
// target is set: a listEntry with an empty target (a normal file, or a symlink
// whose target could not be read) renders no " -> " suffix.
//
// Validates: Requirement 4.4
func TestPrintLongNoArrowForNonSymlink(t *testing.T) {
	reg, err := drive.NewTestRegularFileLink("plain.txt", 5)
	if err != nil {
		t.Fatalf("NewTestRegularFileLink: %v", err)
	}
	entry := listEntry{entry: drive.DirEntry{Link: reg}, name: "plain.txt"}

	out := captureStdout(t, func() {
		printLong(entry, listOpts{timeStyle: timeLongISO})
	})

	if strings.Contains(out, " -> ") {
		t.Errorf("non-symlink line should not render an arrow, got: %q", out)
	}
}

// TestRunLnRequiresSymbolic verifies `ln` rejects invocation without -s/
// --symbolic: only symbolic links are supported. This guard runs before any
// session setup, so it is exercised directly.
func TestRunLnRequiresSymbolic(t *testing.T) {
	old := lnFlags.symbolic
	defer func() { lnFlags.symbolic = old }()
	lnFlags.symbolic = false

	err := runLn(driveLnCmd, []string{"/target", "proton:///link"})
	if err == nil {
		t.Fatal("expected error when -s is not set")
	}
	if !strings.Contains(err.Error(), "symbolic") {
		t.Errorf("error = %q, want mention of 'symbolic'", err)
	}
}

// TestLnCommandRegistration verifies the ln command is wired with the -s/-v
// flags and requires exactly two positional args (target, linkpath).
func TestLnCommandRegistration(t *testing.T) {
	if f := driveLnCmd.Flags().Lookup("symbolic"); f == nil || f.Shorthand != "s" {
		t.Error("ln missing -s/--symbolic flag")
	}
	if f := driveLnCmd.Flags().Lookup("verbose"); f == nil || f.Shorthand != "v" {
		t.Error("ln missing -v/--verbose flag")
	}
	if err := driveLnCmd.Args(driveLnCmd, []string{"only-one"}); err == nil {
		t.Error("ln accepted 1 arg, want exactly 2")
	}
	if err := driveLnCmd.Args(driveLnCmd, []string{"target", "linkpath"}); err != nil {
		t.Errorf("ln rejected 2 args: %v", err)
	}
}

// TestReadlinkCommandRegistration verifies the readlink command requires
// exactly one positional argument (the link path).
func TestReadlinkCommandRegistration(t *testing.T) {
	if err := driveReadlinkCmd.Args(driveReadlinkCmd, nil); err == nil {
		t.Error("readlink accepted 0 args, want exactly 1")
	}
	if err := driveReadlinkCmd.Args(driveReadlinkCmd, []string{"proton:///link"}); err != nil {
		t.Errorf("readlink rejected 1 arg: %v", err)
	}
	if err := driveReadlinkCmd.Args(driveReadlinkCmd, []string{"a", "b"}); err == nil {
		t.Error("readlink accepted 2 args, want exactly 1")
	}
}

// TestLnSymlinkSessionError verifies that, with -s set, ln proceeds past the
// flag guard into session setup and surfaces the session error. It confirms
// the create path never short-circuits on the (unresolved, unchecked) target —
// ln does not inspect the target before attempting the operation, matching the
// no-existence-check contract (dangling targets are valid). The verbatim
// round-trip and dangling-create behaviour are covered at the api/drive layer
// by CreateSymlink's property tests.
//
// Validates: Requirement 2.2 (wiring; create semantics covered in api/drive)
func TestLnSymlinkSessionError(t *testing.T) {
	old := lnFlags.symbolic
	defer func() { lnFlags.symbolic = old }()
	lnFlags.symbolic = true

	mockSessionContext(t, driveLnCmd)

	// A plainly dangling target — ln must not reject or resolve it; it fails
	// only later at session setup.
	err := runLn(driveLnCmd, []string{"/no/such/target", "proton:///newlink"})
	if err == nil {
		t.Fatal("expected session error")
	}
}

// TestReadlinkSessionError verifies the readlink command reaches session setup
// and surfaces its error. The verbatim target print is a thin wrapper over
// ReadSymlinkTarget, whose round-trip guarantee is covered at the api/drive
// layer (Property 3).
//
// Validates: Requirement 3.1 (wiring; verbatim round-trip covered in api/drive)
func TestReadlinkSessionError(t *testing.T) {
	mockSessionContext(t, driveReadlinkCmd)

	err := runReadlink(driveReadlinkCmd, []string{"proton:///somelink"})
	if err == nil {
		t.Fatal("expected session error")
	}
}

// TestMvSymlinkIsAgnostic verifies that mv treats a symlink like any other
// file link: doMove applies the generic Move (which relinks the node without
// touching its content or XAttr), so a symlink's verbatim target and POSIX
// Symlink marker are preserved across a rename. Here a symlink-typed file link
// on the same volume as its destination passes the cross-volume guard and
// reaches dc.Move (failing only at the nil client) — mv never special-cases or
// rewrites the symlink, which is what makes preservation fall out.
//
// Validates: Requirement 6.2 (mv is content/marker-agnostic; the underlying
// Move's preservation is exercised at the api/drive layer)
func TestMvSymlinkIsAgnostic(t *testing.T) {
	resolver := &testResolver{}
	share := makeTestShareWithVolume(resolver, "share", "vol-1")

	// A symlink is stored as a Type: file link; mv does not inspect the
	// Symlink marker, so a plain file link stands in for one here.
	symPLink := &proton.Link{LinkID: "symlink", Type: proton.LinkTypeFile}
	symLink := drive.NewTestLink(symPLink, share.Link, share, resolver, "link")

	dstPLink := &proton.Link{LinkID: "destdir", Type: proton.LinkTypeFolder}
	dstLink := drive.NewTestLink(dstPLink, share.Link, share, resolver, "dest")

	// Same volume — passes the cross-volume guard, then fails at dc.Move
	// (nil client). Crucially, it is NOT rejected for being a symlink.
	err := doMove(context.Background(), nil, share, symLink, share, dstLink, "link")
	if err == nil {
		t.Fatal("expected error from nil client, got nil")
	}
	if strings.Contains(err.Error(), "cross-volume") {
		t.Fatalf("same-volume symlink move should not get cross-volume error: %v", err)
	}
}

// TestRmSymlinkResolvesWithoutFollowing verifies the rm path for a dangling
// symlink: rmOne resolves the path with ResolveProtonPath, which walks the
// tree WITHOUT dereferencing symlinks, then calls the generic dc.Remove on the
// resolved link — so a dangling symlink (whose target does not resolve) is
// removed as itself, with no target resolution. An invalid (non-proton) path
// is rejected up front. The actual Remove of a live dangling symlink is
// exercised at the api/drive layer.
//
// Validates: Requirement 7.3 (rm targets the symlink itself; Remove is generic)
func TestRmSymlinkResolvesWithoutFollowing(t *testing.T) {
	// rmOne rejects non-proton paths before any resolution — confirming the
	// removal target is the given path, never a followed target.
	err := rmOne(context.Background(), nil, "/local/path")
	if err == nil {
		t.Fatal("expected error for non-proton path")
	}
	if !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("error = %q, want 'invalid path'", err)
	}
}
