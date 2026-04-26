package client

import (
	"errors"
	"syscall"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

func TestFileExistsError_Is_EEXIST(t *testing.T) {
	link := drive.NewTestLink(&proton.Link{LinkID: "file-123"}, nil, nil, nil, "file-123")
	err := &FileExistsError{Link: link}

	if !errors.Is(err, syscall.EEXIST) {
		t.Fatal("FileExistsError should satisfy errors.Is(syscall.EEXIST)")
	}
	if errors.Is(err, syscall.EISDIR) {
		t.Fatal("FileExistsError should NOT satisfy errors.Is(syscall.EISDIR)")
	}
	if err.Error() != "file exists: file-123" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "file exists: file-123")
	}
}

func TestDirExistsError_Is_EISDIR(t *testing.T) {
	link := drive.NewTestLink(&proton.Link{LinkID: "dir-456"}, nil, nil, nil, "dir-456")
	err := &DirExistsError{Link: link}

	if !errors.Is(err, syscall.EISDIR) {
		t.Fatal("DirExistsError should satisfy errors.Is(syscall.EISDIR)")
	}
	if errors.Is(err, syscall.EEXIST) {
		t.Fatal("DirExistsError should NOT satisfy errors.Is(syscall.EEXIST)")
	}
	if err.Error() != "directory exists: dir-456" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "directory exists: dir-456")
	}
}

func TestDraftExistsError_Is_EEXIST(t *testing.T) {
	link := drive.NewTestLink(&proton.Link{LinkID: "draft-789"}, nil, nil, nil, "draft-789")
	err := &DraftExistsError{Link: link}

	if !errors.Is(err, syscall.EEXIST) {
		t.Fatal("DraftExistsError should satisfy errors.Is(syscall.EEXIST)")
	}
	if errors.Is(err, syscall.EISDIR) {
		t.Fatal("DraftExistsError should NOT satisfy errors.Is(syscall.EISDIR)")
	}
	if err.Error() != "draft exists: draft-789" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "draft exists: draft-789")
	}
}
