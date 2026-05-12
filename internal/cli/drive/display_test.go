package driveCmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

func TestParsePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"triple-slash with path", "proton:///Documents/file.txt", "Documents/file.txt"},
		{"named share with path", "proton://Photos/2024/pic.jpg", "Photos/2024/pic.jpg"},
		{"named share no path", "proton://Drive", "Drive"},
		{"triple-slash root", "proton:///", ""},
		{"invalid prefix", "/local/path", ""},
		{"bare proton://", "proton://", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePath(tt.input)
			if got != tt.want {
				t.Errorf("parsePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRawName(t *testing.T) {
	makeLink := func(lt proton.LinkType) *drive.Link {
		return drive.NewTestLink(&proton.Link{
			LinkID: "test-id",
			Type:   lt,
			State:  proton.LinkStateActive,
		}, nil, nil, nil, "test")
	}

	tests := []struct {
		name     string
		dispName string
		link     *drive.Link
		classify bool
		want     string
	}{
		{"file no classify", "file.txt", makeLink(proton.LinkTypeFile), false, "file.txt"},
		{"file with classify", "file.txt", makeLink(proton.LinkTypeFile), true, "file.txt"},
		{"dir no classify", "mydir", makeLink(proton.LinkTypeFolder), false, "mydir"},
		{"dir with classify", "mydir", makeLink(proton.LinkTypeFolder), true, "mydir/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rawName(tt.dispName, tt.link, tt.classify)
			if got != tt.want {
				t.Errorf("rawName(%q, classify=%v) = %q, want %q", tt.dispName, tt.classify, got, tt.want)
			}
		})
	}
}

func TestColorName(t *testing.T) {
	makeLink := func(lt proton.LinkType, state proton.LinkState) *drive.Link {
		return drive.NewTestLink(&proton.Link{
			LinkID: "test-id",
			Type:   lt,
			State:  state,
		}, nil, nil, nil, "test")
	}

	t.Run("no color returns plain name", func(t *testing.T) {
		l := makeLink(proton.LinkTypeFile, proton.LinkStateActive)
		got := colorName("file.txt", l, false, false)
		if got != "file.txt" {
			t.Errorf("got %q, want %q", got, "file.txt")
		}
	})

	t.Run("no color with classify on dir", func(t *testing.T) {
		l := makeLink(proton.LinkTypeFolder, proton.LinkStateActive)
		got := colorName("mydir", l, false, true)
		if got != "mydir/" {
			t.Errorf("got %q, want %q", got, "mydir/")
		}
	})

	t.Run("color on directory", func(t *testing.T) {
		l := makeLink(proton.LinkTypeFolder, proton.LinkStateActive)
		got := colorName("mydir", l, true, false)
		if !strings.Contains(got, colorBoldBlue) {
			t.Errorf("expected blue color for directory: %q", got)
		}
		if !strings.Contains(got, "mydir") {
			t.Errorf("expected name in output: %q", got)
		}
	})

	t.Run("color on trashed item", func(t *testing.T) {
		l := makeLink(proton.LinkTypeFile, proton.LinkStateTrashed)
		got := colorName("trashed.txt", l, true, false)
		if !strings.Contains(got, colorBoldRed) {
			t.Errorf("expected red color for trashed: %q", got)
		}
	})

	t.Run("color on regular file", func(t *testing.T) {
		l := makeLink(proton.LinkTypeFile, proton.LinkStateActive)
		got := colorName("file.txt", l, true, false)
		// Regular files get no color codes.
		if strings.Contains(got, "\033[") {
			t.Errorf("regular file should have no color: %q", got)
		}
		if got != "file.txt" {
			t.Errorf("got %q, want %q", got, "file.txt")
		}
	})
}

func TestMakeProgressFunc(t *testing.T) {
	// Capture stderr output from makeProgressFunc.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	pf := makeProgressFunc()
	// Call with final (completed == total) to force print.
	pf(10, 10, 10240, 1024.0)

	_ = w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "10/10") {
		t.Errorf("progress output should contain '10/10': %q", output)
	}
	if !strings.Contains(output, "KiB") {
		t.Errorf("progress output should contain 'KiB': %q", output)
	}
}

func TestPrintLong(t *testing.T) {
	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	pl := &proton.Link{
		LinkID:     "test-link-id",
		Type:       proton.LinkTypeFile,
		ModifyTime: 1718487045,
		State:      proton.LinkStateActive,
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				Size:       4096,
				CreateTime: 1718487045,
			},
		},
	}
	l := drive.NewTestLink(pl, nil, nil, nil, "hello.txt")
	entry := listEntry{entry: drive.DirEntry{Link: l}, name: "hello.txt"}

	printLong(entry, listOpts{human: true, timeStyle: timeLongISO})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "hello.txt") {
		t.Errorf("printLong should contain filename: %q", output)
	}
	if !strings.Contains(output, "-") {
		t.Errorf("printLong should contain type char: %q", output)
	}
}

func TestPrintEntries(t *testing.T) {
	makeEntry := func(name string, lt proton.LinkType) listEntry {
		pl := &proton.Link{
			LinkID:     name + "-id",
			Type:       lt,
			ModifyTime: 1718487045,
			State:      proton.LinkStateActive,
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	entries := []listEntry{
		makeEntry("alpha.txt", proton.LinkTypeFile),
		makeEntry("beta", proton.LinkTypeFolder),
		makeEntry("gamma.go", proton.LinkTypeFile),
	}

	formats := []struct {
		name   string
		format outputFormat
	}{
		{"single", formatSingle},
		{"long", formatLong},
		{"columns", formatColumns},
		{"across", formatAcross},
	}

	for _, f := range formats {
		t.Run(f.name, func(t *testing.T) {
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			printEntries(entries, listOpts{format: f.format, timeStyle: timeLongISO})

			_ = w.Close()
			os.Stdout = oldStdout

			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			output := buf.String()

			// All formats should include at least one entry name.
			if !strings.Contains(output, "alpha.txt") {
				t.Errorf("format %s: output should contain 'alpha.txt': %q", f.name, output)
			}
		})
	}
}

func TestPrintEntryColumns(t *testing.T) {
	makeEntry := func(name string) listEntry {
		pl := &proton.Link{
			LinkID: name + "-id",
			Type:   proton.LinkTypeFile,
			State:  proton.LinkStateActive,
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	t.Run("empty entries no output", func(t *testing.T) {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		printEntryColumns(nil, false, listOpts{})

		_ = w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		if buf.Len() != 0 {
			t.Errorf("empty entries should produce no output, got %q", buf.String())
		}
	})

	t.Run("with inode prefix", func(t *testing.T) {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		entries := []listEntry{makeEntry("file1"), makeEntry("file2")}
		printEntryColumns(entries, false, listOpts{inode: true})

		_ = w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		output := buf.String()

		if !strings.Contains(output, "file1-id") {
			t.Errorf("inode mode should show link ID: %q", output)
		}
	})

	t.Run("across mode", func(t *testing.T) {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		var entries []listEntry
		for i := 0; i < 5; i++ {
			entries = append(entries, makeEntry(fmt.Sprintf("f%d", i)))
		}
		printEntryColumns(entries, true, listOpts{})

		_ = w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		output := buf.String()

		if !strings.Contains(output, "f0") || !strings.Contains(output, "f4") {
			t.Errorf("across mode should show all entries: %q", output)
		}
	})
}
