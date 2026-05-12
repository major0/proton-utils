package driveCmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDest(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, tmp string) pathArg
		multiSource bool
		wantErr     string
		check       func(t *testing.T, ep *resolvedEndpoint)
	}{
		{
			name: "existing directory single source",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				dir := filepath.Join(tmp, "dest")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				return pathArg{raw: dir, pathType: PathLocal}
			},
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if ep.localInfo == nil {
					t.Error("localInfo should not be nil for existing dir")
				}
				if !ep.isDir() {
					t.Error("should be a directory")
				}
			},
		},
		{
			name: "existing file single source",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				f := filepath.Join(tmp, "file.txt")
				if err := os.WriteFile(f, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				return pathArg{raw: f, pathType: PathLocal}
			},
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if ep.localInfo == nil {
					t.Error("localInfo should not be nil for existing file")
				}
				if ep.isDir() {
					t.Error("should not be a directory")
				}
			},
		},
		{
			name: "existing file multi source fails",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				f := filepath.Join(tmp, "file.txt")
				if err := os.WriteFile(f, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				return pathArg{raw: f, pathType: PathLocal}
			},
			multiSource: true,
			wantErr:     "not a directory",
		},
		{
			name: "non-existent path parent exists",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				return pathArg{raw: filepath.Join(tmp, "newfile.txt"), pathType: PathLocal}
			},
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if ep.localInfo != nil {
					t.Error("localInfo should be nil for non-existent dest")
				}
				if ep.localPath == "" {
					t.Error("localPath should be set")
				}
			},
		},
		{
			name: "non-existent path multi source fails",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				return pathArg{raw: filepath.Join(tmp, "nope"), pathType: PathLocal}
			},
			multiSource: true,
			wantErr:     "no such file or directory",
		},
		{
			name: "non-existent path parent doesn't exist",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				return pathArg{raw: filepath.Join(tmp, "no", "such", "dir", "file.txt"), pathType: PathLocal}
			},
			wantErr: "no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			arg := tt.setup(t, tmp)
			ctx := context.Background()

			ep, err := resolveDest(ctx, nil, arg, tt.multiSource)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, ep)
			}
		})
	}
}

func TestResolveSource(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, tmp string) pathArg
		opts    cpOptions
		wantErr string
		check   func(t *testing.T, ep *resolvedEndpoint)
	}{
		{
			name: "regular file",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				f := filepath.Join(tmp, "file.txt")
				if err := os.WriteFile(f, []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
				return pathArg{raw: f, pathType: PathLocal}
			},
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if ep.localInfo == nil {
					t.Error("localInfo should not be nil")
				}
				if ep.isDir() {
					t.Error("should not be a directory")
				}
			},
		},
		{
			name: "directory",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				dir := filepath.Join(tmp, "dir")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				return pathArg{raw: dir, pathType: PathLocal}
			},
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if !ep.isDir() {
					t.Error("should be a directory")
				}
			},
		},
		{
			name: "non-existent source",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				return pathArg{raw: filepath.Join(tmp, "ghost.txt"), pathType: PathLocal}
			},
			wantErr: "no such file or directory",
		},
		{
			name: "symlink without dereference",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				target := filepath.Join(tmp, "target.txt")
				if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(tmp, "link.txt")
				if err := os.Symlink(target, link); err != nil {
					t.Skip("symlinks not supported")
				}
				return pathArg{raw: link, pathType: PathLocal}
			},
			opts:    cpOptions{dereference: false},
			wantErr: "skipping symbolic link",
		},
		{
			name: "symlink with dereference",
			setup: func(t *testing.T, tmp string) pathArg {
				t.Helper()
				target := filepath.Join(tmp, "target.txt")
				if err := os.WriteFile(target, []byte("followed"), 0600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(tmp, "link.txt")
				if err := os.Symlink(target, link); err != nil {
					t.Skip("symlinks not supported")
				}
				return pathArg{raw: link, pathType: PathLocal}
			},
			opts: cpOptions{dereference: true},
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if ep.localInfo == nil {
					t.Error("localInfo should not be nil after following symlink")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			arg := tt.setup(t, tmp)
			ctx := context.Background()

			ep, err := resolveSource(ctx, nil, arg, tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, ep)
			}
		})
	}
}
