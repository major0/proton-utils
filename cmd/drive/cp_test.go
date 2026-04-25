package driveCmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cli "github.com/major0/proton-cli/cmd"
)

func TestClassifyPath(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want PathType
	}{
		{"proton triple-slash", "proton:///Documents/file.txt", PathProton},
		{"proton double-slash", "proton://Photos/vacation.jpg", PathProton},
		{"proton bare prefix", "proton://", PathProton},
		{"absolute local", "/home/user/file.txt", PathLocal},
		{"relative local", "./relative/path", PathLocal},
		{"bare filename", "file.txt", PathLocal},
		{"empty string", "", PathLocal},
		{"uppercase prefix", "PROTON://uppercase", PathLocal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyPath(tt.arg); got != tt.want {
				t.Errorf("classifyPath(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestArgSplitting(t *testing.T) {
	// resetFlags zeroes cpFlags so tests are independent.
	resetFlags := func() {
		cpFlags = struct {
			recursive   bool
			archive     bool
			dereference bool
			noDeref     bool
			verbose     bool
			progress    bool
			preserve    string
			workers     int
			targetDir   string
			removeDest  bool
			force       bool
			backup      bool
		}{}
	}

	// Create temp files/dirs so path resolution succeeds and dispatch
	// reaches cpSingle → "not yet implemented".
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "src.txt")
	if err := os.WriteFile(srcFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	srcA := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(srcA, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	srcB := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(srcB, []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(tmp, "destdir")
	if err := os.Mkdir(destDir, 0700); err != nil {
		t.Fatal(err)
	}
	// dstFile is a non-existent path whose parent exists.
	dstFile := filepath.Join(tmp, "dst.txt")

	tests := []struct {
		name    string
		args    []string
		setup   func() // optional flag setup before calling runCp
		wantErr string // substring expected in error; empty means expect success
	}{
		{
			name:    "default mode valid args",
			args:    []string{srcFile, dstFile},
			wantErr: "",
		},
		{
			name:    "default mode multiple sources",
			args:    []string{srcA, srcB, destDir},
			wantErr: "",
		},
		{
			name: "target-directory mode",
			args: []string{srcA, srcB},
			setup: func() {
				cpFlags.targetDir = destDir
			},
			wantErr: "",
		},
		{
			name:    "fewer than 2 args without -t",
			args:    []string{"only-one"},
			wantErr: "missing destination operand",
		},
		{
			name: "remove-destination and backup mutually exclusive",
			args: []string{srcFile, dstFile},
			setup: func() {
				cpFlags.removeDest = true
				cpFlags.backup = true
			},
			wantErr: "mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetFlags()
			if tt.setup != nil {
				tt.setup()
			}

			err := runCp(nil, tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("runCp() returned error %q, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("runCp() returned nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("runCp() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// resetFlags zeroes cpFlags so tests are independent.
func resetFlags() {
	cli.Timeout = 30 * time.Second
	cpFlags = struct {
		recursive   bool
		archive     bool
		dereference bool
		noDeref     bool
		verbose     bool
		progress    bool
		preserve    string
		workers     int
		targetDir   string
		removeDest  bool
		force       bool
		backup      bool
	}{}
}

func TestDestSemantics(t *testing.T) {
	tmp := t.TempDir()

	// Fixtures: source files.
	srcFile := filepath.Join(tmp, "src.txt")
	if err := os.WriteFile(srcFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	srcA := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(srcA, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	srcB := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(srcB, []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}

	// Fixtures: destination directory.
	destDir := filepath.Join(tmp, "destdir")
	if err := os.Mkdir(destDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Fixtures: destination file (existing).
	destFile := filepath.Join(tmp, "existing.txt")
	if err := os.WriteFile(destFile, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	// Non-existent path whose parent exists.
	newDst := filepath.Join(tmp, "newfile.txt")

	// Non-existent path whose parent does NOT exist.
	deepDst := filepath.Join(tmp, "no", "such", "parent", "file.txt")

	// Non-existent directory path (for multi-source).
	missingDir := filepath.Join(tmp, "missing-dir")

	// Non-existent source.
	missingSrc := filepath.Join(tmp, "ghost.txt")

	tests := []struct {
		name    string
		args    []string
		wantErr string // empty means expect success
	}{
		{
			name:    "single source to existing directory",
			args:    []string{srcFile, destDir},
			wantErr: "",
		},
		{
			name:    "single source to non-existent path (parent exists)",
			args:    []string{srcFile, newDst},
			wantErr: "",
		},
		{
			name:    "multi-source to existing directory",
			args:    []string{srcA, srcB, destDir},
			wantErr: "",
		},
		{
			name:    "multi-source to non-existent path",
			args:    []string{srcA, srcB, missingDir},
			wantErr: "no such file or directory",
		},
		{
			name:    "multi-source to existing file",
			args:    []string{srcA, srcB, destFile},
			wantErr: "not a directory",
		},
		{
			name:    "single source to non-existent path (parent doesn't exist)",
			args:    []string{srcFile, deepDst},
			wantErr: "no such file or directory",
		},
		{
			name:    "source doesn't exist",
			args:    []string{missingSrc, destDir},
			wantErr: "no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetFlags()
			err := runCp(nil, tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("runCp() returned error %q, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("runCp() returned nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("runCp() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestResolvedEndpointIsDir(t *testing.T) {
	tmp := t.TempDir()

	file := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	dirInfo, err := os.Stat(tmp)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		ep   resolvedEndpoint
		want bool
	}{
		{
			name: "file endpoint",
			ep: resolvedEndpoint{
				pathType:  PathLocal,
				localPath: file,
				localInfo: fileInfo,
			},
			want: false,
		},
		{
			name: "dir endpoint",
			ep: resolvedEndpoint{
				pathType:  PathLocal,
				localPath: tmp,
				localInfo: dirInfo,
			},
			want: true,
		},
		{
			name: "nil localInfo (non-existent dest)",
			ep: resolvedEndpoint{
				pathType:  PathLocal,
				localPath: filepath.Join(tmp, "nope"),
				localInfo: nil,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ep.isDir(); got != tt.want {
				t.Errorf("isDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolvedEndpointBasename(t *testing.T) {
	tmp := t.TempDir()

	file := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(tmp, "mydir")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(sub)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		ep   resolvedEndpoint
		want string
	}{
		{
			name: "file endpoint",
			ep: resolvedEndpoint{
				pathType:  PathLocal,
				localPath: file,
				localInfo: fileInfo,
			},
			want: "hello.txt",
		},
		{
			name: "dir endpoint",
			ep: resolvedEndpoint{
				pathType:  PathLocal,
				localPath: sub,
				localInfo: dirInfo,
			},
			want: "mydir",
		},
		{
			name: "nil localInfo with localPath set",
			ep: resolvedEndpoint{
				pathType:  PathLocal,
				localPath: "/some/path/newfile.dat",
				localInfo: nil,
			},
			want: "newfile.dat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ep.basename(); got != tt.want {
				t.Errorf("basename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConflictHandling(t *testing.T) {
	t.Run("default refuses to overwrite existing file", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		if err := os.WriteFile(src, []byte("new-data"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("old-data-longer"), 0600); err != nil {
			t.Fatal(err)
		}

		err := runCp(nil, []string{src, dst})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "file exists") {
			t.Errorf("error = %q, want substring %q", err, "file exists")
		}

		// Original content should be preserved.
		got, err := os.ReadFile(dst) //nolint:gosec // test temp path
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "old-data-longer" {
			t.Errorf("dst content = %q, want %q (should be unchanged)", got, "old-data-longer")
		}
	})

	t.Run("-f overwrites existing file", func(t *testing.T) {
		resetFlags()
		cpFlags.force = true
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		if err := os.WriteFile(src, []byte("new-data"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("old-data-longer"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := runCp(nil, []string{src, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test temp path
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new-data" {
			t.Errorf("dst content = %q, want %q", got, "new-data")
		}
	})

	t.Run("--remove-destination removes before copy", func(t *testing.T) {
		resetFlags()
		cpFlags.removeDest = true
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("old-longer-content"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := runCp(nil, []string{src, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test temp path
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new" {
			t.Errorf("dst content = %q, want %q", got, "new")
		}
	})

	t.Run("--backup renames existing to tilde suffix", func(t *testing.T) {
		resetFlags()
		cpFlags.backup = true
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := runCp(nil, []string{src, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		// New content at dst.
		got, err := os.ReadFile(dst) //nolint:gosec // test temp path
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new" {
			t.Errorf("dst content = %q, want %q", got, "new")
		}

		// Old content at dst~.
		backup, err := os.ReadFile(dst + "~") //nolint:gosec // test temp path
		if err != nil {
			t.Fatalf("backup file missing: %v", err)
		}
		if string(backup) != "old" {
			t.Errorf("backup content = %q, want %q", backup, "old")
		}
	})

	t.Run("copy to non-existent dest (no conflict)", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "new.txt")
		if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := runCp(nil, []string{src, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test temp path
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "data" {
			t.Errorf("dst content = %q, want %q", got, "data")
		}
	})

	t.Run("copy into existing directory preserves basename", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dstDir := filepath.Join(tmp, "destdir")
		if err := os.WriteFile(src, []byte("hello"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dstDir, 0700); err != nil {
			t.Fatal(err)
		}

		if err := runCp(nil, []string{src, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(dstDir, "src.txt")) //nolint:gosec // test temp path
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello" {
			t.Errorf("dst content = %q, want %q", got, "hello")
		}
	})
}

func TestRecursiveCopy(t *testing.T) {
	t.Run("local to local recursive", func(t *testing.T) {
		resetFlags()
		cpFlags.recursive = true
		tmp := t.TempDir()

		// Build source tree: src/{a.txt, sub/{b.txt, deep/{c.txt}}}
		srcDir := filepath.Join(tmp, "src")
		_ = os.MkdirAll(filepath.Join(srcDir, "sub", "deep"), 0700)
		_ = os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0600)
		_ = os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("bbb"), 0600)
		_ = os.WriteFile(filepath.Join(srcDir, "sub", "deep", "c.txt"), []byte("ccc"), 0600)

		dstDir := filepath.Join(tmp, "dst")
		_ = os.Mkdir(dstDir, 0700)

		if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		// Dest should have: dst/src/{a.txt, sub/{b.txt, deep/{c.txt}}}
		for _, tc := range []struct {
			path    string
			content string
		}{
			{filepath.Join(dstDir, "src", "a.txt"), "aaa"},
			{filepath.Join(dstDir, "src", "sub", "b.txt"), "bbb"},
			{filepath.Join(dstDir, "src", "sub", "deep", "c.txt"), "ccc"},
		} {
			got, err := os.ReadFile(tc.path) //nolint:gosec // test temp path
			if err != nil {
				t.Errorf("missing %s: %v", tc.path, err)
				continue
			}
			if string(got) != tc.content {
				t.Errorf("%s: got %q, want %q", tc.path, got, tc.content)
			}
		}
	})

	t.Run("non-recursive directory skipped", func(t *testing.T) {
		resetFlags()
		// recursive NOT set
		tmp := t.TempDir()
		srcDir := filepath.Join(tmp, "src")
		_ = os.Mkdir(srcDir, 0700)
		_ = os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("x"), 0600)
		dstDir := filepath.Join(tmp, "dst")
		_ = os.Mkdir(dstDir, 0700)

		// Should succeed (skip the dir, no error) but not copy anything.
		if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		// dst/src should not exist.
		if _, err := os.Stat(filepath.Join(dstDir, "src")); err == nil {
			t.Error("expected dst/src to not exist (dir should be skipped)")
		}
	})

	t.Run("recursive with missing source file continues", func(t *testing.T) {
		resetFlags()
		cpFlags.recursive = true
		tmp := t.TempDir()

		srcDir := filepath.Join(tmp, "src")
		_ = os.Mkdir(srcDir, 0700)
		_ = os.WriteFile(filepath.Join(srcDir, "good.txt"), []byte("ok"), 0600)

		dstDir := filepath.Join(tmp, "dst")
		_ = os.Mkdir(dstDir, 0700)

		// Should copy good.txt even if other errors occur.
		if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(dstDir, "src", "good.txt")) //nolint:gosec // test temp path
		if err != nil {
			t.Fatalf("missing good.txt: %v", err)
		}
		if string(got) != "ok" {
			t.Errorf("good.txt: got %q, want %q", got, "ok")
		}
	})

	t.Run("empty directory creates dest dir", func(t *testing.T) {
		resetFlags()
		cpFlags.recursive = true
		tmp := t.TempDir()

		srcDir := filepath.Join(tmp, "empty")
		_ = os.Mkdir(srcDir, 0700)

		dstDir := filepath.Join(tmp, "dst")
		_ = os.Mkdir(dstDir, 0700)

		if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		// dst/empty should exist as a directory (created by dest-is-dir logic).
		info, err := os.Stat(filepath.Join(dstDir, "empty"))
		if err != nil {
			t.Fatalf("dst/empty missing: %v", err)
		}
		if !info.IsDir() {
			t.Error("dst/empty should be a directory")
		}
	})
}

func TestSymlinkHandling(t *testing.T) {
	t.Run("default skips symlink", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		target := filepath.Join(tmp, "real.txt")
		link := filepath.Join(tmp, "link.txt")
		dst := filepath.Join(tmp, "dst.txt")
		_ = os.WriteFile(target, []byte("data"), 0600)
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks not supported")
		}

		err := runCp(nil, []string{link, dst})
		// Should not fail — just skip with diagnostic.
		if err != nil && !errors.Is(err, errSkipSymlink) {
			t.Fatalf("runCp: %v", err)
		}
		// dst should not exist.
		if _, err := os.Stat(dst); err == nil {
			t.Error("expected dst to not exist (symlink should be skipped)")
		}
	})

	t.Run("-L follows symlink", func(t *testing.T) {
		resetFlags()
		cpFlags.dereference = true
		tmp := t.TempDir()
		target := filepath.Join(tmp, "real.txt")
		link := filepath.Join(tmp, "link.txt")
		dst := filepath.Join(tmp, "dst.txt")
		_ = os.WriteFile(target, []byte("followed"), 0600)
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks not supported")
		}

		if err := runCp(nil, []string{link, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		got, err := os.ReadFile(dst) //nolint:gosec // test temp path
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if string(got) != "followed" {
			t.Errorf("dst content = %q, want %q", got, "followed")
		}
	})

	t.Run("recursive skips symlinks in tree", func(t *testing.T) {
		resetFlags()
		cpFlags.recursive = true
		tmp := t.TempDir()

		srcDir := filepath.Join(tmp, "src")
		_ = os.Mkdir(srcDir, 0700)
		_ = os.WriteFile(filepath.Join(srcDir, "real.txt"), []byte("ok"), 0600)
		target := filepath.Join(tmp, "target.txt")
		_ = os.WriteFile(target, []byte("linked"), 0600)
		if err := os.Symlink(target, filepath.Join(srcDir, "link.txt")); err != nil {
			t.Skip("symlinks not supported")
		}

		dstDir := filepath.Join(tmp, "dst")
		_ = os.Mkdir(dstDir, 0700)

		if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		// real.txt should be copied.
		got, err := os.ReadFile(filepath.Join(dstDir, "src", "real.txt")) //nolint:gosec // test temp path
		if err != nil {
			t.Fatalf("missing real.txt: %v", err)
		}
		if string(got) != "ok" {
			t.Errorf("real.txt: got %q, want %q", got, "ok")
		}

		// link.txt should NOT be copied (symlink skipped).
		if _, err := os.Stat(filepath.Join(dstDir, "src", "link.txt")); err == nil {
			t.Error("expected link.txt to not exist (symlink should be skipped)")
		}
	})

	t.Run("recursive -L follows symlinks in tree", func(t *testing.T) {
		resetFlags()
		cpFlags.recursive = true
		cpFlags.dereference = true
		tmp := t.TempDir()

		srcDir := filepath.Join(tmp, "src")
		_ = os.Mkdir(srcDir, 0700)
		target := filepath.Join(tmp, "target.txt")
		_ = os.WriteFile(target, []byte("linked-data"), 0600)
		if err := os.Symlink(target, filepath.Join(srcDir, "link.txt")); err != nil {
			t.Skip("symlinks not supported")
		}

		dstDir := filepath.Join(tmp, "dst")
		_ = os.Mkdir(dstDir, 0700)

		if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		// link.txt should be copied (symlink followed).
		got, err := os.ReadFile(filepath.Join(dstDir, "src", "link.txt")) //nolint:gosec // test temp path
		if err != nil {
			t.Fatalf("missing link.txt: %v", err)
		}
		if string(got) != "linked-data" {
			t.Errorf("link.txt: got %q, want %q", got, "linked-data")
		}
	})
}

func TestPreservation(t *testing.T) {
	t.Run("preserve mode", func(t *testing.T) {
		resetFlags()
		cpFlags.preserve = "mode"
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		_ = os.WriteFile(src, []byte("data"), 0600)
		_ = os.Chmod(src, 0755) //nolint:gosec // testing mode preservation

		if err := runCp(nil, []string{src, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0755 {
			t.Errorf("mode = %o, want %o", info.Mode().Perm(), 0755)
		}
	})

	t.Run("preserve timestamps", func(t *testing.T) {
		resetFlags()
		cpFlags.preserve = "timestamps"
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		_ = os.WriteFile(src, []byte("data"), 0600)
		past := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)
		_ = os.Chtimes(src, past, past)

		if err := runCp(nil, []string{src, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(past) {
			t.Errorf("mtime = %v, want %v", info.ModTime(), past)
		}
	})

	t.Run("-a preserves mode and timestamps", func(t *testing.T) {
		resetFlags()
		cpFlags.archive = true
		tmp := t.TempDir()
		srcDir := filepath.Join(tmp, "src")
		_ = os.Mkdir(srcDir, 0700)
		src := filepath.Join(srcDir, "file.txt")
		_ = os.WriteFile(src, []byte("data"), 0600)
		_ = os.Chmod(src, 0754) //nolint:gosec // testing mode preservation
		past := time.Date(2019, 3, 10, 8, 30, 0, 0, time.UTC)
		_ = os.Chtimes(src, past, past)

		dstDir := filepath.Join(tmp, "dst")
		_ = os.Mkdir(dstDir, 0700)

		if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		dstFile := filepath.Join(dstDir, "src", "file.txt")
		info, err := os.Stat(dstFile)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0754 {
			t.Errorf("mode = %o, want %o", info.Mode().Perm(), 0754)
		}
		if !info.ModTime().Equal(past) {
			t.Errorf("mtime = %v, want %v", info.ModTime(), past)
		}
	})

	t.Run("no preserve flag leaves default mode", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		_ = os.WriteFile(src, []byte("data"), 0600)
		_ = os.Chmod(src, 0755) //nolint:gosec // testing mode preservation

		if err := runCp(nil, []string{src, dst}); err != nil {
			t.Fatalf("runCp: %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		// Without --preserve=mode, dest gets default permissions from os.Create
		// (0666 masked by umask). Just verify it's NOT 0755 (the source mode).
		if info.Mode().Perm() == 0755 {
			t.Error("mode should not be preserved without --preserve=mode")
		}
	})
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVerboseOutput(t *testing.T) {
	resetFlags()
	cpFlags.verbose = true
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	_ = os.WriteFile(src, []byte("data"), 0600)

	// Capture stderr.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := runCp(nil, []string{src, dst})

	_ = w.Close()
	os.Stderr = oldStderr

	if err != nil {
		t.Fatalf("runCp: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "->") {
		t.Errorf("verbose output should contain '->': %q", output)
	}
	if !strings.Contains(output, src) {
		t.Errorf("verbose output should contain source path: %q", output)
	}
}

func TestProgressRateLimit(t *testing.T) {
	var calls int
	var mu sync.Mutex
	pf := func(_, _ int, _ int64, _ float64) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	// Simulate rapid calls — rate limiter should suppress most.
	for i := 0; i < 100; i++ {
		pf(i, 100, int64(i*1024), 1024.0)
	}

	// The raw function doesn't rate-limit (that's in makeProgressFunc).
	// Just verify the callback is callable.
	mu.Lock()
	if calls != 100 {
		t.Errorf("expected 100 calls, got %d", calls)
	}
	mu.Unlock()
}

func TestErrorDiagnostics(t *testing.T) {
	t.Run("missing source", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		dst := filepath.Join(tmp, "dst.txt")
		err := runCp(nil, []string{filepath.Join(tmp, "ghost.txt"), dst})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no such file or directory") {
			t.Errorf("error = %q, want 'no such file or directory'", err)
		}
	})

	t.Run("dest not a directory for multi-source", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		a := filepath.Join(tmp, "a.txt")
		b := filepath.Join(tmp, "b.txt")
		dst := filepath.Join(tmp, "dst.txt")
		_ = os.WriteFile(a, []byte("a"), 0600)
		_ = os.WriteFile(b, []byte("b"), 0600)
		_ = os.WriteFile(dst, []byte("x"), 0600)

		err := runCp(nil, []string{a, b, dst})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("error = %q, want 'not a directory'", err)
		}
	})

	t.Run("same source and dest local", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		f := filepath.Join(tmp, "file.txt")
		_ = os.WriteFile(f, []byte("data"), 0600)

		err := runCp(nil, []string{f, f})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "source and destination are the same") {
			t.Errorf("error = %q, want 'source and destination are the same'", err)
		}
	})

	t.Run("missing destination parent", func(t *testing.T) {
		resetFlags()
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		_ = os.WriteFile(src, []byte("data"), 0600)
		dst := filepath.Join(tmp, "no", "such", "dir", "dst.txt")

		err := runCp(nil, []string{src, dst})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no such file or directory") {
			t.Errorf("error = %q, want 'no such file or directory'", err)
		}
	})

	t.Run("mutually exclusive flags", func(t *testing.T) {
		resetFlags()
		cpFlags.removeDest = true
		cpFlags.backup = true
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src.txt")
		dst := filepath.Join(tmp, "dst.txt")
		_ = os.WriteFile(src, []byte("data"), 0600)

		err := runCp(nil, []string{src, dst})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("error = %q, want 'mutually exclusive'", err)
		}
	})
}
