package driveCmd

import (
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

func TestBuildPredicates(t *testing.T) {
	// Helper to create a test link.
	makeLink := func(name string, lt proton.LinkType, size int64, mtime int64) *drive.Link {
		pl := &proton.Link{
			LinkID:     name + "-id",
			Type:       lt,
			ModifyTime: mtime,
			State:      proton.LinkStateActive,
		}
		if lt == proton.LinkTypeFile {
			pl.FileProperties = &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					Size:       size,
					CreateTime: mtime,
				},
			}
		}
		return drive.NewTestLink(pl, nil, nil, nil, name)
	}

	resetFindFlags := func() {
		findFlags = struct {
			name     string
			iname    string
			findType string
			minSize  int64
			maxSize  int64
			mtime    int
			newer    string
			maxDepth int
			print0   bool
			print    bool
			depth    bool
			trashed  bool
		}{}
	}

	t.Run("no predicates matches everything", func(t *testing.T) {
		resetFindFlags()
		preds := buildPredicates()
		l := makeLink("test.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		if !matchAll(preds, "/test.txt", l, 0, "test.txt") {
			t.Error("empty predicates should match everything")
		}
	})

	t.Run("type f matches files only", func(t *testing.T) {
		resetFindFlags()
		findFlags.findType = "f"
		preds := buildPredicates()

		file := makeLink("file.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		dir := makeLink("dir", proton.LinkTypeFolder, 0, time.Now().Unix())

		if !matchAll(preds, "/file.txt", file, 0, "file.txt") {
			t.Error("type=f should match files")
		}
		if matchAll(preds, "/dir", dir, 0, "dir") {
			t.Error("type=f should not match directories")
		}
	})

	t.Run("type d matches directories only", func(t *testing.T) {
		resetFindFlags()
		findFlags.findType = "d"
		preds := buildPredicates()

		file := makeLink("file.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		dir := makeLink("dir", proton.LinkTypeFolder, 0, time.Now().Unix())

		if matchAll(preds, "/file.txt", file, 0, "file.txt") {
			t.Error("type=d should not match files")
		}
		if !matchAll(preds, "/dir", dir, 0, "dir") {
			t.Error("type=d should match directories")
		}
	})

	t.Run("name glob match", func(t *testing.T) {
		resetFindFlags()
		findFlags.name = "*.txt"
		preds := buildPredicates()

		l := makeLink("hello.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		if !matchAll(preds, "/hello.txt", l, 0, "hello.txt") {
			t.Error("*.txt should match hello.txt")
		}

		l2 := makeLink("hello.go", proton.LinkTypeFile, 100, time.Now().Unix())
		if matchAll(preds, "/hello.go", l2, 0, "hello.go") {
			t.Error("*.txt should not match hello.go")
		}
	})

	t.Run("iname case-insensitive match", func(t *testing.T) {
		resetFindFlags()
		findFlags.iname = "*.TXT"
		preds := buildPredicates()

		l := makeLink("Hello.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		if !matchAll(preds, "/Hello.txt", l, 0, "Hello.txt") {
			t.Error("iname *.TXT should match Hello.txt")
		}
	})

	t.Run("minSize filter", func(t *testing.T) {
		resetFindFlags()
		findFlags.minSize = 500
		preds := buildPredicates()

		small := makeLink("small.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		large := makeLink("large.txt", proton.LinkTypeFile, 1000, time.Now().Unix())

		if matchAll(preds, "/small.txt", small, 0, "small.txt") {
			t.Error("minSize=500 should not match 100-byte file")
		}
		if !matchAll(preds, "/large.txt", large, 0, "large.txt") {
			t.Error("minSize=500 should match 1000-byte file")
		}
	})

	t.Run("maxSize filter", func(t *testing.T) {
		resetFindFlags()
		findFlags.maxSize = 500
		preds := buildPredicates()

		small := makeLink("small.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		large := makeLink("large.txt", proton.LinkTypeFile, 1000, time.Now().Unix())

		if !matchAll(preds, "/small.txt", small, 0, "small.txt") {
			t.Error("maxSize=500 should match 100-byte file")
		}
		if matchAll(preds, "/large.txt", large, 0, "large.txt") {
			t.Error("maxSize=500 should not match 1000-byte file")
		}
	})

	t.Run("mtime negative (within N days)", func(t *testing.T) {
		resetFindFlags()
		findFlags.mtime = -7 // within 7 days
		preds := buildPredicates()

		recent := makeLink("recent.txt", proton.LinkTypeFile, 100, time.Now().Add(-24*time.Hour).Unix())
		old := makeLink("old.txt", proton.LinkTypeFile, 100, time.Now().Add(-30*24*time.Hour).Unix())

		if !matchAll(preds, "/recent.txt", recent, 0, "recent.txt") {
			t.Error("mtime=-7 should match file from 1 day ago")
		}
		if matchAll(preds, "/old.txt", old, 0, "old.txt") {
			t.Error("mtime=-7 should not match file from 30 days ago")
		}
	})

	t.Run("mtime positive (older than N days)", func(t *testing.T) {
		resetFindFlags()
		findFlags.mtime = 7 // older than 7 days
		preds := buildPredicates()

		recent := makeLink("recent.txt", proton.LinkTypeFile, 100, time.Now().Add(-24*time.Hour).Unix())
		old := makeLink("old.txt", proton.LinkTypeFile, 100, time.Now().Add(-30*24*time.Hour).Unix())

		if matchAll(preds, "/recent.txt", recent, 0, "recent.txt") {
			t.Error("mtime=7 should not match file from 1 day ago")
		}
		if !matchAll(preds, "/old.txt", old, 0, "old.txt") {
			t.Error("mtime=7 should match file from 30 days ago")
		}
	})

	t.Run("newer filter", func(t *testing.T) {
		resetFindFlags()
		findFlags.newer = "2024-01-01"
		preds := buildPredicates()

		newFile := makeLink("new.txt", proton.LinkTypeFile, 100, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC).Unix())
		oldFile := makeLink("old.txt", proton.LinkTypeFile, 100, time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC).Unix())

		if !matchAll(preds, "/new.txt", newFile, 0, "new.txt") {
			t.Error("newer=2024-01-01 should match file from 2024-06-01")
		}
		if matchAll(preds, "/old.txt", oldFile, 0, "old.txt") {
			t.Error("newer=2024-01-01 should not match file from 2023-06-01")
		}
	})

	t.Run("multiple predicates AND together", func(t *testing.T) {
		resetFindFlags()
		findFlags.findType = "f"
		findFlags.name = "*.txt"
		findFlags.minSize = 50
		preds := buildPredicates()

		match := makeLink("big.txt", proton.LinkTypeFile, 100, time.Now().Unix())
		noMatch := makeLink("big.go", proton.LinkTypeFile, 100, time.Now().Unix())

		if !matchAll(preds, "/big.txt", match, 0, "big.txt") {
			t.Error("should match file with .txt and size >= 50")
		}
		if matchAll(preds, "/big.go", noMatch, 0, "big.go") {
			t.Error("should not match .go file")
		}
	})
}

func TestMatchAll(t *testing.T) {
	l := drive.NewTestLink(&proton.Link{
		LinkID: "test-id",
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
	}, nil, nil, nil, "test")

	t.Run("empty predicates always true", func(t *testing.T) {
		if !matchAll(nil, "/test", l, 0, "test") {
			t.Error("nil predicates should match")
		}
	})

	t.Run("all true predicates", func(t *testing.T) {
		preds := []findPredicate{
			func(_ string, _ *drive.Link, _ int, _ string) bool { return true },
			func(_ string, _ *drive.Link, _ int, _ string) bool { return true },
		}
		if !matchAll(preds, "/test", l, 0, "test") {
			t.Error("all-true predicates should match")
		}
	})

	t.Run("one false predicate", func(t *testing.T) {
		preds := []findPredicate{
			func(_ string, _ *drive.Link, _ int, _ string) bool { return true },
			func(_ string, _ *drive.Link, _ int, _ string) bool { return false },
		}
		if matchAll(preds, "/test", l, 0, "test") {
			t.Error("one-false predicate should not match")
		}
	})
}
