package driveCmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleConflict(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, tmp string) *resolvedEndpoint
		opts    cpOptions
		verify  func(t *testing.T, tmp string)
		wantErr string
	}{
		{
			name: "directory endpoint is no-op",
			setup: func(t *testing.T, tmp string) *resolvedEndpoint {
				t.Helper()
				dir := filepath.Join(tmp, "dir")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(dir)
				return &resolvedEndpoint{
					pathType:  PathLocal,
					localPath: dir,
					localInfo: info,
				}
			},
			verify: func(t *testing.T, tmp string) {
				t.Helper()
				if _, err := os.Stat(filepath.Join(tmp, "dir")); err != nil {
					t.Errorf("directory should still exist: %v", err)
				}
			},
		},
		{
			name: "nil localInfo (non-existent dest) is no-op",
			setup: func(t *testing.T, tmp string) *resolvedEndpoint {
				t.Helper()
				return &resolvedEndpoint{
					pathType:  PathLocal,
					localPath: filepath.Join(tmp, "nonexistent"),
					localInfo: nil,
				}
			},
			verify: func(t *testing.T, _ string) { t.Helper() },
		},
		{
			name: "default refuses to overwrite existing file",
			setup: func(t *testing.T, tmp string) *resolvedEndpoint {
				t.Helper()
				f := filepath.Join(tmp, "existing.txt")
				if err := os.WriteFile(f, []byte("old-content"), 0600); err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(f)
				return &resolvedEndpoint{
					pathType:  PathLocal,
					localPath: f,
					localInfo: info,
				}
			},
			wantErr: "file exists",
			verify:  func(t *testing.T, _ string) { t.Helper() },
		},
		{
			name: "force truncates existing file",
			opts: cpOptions{force: true},
			setup: func(t *testing.T, tmp string) *resolvedEndpoint {
				t.Helper()
				f := filepath.Join(tmp, "existing.txt")
				if err := os.WriteFile(f, []byte("old-content"), 0600); err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(f)
				return &resolvedEndpoint{
					pathType:  PathLocal,
					localPath: f,
					localInfo: info,
				}
			},
			verify: func(t *testing.T, tmp string) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(tmp, "existing.txt")) //nolint:gosec // test temp path
				if err != nil {
					t.Fatal(err)
				}
				if len(data) != 0 {
					t.Errorf("file should be truncated, got %d bytes", len(data))
				}
			},
		},
		{
			name: "removeDest removes existing file",
			opts: cpOptions{removeDest: true},
			setup: func(t *testing.T, tmp string) *resolvedEndpoint {
				t.Helper()
				f := filepath.Join(tmp, "remove-me.txt")
				if err := os.WriteFile(f, []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(f)
				return &resolvedEndpoint{
					pathType:  PathLocal,
					localPath: f,
					localInfo: info,
				}
			},
			verify: func(t *testing.T, tmp string) {
				t.Helper()
				if _, err := os.Stat(filepath.Join(tmp, "remove-me.txt")); !os.IsNotExist(err) {
					t.Error("file should have been removed")
				}
			},
		},
		{
			name: "backup renames to tilde suffix",
			opts: cpOptions{backup: true},
			setup: func(t *testing.T, tmp string) *resolvedEndpoint {
				t.Helper()
				f := filepath.Join(tmp, "backup-me.txt")
				if err := os.WriteFile(f, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(f)
				return &resolvedEndpoint{
					pathType:  PathLocal,
					localPath: f,
					localInfo: info,
				}
			},
			verify: func(t *testing.T, tmp string) {
				t.Helper()
				if _, err := os.Stat(filepath.Join(tmp, "backup-me.txt")); !os.IsNotExist(err) {
					t.Error("original file should have been renamed")
				}
				data, err := os.ReadFile(filepath.Join(tmp, "backup-me.txt~")) //nolint:gosec // test temp path
				if err != nil {
					t.Fatalf("backup file missing: %v", err)
				}
				if string(data) != "original" {
					t.Errorf("backup content = %q, want %q", data, "original")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			dst := tt.setup(t, tmp)
			ctx := context.Background()

			err := handleConflict(ctx, nil, dst, tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.verify(t, tmp)
		})
	}
}
