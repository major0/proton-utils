package driveCmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cli "github.com/major0/proton-cli/cmd"
)

func TestRunCpArchiveMode(t *testing.T) {
	resetFlags()
	cpFlags.archive = true
	tmp := t.TempDir()

	srcDir := filepath.Join(tmp, "src")
	_ = os.MkdirAll(filepath.Join(srcDir, "sub"), 0700)
	_ = os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("data"), 0600)
	_ = os.Chmod(filepath.Join(srcDir, "file.txt"), 0754) //nolint:gosec // testing mode preservation
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = os.Chtimes(filepath.Join(srcDir, "file.txt"), past, past)
	_ = os.WriteFile(filepath.Join(srcDir, "sub", "nested.txt"), []byte("nested"), 0600)

	dstDir := filepath.Join(tmp, "dst")
	_ = os.Mkdir(dstDir, 0700)

	if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	// Verify recursive copy happened.
	got, err := os.ReadFile(filepath.Join(dstDir, "src", "file.txt")) //nolint:gosec // test temp path
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("content = %q, want %q", got, "data")
	}

	// Verify mode preserved.
	info, err := os.Stat(filepath.Join(dstDir, "src", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0754 {
		t.Errorf("mode = %o, want %o", info.Mode().Perm(), 0754)
	}

	// Verify timestamps preserved.
	if !info.ModTime().Equal(past) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), past)
	}

	// Verify nested file.
	got, err = os.ReadFile(filepath.Join(dstDir, "src", "sub", "nested.txt")) //nolint:gosec // test temp path
	if err != nil {
		t.Fatalf("missing nested file: %v", err)
	}
	if string(got) != "nested" {
		t.Errorf("nested content = %q, want %q", got, "nested")
	}
}

func TestRunCpTargetDirectory(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()

	srcA := filepath.Join(tmp, "a.txt")
	srcB := filepath.Join(tmp, "b.txt")
	_ = os.WriteFile(srcA, []byte("aaa"), 0600)
	_ = os.WriteFile(srcB, []byte("bbb"), 0600)

	dstDir := filepath.Join(tmp, "target")
	_ = os.Mkdir(dstDir, 0700)
	cpFlags.targetDir = dstDir

	if err := runCp(nil, []string{srcA, srcB}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	for _, tc := range []struct {
		path    string
		content string
	}{
		{filepath.Join(dstDir, "a.txt"), "aaa"},
		{filepath.Join(dstDir, "b.txt"), "bbb"},
	} {
		got, err := os.ReadFile(tc.path)
		if err != nil {
			t.Errorf("missing %s: %v", tc.path, err)
			continue
		}
		if string(got) != tc.content {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.content)
		}
	}
}

func TestRunCpEmptySourceList(t *testing.T) {
	resetFlags()
	cpFlags.targetDir = "/tmp"
	// No source args with -t should fail with "missing source operand".
	err := runCp(nil, []string{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing source operand") {
		t.Errorf("error = %q, want 'missing source operand'", err)
	}
}

func TestRunCpTimeout(t *testing.T) {
	// Verify that cli.Timeout is respected (just ensure it doesn't panic).
	resetFlags()
	oldTimeout := cli.Timeout
	cli.Timeout = 5 * time.Second
	defer func() { cli.Timeout = oldTimeout }()

	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	_ = os.WriteFile(src, []byte("x"), 0600)

	if err := runCp(nil, []string{src, dst}); err != nil {
		t.Fatalf("runCp: %v", err)
	}
}

func TestRunCpMultipleSourcesIntoDir(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()

	// Create multiple source files.
	for i := 0; i < 5; i++ {
		name := filepath.Join(tmp, "src", string(rune('a'+i))+".txt")
		_ = os.MkdirAll(filepath.Dir(name), 0700)
		_ = os.WriteFile(name, []byte(string(rune('a'+i))), 0600)
	}

	dstDir := filepath.Join(tmp, "dst")
	_ = os.Mkdir(dstDir, 0700)

	args := make([]string, 0, 6)
	for i := 0; i < 5; i++ {
		args = append(args, filepath.Join(tmp, "src", string(rune('a'+i))+".txt"))
	}
	args = append(args, dstDir)

	if err := runCp(nil, []string{args[0], args[1], args[2], args[3], args[4], dstDir}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	// Verify all files copied.
	for i := 0; i < 5; i++ {
		name := string(rune('a'+i)) + ".txt"
		got, err := os.ReadFile(filepath.Join(dstDir, name)) //nolint:gosec // test temp path
		if err != nil {
			t.Errorf("missing %s: %v", name, err)
			continue
		}
		if string(got) != string(rune('a'+i)) {
			t.Errorf("%s: got %q, want %q", name, got, string(rune('a'+i)))
		}
	}
}

func TestRunCpPreserveBoth(t *testing.T) {
	resetFlags()
	cpFlags.preserve = "mode,timestamps"
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	_ = os.WriteFile(src, []byte("data"), 0600)
	_ = os.Chmod(src, 0741) //nolint:gosec // testing mode preservation
	past := time.Date(2021, 3, 15, 10, 0, 0, 0, time.UTC)
	_ = os.Chtimes(src, past, past)

	if err := runCp(nil, []string{src, dst}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0741 {
		t.Errorf("mode = %o, want %o", info.Mode().Perm(), 0741)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), past)
	}
}

func TestRunCpRecursiveWithBackup(t *testing.T) {
	resetFlags()
	cpFlags.recursive = true
	cpFlags.backup = true
	tmp := t.TempDir()

	// Source tree.
	srcDir := filepath.Join(tmp, "src")
	_ = os.Mkdir(srcDir, 0700)
	_ = os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("new"), 0600)

	// Destination — no existing file (backup is no-op when dest doesn't exist).
	dstDir := filepath.Join(tmp, "dst")
	_ = os.Mkdir(dstDir, 0700)

	if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	// New content should be written.
	got, err := os.ReadFile(filepath.Join(dstDir, "src", "file.txt")) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

func TestRunCpRecursiveWithRemoveDest(t *testing.T) {
	resetFlags()
	cpFlags.recursive = true
	cpFlags.removeDest = true
	tmp := t.TempDir()

	srcDir := filepath.Join(tmp, "src")
	_ = os.Mkdir(srcDir, 0700)
	_ = os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("new"), 0600)

	dstDir := filepath.Join(tmp, "dst")
	_ = os.MkdirAll(filepath.Join(dstDir, "src"), 0700)
	_ = os.WriteFile(filepath.Join(dstDir, "src", "file.txt"), []byte("old-longer"), 0600)

	if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "src", "file.txt")) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}
