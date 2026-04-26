package driveCmd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

func TestResolveProtonPathInvalidPrefix(t *testing.T) {
	// ResolveProtonPath should fail for non-proton:// paths.
	_, _, err := ResolveProtonPath(context.Background(), nil, "/local/path")
	if err == nil {
		t.Fatal("expected error for non-proton path")
	}
	if !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("error = %q, want 'invalid path'", err)
	}
}

func TestResolveProtonPathBarePrefix(t *testing.T) {
	// proton:// alone should fail.
	_, _, err := ResolveProtonPath(context.Background(), nil, "proton://")
	if err == nil {
		t.Fatal("expected error for bare proton://")
	}
	if !strings.Contains(err.Error(), "no share specified") {
		t.Errorf("error = %q, want 'no share specified'", err)
	}
}

func TestTransferOpts(t *testing.T) {
	t.Run("default workers", func(t *testing.T) {
		opts := cpOptions{}
		topts := transferOpts(opts)
		if topts.Progress != nil {
			t.Error("Progress should be nil without --progress")
		}
		if topts.Verbose != nil {
			t.Error("Verbose should be nil without --verbose")
		}
	})

	t.Run("progress enabled", func(t *testing.T) {
		opts := cpOptions{progress: true}
		topts := transferOpts(opts)
		if topts.Progress == nil {
			t.Error("Progress should not be nil with --progress")
		}
	})

	t.Run("verbose enabled", func(t *testing.T) {
		opts := cpOptions{verbose: true}
		topts := transferOpts(opts)
		if topts.Verbose == nil {
			t.Error("Verbose should not be nil with --verbose")
		}
	})
}

func TestPrintLongWithInode(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	pl := &proton.Link{
		LinkID:     "my-link-id-123",
		Type:       proton.LinkTypeFolder,
		ModifyTime: 1718487045,
		State:      proton.LinkStateActive,
	}
	l := drive.NewTestLink(pl, nil, nil, nil, "Documents")
	entry := listEntry{entry: drive.DirEntry{Link: l}, name: "Documents"}

	printLong(entry, listOpts{inode: true, timeStyle: timeLongISO})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "my-link-id-123") {
		t.Errorf("inode mode should show link ID: %q", output)
	}
	if !strings.Contains(output, "d") {
		t.Errorf("should show 'd' for directory: %q", output)
	}
}

func TestPrintEntriesSingleWithInode(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	pl := &proton.Link{
		LinkID: "link-abc",
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
	}
	l := drive.NewTestLink(pl, nil, nil, nil, "test.txt")
	entries := []listEntry{{entry: drive.DirEntry{Link: l}, name: "test.txt"}}

	printEntries(entries, listOpts{format: formatSingle, inode: true})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "link-abc") {
		t.Errorf("single format with inode should show link ID: %q", output)
	}
	if !strings.Contains(output, "test.txt") {
		t.Errorf("should show filename: %q", output)
	}
}

func TestResolveOptsFormatStrings(t *testing.T) {
	// Test remaining format string values.
	tests := []struct {
		name   string
		format string
		want   outputFormat
	}{
		{"single-column", "single-column", formatSingle},
		{"horizontal", "horizontal", formatAcross},
		{"vertical", "vertical", formatColumns},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listFlags = struct {
				all, almostAll, long, single, across, columns bool
				human, recursive, reverse                     bool
				sortSize, sortTime, unsorted                  bool
				fullTime, trash, classify, inode              bool
				format, sortWord, timeStyle, color            string
			}{format: tt.format, color: "never"}

			opts, err := resolveOpts()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.format != tt.want {
				t.Errorf("format = %d, want %d", opts.format, tt.want)
			}
		})
	}
}

func TestResolveOptsSortWords(t *testing.T) {
	tests := []struct {
		name     string
		sortWord string
		want     sortMode
	}{
		{"size", "size", sortSize},
		{"time", "time", sortTime},
		{"none", "none", sortNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listFlags = struct {
				all, almostAll, long, single, across, columns bool
				human, recursive, reverse                     bool
				sortSize, sortTime, unsorted                  bool
				fullTime, trash, classify, inode              bool
				format, sortWord, timeStyle, color            string
			}{sortWord: tt.sortWord, color: "never"}

			opts, err := resolveOpts()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.sortBy != tt.want {
				t.Errorf("sortBy = %d, want %d", opts.sortBy, tt.want)
			}
		})
	}
}
