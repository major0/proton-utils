package shareCmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/internal/cli"
)

func TestFmtTime(t *testing.T) {
	tests := []struct {
		name  string
		epoch int64
		check func(string) bool
	}{
		{"zero returns dash", 0, func(s string) bool { return s == "-" }},
		{"nonzero returns YYYY-MM-DD", 1705276800, func(s string) bool { return len(s) == 10 && s[4] == '-' && s[7] == '-' }},
		{"negative epoch", -1, func(s string) bool { return len(s) == 10 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cli.FormatEpoch(tt.epoch)
			if !tt.check(got) {
				t.Errorf("FormatEpoch(%d) = %q, unexpected", tt.epoch, got)
			}
		})
	}
}

func TestShareShowCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("session expired"))

	err := shareShowCmd.RunE(shareShowCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "session expired") {
		t.Fatalf("error = %v, want 'session expired'", err)
	}
}

func TestShareShowCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", []string{}, true},
		{"one arg valid", []string{"share"}, false},
		{"two args", []string{"share", "extra"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := shareShowCmd.Args(shareShowCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Args(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestPrintShareMetadata(t *testing.T) {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID:      "share-123",
			Type:         proton.ShareTypeStandard,
			Creator:      "alice@proton.me",
			CreationTime: 1705276800,
		},
	}
	pLink := &proton.Link{LinkID: "link-1"}
	share := drive.NewShare(pShare, nil, drive.NewTestLink(pLink, nil, nil, nil, "My Folder"), nil, "vol-1")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printShareMetadata(context.Background(), share)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	got := buf.String()

	for _, sub := range []string{"Share:    My Folder", "Type:     shared", "Creator:  alice@proton.me"} {
		if !strings.Contains(got, sub) {
			t.Errorf("output missing %q, got:\n%s", sub, got)
		}
	}
}

// captureStdStreams captures stdout and stderr during fn execution.
func captureStdStreams(t *testing.T, fn func()) string {
	t.Helper()
	oldOut := os.Stdout
	oldErr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	fn()

	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	var bufErr bytes.Buffer
	_, _ = io.Copy(io.Discard, rOut)
	_, _ = io.Copy(&bufErr, rErr)
	return bufErr.String()
}

func TestPrintMembers_Error(t *testing.T) {
	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")
	session := &api.Session{Client: client}
	dc := &drive.Client{Session: session}

	stderrOut := captureStdStreams(t, func() {
		printMembers(context.Background(), dc, "fake-share-id")
	})

	if !strings.Contains(stderrOut, "failed to list members") {
		t.Errorf("stderr missing warning, got: %q", stderrOut)
	}
}

func TestPrintInvitations_Error(t *testing.T) {
	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")
	session := &api.Session{Client: client}
	dc := &drive.Client{Session: session}

	stderrOut := captureStdStreams(t, func() {
		printInvitations(context.Background(), dc, "fake-share-id")
	})

	if !strings.Contains(stderrOut, "failed to list invitations") {
		t.Errorf("stderr missing warning, got: %q", stderrOut)
	}
}

func TestPrintExternalInvitations_Error(t *testing.T) {
	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")
	session := &api.Session{Client: client}
	dc := &drive.Client{Session: session}

	stderrOut := captureStdStreams(t, func() {
		printExternalInvitations(context.Background(), dc, "fake-share-id")
	})

	if !strings.Contains(stderrOut, "failed to list external invitations") {
		t.Errorf("stderr missing warning, got: %q", stderrOut)
	}
}
