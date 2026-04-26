package driveCmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	driveClient "github.com/major0/proton-cli/api/drive/client"
	"pgregory.net/rapid"
)

// TestTypedErrorsMatchable validates that the typed errors from
// client.CreateFile are properly matchable via errors.As — unlike the
// old approach that tried to match sentinel errors as proton.APIError.
//
// This test confirms the fix: buildCopyJob can now branch on
// FileExistsError, DirExistsError, and DraftExistsError.
//
// **Validates: Requirements 2.1, 2.2**
func TestTypedErrorsMatchable(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "FileExistsError is matchable",
			err:  &driveClient.FileExistsError{},
		},
		{
			name: "DirExistsError is matchable",
			err:  &driveClient.DirExistsError{},
		},
		{
			name: "DraftExistsError is matchable",
			err:  &driveClient.DraftExistsError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Wrap the error as it would be in practice.
			wrapped := fmt.Errorf("some context: %w", tt.err)

			// Verify errors.As can extract the typed error.
			var fileErr *driveClient.FileExistsError
			var dirErr *driveClient.DirExistsError
			var draftErr *driveClient.DraftExistsError

			switch {
			case errors.As(tt.err, &fileErr):
				if !errors.As(wrapped, &fileErr) {
					t.Fatal("errors.As failed for FileExistsError")
				}
			case errors.As(tt.err, &dirErr):
				if !errors.As(wrapped, &dirErr) {
					t.Fatal("errors.As failed for DirExistsError")
				}
			case errors.As(tt.err, &draftErr):
				if !errors.As(wrapped, &draftErr) {
					t.Fatal("errors.As failed for DraftExistsError")
				}
			}
		})
	}
}

// TestSentinelNeverMatchedAPIError documents the root cause of the
// original bug: go-proton-api sentinel errors never satisfy errors.As
// for proton.APIError. This is a regression guard — if someone tries
// to use the old pattern, this test reminds them why it doesn't work.
func TestSentinelNeverMatchedAPIError(t *testing.T) {
	sentinels := []error{proton.ErrFileNameExist, proton.ErrADraftExist}
	for _, s := range sentinels {
		wrapped := fmt.Errorf("drive.CreateFile: %w", s)
		var apiErr proton.APIError
		if errors.As(wrapped, &apiErr) {
			t.Fatalf("sentinel %v unexpectedly matched proton.APIError — old bug pattern would work", s)
		}
	}
}

// TestBuildCopyJob_LocalSuccess_Preservation verifies that buildCopyJob
// with valid local src/dst always returns a CopyJob with non-nil Src and
// Dst. This preservation test must pass on unfixed code — it confirms
// the local→local path is unaffected by any Proton error handling changes.
//
// **Validates: Requirements 3.1, 3.2**
func TestBuildCopyJob_LocalSuccess_Preservation(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		size := int64(rapid.IntRange(1, 4096).Draw(t, "size"))
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i % 251) //nolint:gosec // deterministic test pattern
		}

		name := rapid.StringMatching(`[a-z]{6}`).Draw(t, "name")
		srcPath := filepath.Join(dir, "src_"+name+".bin")
		dstPath := filepath.Join(dir, "dst_"+name+".bin")

		if err := os.WriteFile(srcPath, data, 0600); err != nil {
			t.Fatalf("write src: %v", err)
		}

		srcInfo, err := os.Stat(srcPath)
		if err != nil {
			t.Fatalf("stat src: %v", err)
		}

		src := &resolvedEndpoint{
			pathType:  PathLocal,
			raw:       srcPath,
			localPath: srcPath,
			localInfo: srcInfo,
		}
		dst := &resolvedEndpoint{
			pathType:  PathLocal,
			raw:       dstPath,
			localPath: dstPath,
			localInfo: nil,
		}

		job, err := buildCopyJob(context.Background(), nil, src, dst, cpOptions{})
		if err != nil {
			t.Fatalf("buildCopyJob: %v", err)
		}
		if job == nil || job.Src == nil || job.Dst == nil {
			t.Fatal("expected non-nil job with Src and Dst")
		}
	})
}

// TestBuildCopyJob_SameFile_Preservation verifies that buildCopyJob with
// the same local src and dst path always returns "source and destination
// are the same". This preservation test must pass on unfixed code — it
// confirms the same-file check isn't broken by new error handling.
//
// **Validates: Requirements 3.1, 3.4**
func TestBuildCopyJob_SameFile_Preservation(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[a-z]{6}`).Draw(t, "name") + ".txt"
		p := filepath.Join(dir, name)

		if err := os.WriteFile(p, []byte("data"), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		ep := &resolvedEndpoint{
			pathType:  PathLocal,
			raw:       p,
			localPath: p,
			localInfo: info,
		}

		_, err = buildCopyJob(context.Background(), nil, ep, ep, cpOptions{})
		if err == nil {
			t.Fatal("expected error for same src and dst, got nil")
		}
		if !strings.Contains(err.Error(), "source and destination are the same") {
			t.Fatalf("error = %q, want substring %q", err, "source and destination are the same")
		}
	})
}
