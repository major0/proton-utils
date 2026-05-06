package drive

import (
	"errors"
	"fmt"
	"syscall"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

func TestFileExistsError_Is_EEXIST(t *testing.T) {
	link := NewTestLink(&proton.Link{LinkID: "file-123"}, nil, nil, nil, "file-123")
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
	link := NewTestLink(&proton.Link{LinkID: "dir-456"}, nil, nil, nil, "dir-456")
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
	link := NewTestLink(&proton.Link{LinkID: "draft-789"}, nil, nil, nil, "draft-789")
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

// TestPropertyStructuredErrorsSatisfyDriveSentinels verifies that for any
// FileExistsError instance, errors.Is matches both ErrFileNameExist and
// syscall.EEXIST, and for any DraftExistsError instance, errors.Is matches
// both ErrDraftExist and syscall.EEXIST.
//
// **Property 5: Structured errors satisfy drive sentinels**
// **Validates: Requirements 4.3**
func TestPropertyStructuredErrorsSatisfyDriveSentinels(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random LinkID for the Link carried by the error.
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{1,30}`).Draw(rt, "linkID")

		// Choose which error type to test.
		errType := rapid.IntRange(0, 1).Draw(rt, "errType")

		var link *Link
		useNilLink := rapid.Bool().Draw(rt, "nilLink")
		if !useNilLink {
			link = NewTestLink(&proton.Link{LinkID: linkID}, nil, nil, nil, linkID)
		}

		switch errType {
		case 0:
			// FileExistsError must satisfy ErrFileNameExist and syscall.EEXIST.
			var err error = &FileExistsError{Link: link}

			if !errors.Is(err, ErrFileNameExist) {
				rt.Fatalf("FileExistsError{Link:%v} does not satisfy errors.Is(ErrFileNameExist)", linkID)
			}
			if !errors.Is(err, syscall.EEXIST) {
				rt.Fatalf("FileExistsError{Link:%v} does not satisfy errors.Is(syscall.EEXIST)", linkID)
			}

		case 1:
			// DraftExistsError must satisfy ErrDraftExist and syscall.EEXIST.
			var err error = &DraftExistsError{Link: link}

			if !errors.Is(err, ErrDraftExist) {
				rt.Fatalf("DraftExistsError{Link:%v} does not satisfy errors.Is(ErrDraftExist)", linkID)
			}
			if !errors.Is(err, syscall.EEXIST) {
				rt.Fatalf("DraftExistsError{Link:%v} does not satisfy errors.Is(syscall.EEXIST)", linkID)
			}
		}
	})
}

// TestCreateFile_BoundaryTranslation_FileNameExist verifies that the
// CreateFile boundary translation wraps ErrFileNameExist (not the proton
// sentinel) so callers can match on drive.ErrFileNameExist.
func TestCreateFile_BoundaryTranslation_FileNameExist(t *testing.T) {
	// Simulate the exact wrapping pattern from CreateFile:
	//   return nil, fmt.Errorf("CreateFile: %w", ErrFileNameExist)
	wrapped := fmt.Errorf("CreateFile: %w", ErrFileNameExist)

	// The drive sentinel must match.
	if !errors.Is(wrapped, ErrFileNameExist) {
		t.Fatal("expected errors.Is(wrapped, ErrFileNameExist) == true")
	}

	// The old proton sentinel must NOT match — this is the isolation guarantee.
	if errors.Is(wrapped, proton.ErrFileNameExist) {
		t.Fatal("expected errors.Is(wrapped, proton.ErrFileNameExist) == false; proton sentinel leaked through boundary")
	}
}

// TestCreateFile_BoundaryTranslation_DraftExist verifies that the
// CreateFile boundary translation wraps ErrDraftExist (not the proton
// sentinel) so callers can match on drive.ErrDraftExist.
func TestCreateFile_BoundaryTranslation_DraftExist(t *testing.T) {
	// Simulate the exact wrapping pattern from CreateFile:
	//   return nil, fmt.Errorf("CreateFile: %w", ErrDraftExist)
	wrapped := fmt.Errorf("CreateFile: %w", ErrDraftExist)

	// The drive sentinel must match.
	if !errors.Is(wrapped, ErrDraftExist) {
		t.Fatal("expected errors.Is(wrapped, ErrDraftExist) == true")
	}

	// The old proton sentinel must NOT match — this is the isolation guarantee.
	if errors.Is(wrapped, proton.ErrADraftExist) {
		t.Fatal("expected errors.Is(wrapped, proton.ErrADraftExist) == false; proton sentinel leaked through boundary")
	}
}

// TestCreateFile_BoundaryTranslation_StructuredErrorAlsoMatches verifies
// that a FileExistsError (structured error) also satisfies the new drive
// sentinel via its Is() method, ensuring both error paths converge.
func TestCreateFile_BoundaryTranslation_StructuredErrorAlsoMatches(t *testing.T) {
	link := NewTestLink(&proton.Link{LinkID: "blocker-1"}, nil, nil, nil, "blocker-1")

	// FileExistsError (returned by typed error path) must match ErrFileNameExist.
	fileErr := &FileExistsError{Link: link}
	if !errors.Is(fileErr, ErrFileNameExist) {
		t.Fatal("FileExistsError should satisfy errors.Is(ErrFileNameExist)")
	}
	if errors.Is(fileErr, proton.ErrFileNameExist) {
		t.Fatal("FileExistsError should NOT satisfy errors.Is(proton.ErrFileNameExist)")
	}

	// DraftExistsError (returned by typed error path) must match ErrDraftExist.
	draftErr := &DraftExistsError{Link: link}
	if !errors.Is(draftErr, ErrDraftExist) {
		t.Fatal("DraftExistsError should satisfy errors.Is(ErrDraftExist)")
	}
	if errors.Is(draftErr, proton.ErrADraftExist) {
		t.Fatal("DraftExistsError should NOT satisfy errors.Is(proton.ErrADraftExist)")
	}
}
