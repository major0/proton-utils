package driveCmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleConflict_LocalNoConflict verifies that handleConflict
// returns nil when the destination file does not exist.
func TestHandleConflict_LocalNoConflict(t *testing.T) {
	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       "/nonexistent/file.txt",
		localPath: "/nonexistent/file.txt",
		localInfo: nil, // does not exist
	}

	err := handleConflict(context.TODO(), nil, dst, cpOptions{})
	if err != nil {
		t.Fatalf("expected nil for non-existent dest, got: %v", err)
	}
}

// TestHandleConflict_LocalExistsNoFlag verifies that handleConflict
// returns an error when the destination exists and no override flag is set.
func TestHandleConflict_LocalExistsNoFlag(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(p, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)

	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       p,
		localPath: p,
		localInfo: info,
	}

	err := handleConflict(context.TODO(), nil, dst, cpOptions{})
	if err == nil {
		t.Fatal("expected error for existing file without flags")
	}
	if !strings.Contains(err.Error(), "file exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHandleConflict_LocalForce verifies that --force truncates the
// destination file to zero length.
func TestHandleConflict_LocalForce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(p, []byte("original content"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)

	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       p,
		localPath: p,
		localInfo: info,
	}

	err := handleConflict(context.TODO(), nil, dst, cpOptions{force: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file is truncated.
	newInfo, _ := os.Stat(p)
	if newInfo.Size() != 0 {
		t.Fatalf("file size = %d, want 0 after force", newInfo.Size())
	}
}

// TestHandleConflict_LocalBackup verifies that --backup renames the
// destination to <name>~.
func TestHandleConflict_LocalBackup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(p, []byte("backup me"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)

	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       p,
		localPath: p,
		localInfo: info,
	}

	err := handleConflict(context.TODO(), nil, dst, cpOptions{backup: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original should be gone, backup should exist.
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("original file should not exist after backup")
	}
	backupPath := p + "~"
	data, err := os.ReadFile(backupPath) //nolint:gosec // test file path from t.TempDir
	if err != nil {
		t.Fatalf("backup file not found: %v", err)
	}
	if string(data) != "backup me" {
		t.Fatalf("backup content = %q, want 'backup me'", data)
	}
}

// TestHandleConflict_LocalRemoveDest verifies that --remove-destination
// deletes the destination file.
func TestHandleConflict_LocalRemoveDest(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(p, []byte("remove me"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)

	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       p,
		localPath: p,
		localInfo: info,
	}

	err := handleConflict(context.TODO(), nil, dst, cpOptions{removeDest: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file should not exist after remove-destination")
	}
}

// TestHandleConflict_LocalDirectory verifies that directories always
// merge (no conflict).
func TestHandleConflict_LocalDirectory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0750); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(subdir)

	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       subdir,
		localPath: subdir,
		localInfo: info,
	}

	err := handleConflict(context.TODO(), nil, dst, cpOptions{})
	if err != nil {
		t.Fatalf("expected nil for directory (merge), got: %v", err)
	}
}
