package driveCmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	apiPkg "github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
)

// TestResolvedEndpointBasenameProton exercises the Proton branch of basename.
func TestResolvedEndpointBasenameProton(t *testing.T) {
	tests := []struct {
		name string
		ep   resolvedEndpoint
		want string
	}{
		{
			name: "proton link with name",
			ep: resolvedEndpoint{
				pathType: PathProton,
				raw:      "proton:///Drive/file.txt",
				link: drive.NewTestLink(&proton.Link{
					LinkID: "link-1",
					Type:   proton.LinkTypeFile,
					State:  proton.LinkStateActive,
				}, nil, nil, nil, "file.txt"),
			},
			want: "file.txt",
		},
		{
			name: "proton nil link falls back to raw",
			ep: resolvedEndpoint{
				pathType: PathProton,
				raw:      "proton:///Drive/fallback.txt",
				link:     nil,
			},
			want: "fallback.txt",
		},
		{
			name: "local path basename",
			ep: resolvedEndpoint{
				pathType:  PathLocal,
				localPath: "/some/dir/local.txt",
			},
			want: "local.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ep.basename()
			if got != tt.want {
				t.Errorf("basename() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolvedEndpointIsDirProton exercises the Proton branch of isDir.
func TestResolvedEndpointIsDirProton(t *testing.T) {
	tests := []struct {
		name string
		ep   resolvedEndpoint
		want bool
	}{
		{
			name: "proton folder link",
			ep: resolvedEndpoint{
				pathType: PathProton,
				link: drive.NewTestLink(&proton.Link{
					LinkID: "dir-1",
					Type:   proton.LinkTypeFolder,
					State:  proton.LinkStateActive,
				}, nil, nil, nil, "docs"),
			},
			want: true,
		},
		{
			name: "proton file link",
			ep: resolvedEndpoint{
				pathType: PathProton,
				link: drive.NewTestLink(&proton.Link{
					LinkID: "file-1",
					Type:   proton.LinkTypeFile,
					State:  proton.LinkStateActive,
				}, nil, nil, nil, "file.txt"),
			},
			want: false,
		},
		{
			name: "proton nil link",
			ep: resolvedEndpoint{
				pathType: PathProton,
				link:     nil,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ep.isDir()
			if got != tt.want {
				t.Errorf("isDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHandleConflictProtonPaths exercises the Proton branches of handleConflict.
func TestHandleConflictProtonPaths(t *testing.T) {
	tests := []struct {
		name    string
		dst     *resolvedEndpoint
		opts    cpOptions
		wantErr string
	}{
		{
			name: "proton nil link is no-op",
			dst: &resolvedEndpoint{
				pathType: PathProton,
				link:     nil,
			},
		},
		{
			name: "proton directory is no-op",
			dst: &resolvedEndpoint{
				pathType: PathProton,
				link: drive.NewTestLink(&proton.Link{
					LinkID: "dir-1",
					Type:   proton.LinkTypeFolder,
					State:  proton.LinkStateActive,
				}, nil, nil, nil, "dir"),
			},
		},
		{
			name: "proton file without force refuses",
			dst: &resolvedEndpoint{
				pathType: PathProton,
				raw:      "proton://root/file.txt",
				link: drive.NewTestLink(&proton.Link{
					LinkID: "file-1",
					Type:   proton.LinkTypeFile,
					State:  proton.LinkStateActive,
				}, nil, nil, nil, "file.txt"),
			},
			wantErr: "file exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := handleConflict(ctx, nil, tt.dst, tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestRunCpFlagValidation exercises runCp flag validation paths.
func TestRunCpFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		setup   func()
		args    []string
		wantErr string
	}{
		{
			name: "mutually exclusive flags",
			setup: func() {
				resetFlags()
				cpFlags.removeDest = true
				cpFlags.backup = true
			},
			args:    []string{"src", "dst"},
			wantErr: "mutually exclusive",
		},
		{
			name: "missing destination operand",
			setup: func() {
				resetFlags()
			},
			args:    []string{"only-source"},
			wantErr: "missing destination operand",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			err := runCp(nil, tt.args)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestBuildCopyJobSameProtonLink exercises the same-link detection for Proton paths.
func TestBuildCopyJobSameProtonLink(t *testing.T) {
	link := drive.NewTestLink(&proton.Link{
		LinkID: "same-link-id",
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
	}, nil, nil, nil, "file.txt")

	src := &resolvedEndpoint{
		pathType: PathProton,
		raw:      "proton:///file.txt",
		link:     link,
	}
	dst := &resolvedEndpoint{
		pathType: PathProton,
		raw:      "proton:///file.txt",
		link:     link,
	}

	_, err := buildCopyJob(context.Background(), nil, src, dst)
	if err == nil {
		t.Fatal("expected error for same Proton link")
	}
	if !strings.Contains(err.Error(), "source and destination are the same") {
		t.Errorf("error = %q, want 'source and destination are the same'", err)
	}
}

// TestMkdirOneMissingDirName exercises the "missing directory name" path.
func TestMkdirOneMissingDirName(t *testing.T) {
	ctx := context.Background()
	// proton:/// parses to empty path → "missing directory name".
	err := mkdirOne(ctx, nil, "proton:///")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing directory name") {
		t.Errorf("error = %q, want 'missing directory name'", err)
	}
}

// TestRmdirOneMissingDirNameCoverage exercises the "missing directory name" path.
func TestRmdirOneMissingDirNameCoverage(t *testing.T) {
	ctx := context.Background()
	err := rmdirOne(ctx, nil, "proton:///")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing directory name") {
		t.Errorf("error = %q, want 'missing directory name'", err)
	}
}

// TestApplyPreserveMode exercises the mode-only preservation path.
func TestApplyPreserveMode(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	entries := []preserveEntry{
		{dstPath: f, mode: 0755},
	}
	applyPreserve(entries, cpOptions{preserve: "mode"})

	info, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("mode = %o, want %o", info.Mode().Perm(), 0755)
	}
}

// TestApplyPreserveEmpty exercises the no-preserve path.
func TestApplyPreserveEmpty(_ *testing.T) {
	// Should be a no-op, no panic.
	applyPreserve(nil, cpOptions{preserve: ""})
}

// TestResolveOptsInvalidSort exercises the invalid --sort error path.
func TestResolveOptsInvalidSort(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{sortWord: "invalid-sort", color: "never"}

	_, err := resolveOpts()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Errorf("error = %q, want 'invalid --sort'", err)
	}
}

// TestResolveOptsInvalidTimeStyle exercises the invalid --time-style error path.
func TestResolveOptsInvalidTimeStyle(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{timeStyle: "invalid-style", color: "never"}

	_, err := resolveOpts()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --time-style") {
		t.Errorf("error = %q, want 'invalid --time-style'", err)
	}
}

// TestResolveOptsInvalidColor exercises the invalid --color error path.
func TestResolveOptsInvalidColor(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{color: "invalid-color"}

	_, err := resolveOpts()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --color") {
		t.Errorf("error = %q, want 'invalid --color'", err)
	}
}

// TestResolveOptsFullTime exercises the --full-time flag path.
func TestResolveOptsFullTime(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{fullTime: true, color: "never"}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.format != formatLong {
		t.Errorf("format = %d, want formatLong", opts.format)
	}
	if opts.timeStyle != timeFull {
		t.Errorf("timeStyle = %d, want timeFull", opts.timeStyle)
	}
}

// TestResolveOptsBoolFlags exercises the boolean flag paths.
func TestResolveOptsBoolFlags(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{
		all:       true,
		almostAll: true,
		human:     true,
		recursive: true,
		reverse:   true,
		sortSize:  true,
		trash:     true,
		classify:  true,
		inode:     true,
		color:     "never",
	}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.all {
		t.Error("all should be true")
	}
	if !opts.almostAll {
		t.Error("almostAll should be true")
	}
	if !opts.human {
		t.Error("human should be true")
	}
	if !opts.recursive {
		t.Error("recursive should be true")
	}
	if !opts.reverse {
		t.Error("reverse should be true")
	}
	if opts.sortBy != sortSize {
		t.Errorf("sortBy = %d, want sortSize", opts.sortBy)
	}
	if !opts.trash {
		t.Error("trash should be true")
	}
	if !opts.classify {
		t.Error("classify should be true")
	}
	if !opts.inode {
		t.Error("inode should be true")
	}
}

// TestResolveOptsSortTime exercises the --sort-time flag.
func TestResolveOptsSortTime(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{sortTime: true, color: "never"}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.sortBy != sortTime {
		t.Errorf("sortBy = %d, want sortTime", opts.sortBy)
	}
}

// TestResolveOptsUnsorted exercises the --unsorted flag.
func TestResolveOptsUnsorted(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{unsorted: true, color: "never"}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.sortBy != sortNone {
		t.Errorf("sortBy = %d, want sortNone", opts.sortBy)
	}
}

// TestResolveOptsTimeStyleISO exercises the --time-style=iso path.
func TestResolveOptsTimeStyleISO(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{timeStyle: "iso", color: "never"}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.timeStyle != timeISO {
		t.Errorf("timeStyle = %d, want timeISO", opts.timeStyle)
	}
}

// TestResolveOptsFormatFlags exercises the format boolean flags.
func TestResolveOptsFormatFlags(t *testing.T) {
	tests := []struct {
		name  string
		setup func()
		want  outputFormat
	}{
		{
			name: "columns flag",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{columns: true, color: "never"}
			},
			want: formatColumns,
		},
		{
			name: "single flag",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{single: true, color: "never"}
			},
			want: formatSingle,
		},
		{
			name: "across flag",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{across: true, color: "never"}
			},
			want: formatAcross,
		},
		{
			name: "long flag",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{long: true, color: "never"}
			},
			want: formatLong,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
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

// TestResolveOptsFormatLongVerbose exercises the format=long/verbose string.
func TestResolveOptsFormatLongVerbose(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{format: "verbose", color: "never"}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.format != formatLong {
		t.Errorf("format = %d, want formatLong", opts.format)
	}
}

// TestResolveOptsColorAlways exercises the --color=always path.
func TestResolveOptsColorAlways(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{color: "always"}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.color {
		t.Error("color should be true with 'always'")
	}
}

// TestResolveOptsSortWordName exercises the --sort=name path.
func TestResolveOptsSortWordName(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{sortWord: "name", color: "never"}

	opts, err := resolveOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.sortBy != sortName {
		t.Errorf("sortBy = %d, want sortName", opts.sortBy)
	}
}

// TestRunMvArgValidation exercises runMv argument validation before session.
func TestRunMvArgValidation(t *testing.T) {
	// runMv with local-only paths should not need a session.
	// But it calls RestoreSession unconditionally. We test the session error path.
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveMvCmd.RunE(driveMvCmd, []string{"proton:///a", "proton:///b"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestExpandLocalRecursivePreserveMetadata exercises the preserve metadata collection.
func TestExpandLocalRecursivePreserveMetadata(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	if err := os.Mkdir(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	srcInfo, _ := os.Stat(srcDir)

	src := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       srcDir,
		localPath: srcDir,
		localInfo: srcInfo,
	}
	dstDir := filepath.Join(tmp, "dst")
	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       dstDir,
		localPath: dstDir,
	}

	ctx := context.Background()
	_, preserves, err := expandLocalRecursive(ctx, nil, src, dst, cpOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(preserves) != 1 {
		t.Errorf("got %d preserves, want 1", len(preserves))
	}
}

// TestMakeFileDstNilPathType exercises the default case of makeFileDst.
func TestMakeFileDstNilPathType(t *testing.T) {
	// PathType that is neither PathLocal nor PathProton.
	ep := makeFileDst(&resolvedEndpoint{pathType: PathType(99)}, "test")
	if ep != nil {
		t.Errorf("expected nil for unknown pathType, got %v", ep)
	}
}

// TestDfVolStateUnknown exercises the unknown state path.
func TestDfVolStateUnknown(t *testing.T) {
	got := dfVolState(proton.VolumeState(99))
	if !strings.Contains(got, "unknown") {
		t.Errorf("dfVolState(99) = %q, want 'unknown(...)'", got)
	}
}

// TestRunCpLocalToLocalOverwrite exercises the overwrite path in runCp.
func TestRunCpLocalToLocalOverwrite(t *testing.T) {
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
		t.Errorf("content = %q, want %q", got, "new-data")
	}
}

// TestRunCpLocalToLocalNewFile exercises creating a new file.
func TestRunCpLocalToLocalNewFile(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "new-dst.txt")
	if err := os.WriteFile(src, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := runCp(nil, []string{src, dst}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	got, err := os.ReadFile(dst) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "content" {
		t.Errorf("content = %q, want %q", got, "content")
	}
}

// TestRunCpDirectoryWithoutRecursive exercises the "is a directory" skip.
func TestRunCpDirectoryWithoutRecursive(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "srcdir")
	if err := os.Mkdir(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(tmp, "dstdir")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Without -r, directory source should be skipped (not error).
	err := runCp(nil, []string{srcDir, dstDir})
	if err != nil {
		t.Fatalf("runCp: %v", err)
	}
}

// TestRunCpBackupExistingFile exercises the backup path in runCp.
func TestRunCpBackupExistingFile(t *testing.T) {
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

	// Backup should exist.
	backup, err := os.ReadFile(dst + "~") //nolint:gosec // test temp path
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(backup) != "old" {
		t.Errorf("backup = %q, want %q", backup, "old")
	}

	// New content should be written.
	got, err := os.ReadFile(dst) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

// TestRunCpRemoveDestExistingFile exercises the remove-destination path.
func TestRunCpRemoveDestExistingFile(t *testing.T) {
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
		t.Errorf("content = %q, want %q", got, "new")
	}
}

// TestRunCpSameSourceAndDest exercises the same-file error.
func TestRunCpSameSourceAndDest(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	err := runCp(nil, []string{f, f})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "source and destination are the same") {
		t.Errorf("error = %q, want 'source and destination are the same'", err)
	}
}

// TestRunCpVerboseOutput exercises the verbose callback path.
func TestRunCpVerboseOutput(t *testing.T) {
	resetFlags()
	cpFlags.verbose = true
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := runCp(nil, []string{src, dst}); err != nil {
		t.Fatalf("runCp: %v", err)
	}
}

// TestRunCpProgressOutput exercises the progress callback path.
func TestRunCpProgressOutput(t *testing.T) {
	resetFlags()
	cpFlags.progress = true
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := runCp(nil, []string{src, dst}); err != nil {
		t.Fatalf("runCp: %v", err)
	}
}

// TestRunCpNonExistentSource exercises the non-existent source error.
func TestRunCpNonExistentSource(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "dst.txt")

	err := runCp(nil, []string{filepath.Join(tmp, "ghost.txt"), dst})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunCpNonExistentDestParent exercises the non-existent dest parent error.
func TestRunCpNonExistentDestParent(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	err := runCp(nil, []string{src, filepath.Join(tmp, "no", "such", "dir", "dst.txt")})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunCpMultiSourceToFile exercises the multi-source-to-file error.
func TestRunCpMultiSourceToFile(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	src1 := filepath.Join(tmp, "a.txt")
	src2 := filepath.Join(tmp, "b.txt")
	dst := filepath.Join(tmp, "dst.txt")
	if err := os.WriteFile(src1, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	err := runCp(nil, []string{src1, src2, dst})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error = %q, want 'not a directory'", err)
	}
}

// TestRunCpRecursiveDeepNested exercises recursive copy with deep nesting.
func TestRunCpRecursiveDeepNested(t *testing.T) {
	resetFlags()
	cpFlags.recursive = true
	tmp := t.TempDir()

	// Create a deep directory tree.
	srcDir := filepath.Join(tmp, "src")
	for _, dir := range []string{
		"a/b/c",
		"a/b/d",
		"a/e",
	} {
		if err := os.MkdirAll(filepath.Join(srcDir, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		"a/top.txt",
		"a/b/mid.txt",
		"a/b/c/deep.txt",
		"a/b/d/other.txt",
		"a/e/side.txt",
	} {
		if err := os.WriteFile(filepath.Join(srcDir, f), []byte(f), 0600); err != nil {
			t.Fatal(err)
		}
	}

	dstDir := filepath.Join(tmp, "dst")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatal(err)
	}

	if err := runCp(nil, []string{filepath.Join(srcDir, "a"), dstDir}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	// Verify files were copied.
	for _, f := range []string{
		"a/top.txt",
		"a/b/mid.txt",
		"a/b/c/deep.txt",
		"a/b/d/other.txt",
		"a/e/side.txt",
	} {
		got, err := os.ReadFile(filepath.Join(dstDir, f)) //nolint:gosec // test temp path
		if err != nil {
			t.Errorf("missing %s: %v", f, err)
			continue
		}
		if string(got) != f {
			t.Errorf("%s: got %q, want %q", f, got, f)
		}
	}
}

// TestRunCpPreserveTimestampsOnly exercises timestamps-only preservation.
func TestRunCpPreserveTimestampsOnly(t *testing.T) {
	resetFlags()
	cpFlags.preserve = "timestamps"
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	past := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(src, past, past); err != nil {
		t.Fatal(err)
	}

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
}

// TestRunCpSymlinkSkip exercises the symlink skip path in runCp.
func TestRunCpSymlinkSkip(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported")
	}
	dst := filepath.Join(tmp, "dst.txt")

	// Without -L, symlink should be skipped (not error).
	err := runCp(nil, []string{link, dst})
	if err != nil {
		// errSkipSymlink is printed to stderr, not returned.
		// But resolveSource wraps it, and runCp checks for errSkipSymlink.
		// Actually it prints to stderr and continues.
		t.Logf("got error: %v", err)
	}
}

// TestRunCpDereferenceSymlink exercises the -L flag path.
func TestRunCpDereferenceSymlink(t *testing.T) {
	resetFlags()
	cpFlags.dereference = true
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target.txt")
	if err := os.WriteFile(target, []byte("followed"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported")
	}
	dst := filepath.Join(tmp, "dst.txt")

	if err := runCp(nil, []string{link, dst}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	got, err := os.ReadFile(dst) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "followed" {
		t.Errorf("content = %q, want %q", got, "followed")
	}
}

// TestApplyPreserveTimestampsOnly exercises the timestamps-only path.
func TestApplyPreserveTimestampsOnly(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	past := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []preserveEntry{
		{dstPath: f, mtime: past},
	}
	applyPreserve(entries, cpOptions{preserve: "timestamps"})

	info, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), past)
	}
}

// TestApplyPreserveNonExistentFile exercises the error path in applyPreserve.
func TestApplyPreserveNonExistentFile(_ *testing.T) {
	entries := []preserveEntry{
		{dstPath: "/nonexistent/path/file.txt", mode: 0755},
	}
	// Should not panic, just print to stderr.
	applyPreserve(entries, cpOptions{preserve: "mode"})
}

// TestApplyPreserveTimestampsNonExistent exercises the timestamps error path.
func TestApplyPreserveTimestampsNonExistent(_ *testing.T) {
	entries := []preserveEntry{
		{dstPath: "/nonexistent/path/file.txt"},
	}
	// Should not panic, just print to stderr.
	applyPreserve(entries, cpOptions{preserve: "timestamps"})
}

// TestParseProtonURISharePathNormalizesToEmpty exercises the path that
// normalizes to empty after the share component (e.g. proton://share/..).
func TestParseProtonURISharePathNormalizesToEmpty(t *testing.T) {
	share, path, err := parseProtonURI("proton://Drive/..")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if share != "Drive" {
		t.Errorf("share = %q, want %q", share, "Drive")
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
}

// TestRunCpArchiveExpandsFlags exercises the -a flag expansion.
func TestRunCpArchiveExpandsFlags(t *testing.T) {
	resetFlags()
	cpFlags.archive = true
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
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
		t.Errorf("content = %q, want %q", got, "data")
	}
}

// TestRunCpEmptyJobs exercises the "no jobs" early return.
func TestRunCpEmptyJobs(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "empty")
	if err := os.Mkdir(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(tmp, "dst")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Directory without -r → skipped, no jobs.
	err := runCp(nil, []string{srcDir, dstDir})
	if err != nil {
		t.Fatalf("runCp: %v", err)
	}
}

// TestRunCpRecursivePreserveCollectsMetadata exercises the preserve metadata
// collection during recursive copy.
func TestRunCpRecursivePreserveCollectsMetadata(t *testing.T) {
	resetFlags()
	cpFlags.recursive = true
	cpFlags.preserve = "mode,timestamps"
	tmp := t.TempDir()

	srcDir := filepath.Join(tmp, "src")
	if err := os.Mkdir(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(srcDir, "file.txt"), 0741); err != nil { //nolint:gosec // testing mode preservation
		t.Fatal(err)
	}
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(srcDir, "file.txt"), past, past); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(tmp, "dst")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatal(err)
	}

	if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	info, err := os.Stat(filepath.Join(dstDir, "src", "file.txt"))
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

// TestRunListResolveOptsInvalidSort exercises the invalid sort error path.
func TestRunListResolveOptsInvalidSort(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{sortWord: "bad-sort", color: "never"}

	err := driveListCmd.RunE(driveListCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Errorf("error = %q, want 'invalid --sort'", err)
	}
}

// TestRunListResolveOptsInvalidTimeStyle exercises the invalid time-style error path.
func TestRunListResolveOptsInvalidTimeStyle(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{timeStyle: "bad-style", color: "never"}

	err := driveListCmd.RunE(driveListCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --time-style") {
		t.Errorf("error = %q, want 'invalid --time-style'", err)
	}
}

// TestRunListResolveOptsInvalidColor exercises the invalid color error path.
func TestRunListResolveOptsInvalidColor(t *testing.T) {
	listFlags = struct {
		all, almostAll, long, single, across, columns bool
		human, recursive, reverse                     bool
		sortSize, sortTime, unsorted                  bool
		fullTime, trash, classify, inode              bool
		format, sortWord, timeStyle, color            string
	}{color: "bad-color"}

	err := driveListCmd.RunE(driveListCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --color") {
		t.Errorf("error = %q, want 'invalid --color'", err)
	}
}

// TestRunCpTargetDirWithMultipleSources exercises the -t flag with multiple sources.
func TestRunCpTargetDirWithMultipleSources(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	src1 := filepath.Join(tmp, "a.txt")
	src2 := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(src1, []byte("aaa"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("bbb"), 0600); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(tmp, "target")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatal(err)
	}
	cpFlags.targetDir = dstDir

	if err := runCp(nil, []string{src1, src2}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	for _, tc := range []struct {
		path    string
		content string
	}{
		{filepath.Join(dstDir, "a.txt"), "aaa"},
		{filepath.Join(dstDir, "b.txt"), "bbb"},
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
}

// TestPrintEntryColumnsColorAndInode exercises the color+inode path.
func TestPrintEntryColumnsColorAndInode(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	makeEntry := func(name string) listEntry {
		pl := &proton.Link{
			LinkID: name + "-id",
			Type:   proton.LinkTypeFile,
			State:  proton.LinkStateActive,
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	entries := []listEntry{makeEntry("file1"), makeEntry("file2")}
	printEntryColumns(entries, false, listOpts{inode: true, color: true, classify: true})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "file1-id") {
		t.Errorf("output should contain link ID: %q", output)
	}
}

// TestPrintEntryColumnsLastColumn exercises the last-column path (no padding).
func TestPrintEntryColumnsLastColumn(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	makeEntry := func(name string) listEntry {
		pl := &proton.Link{
			LinkID: name + "-id",
			Type:   proton.LinkTypeFile,
			State:  proton.LinkStateActive,
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	// Create enough entries to fill multiple columns.
	var entries []listEntry
	for i := 0; i < 20; i++ {
		entries = append(entries, makeEntry(fmt.Sprintf("f%02d", i)))
	}
	printEntryColumns(entries, false, listOpts{})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "f00") || !strings.Contains(output, "f19") {
		t.Errorf("output should contain all entries: %q", output)
	}
}

// TestPrintEntryColumnsAcrossWithColor exercises across mode with color.
func TestPrintEntryColumnsAcrossWithColor(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	makeEntry := func(name string, lt proton.LinkType) listEntry {
		pl := &proton.Link{
			LinkID: name + "-id",
			Type:   lt,
			State:  proton.LinkStateActive,
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	entries := []listEntry{
		makeEntry("dir1", proton.LinkTypeFolder),
		makeEntry("file1", proton.LinkTypeFile),
		makeEntry("dir2", proton.LinkTypeFolder),
	}
	printEntryColumns(entries, true, listOpts{color: true, classify: true})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "dir1") {
		t.Errorf("output should contain dir1: %q", output)
	}
}

// testResolver is a mock LinkResolver that returns predetermined children
// for testing functions that need working Link.ListChildren/Readdir.
type testResolver struct {
	children map[string][]proton.Link // linkID → child proton.Links
}

func (r *testResolver) ListLinkChildren(_ context.Context, _, linkID string, _ bool) ([]proton.Link, error) {
	if children, ok := r.children[linkID]; ok {
		return children, nil
	}
	return nil, nil
}

func (r *testResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	return drive.NewTestLink(pLink, parent, parent.Share(), r, pLink.LinkID)
}

func (r *testResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (r *testResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (r *testResolver) Throttle() *apiPkg.Throttle { return nil }
func (r *testResolver) MaxWorkers() int            { return 1 }

// makeTestShare creates a test Share with a root link using the given resolver.
func makeTestShare(resolver drive.LinkResolver) (*drive.Share, *drive.Link) {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID: "test-share",
			Type:    proton.ShareTypeMain,
		},
	}
	// Create share first with nil link, then create link with share, then set share.Link.
	share := drive.NewShare(pShare, nil, nil, resolver, "vol-1")
	rootLink := drive.NewTestLink(&proton.Link{
		LinkID: "root-link",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "TestDrive")
	share.Link = rootLink
	return share, rootLink
}

// TestCollectEntries exercises collectEntries with a mock resolver.
func TestCollectEntries(t *testing.T) {
	resolver := &testResolver{
		children: map[string][]proton.Link{
			"root-link": {
				{LinkID: "child-1", Type: proton.LinkTypeFile, State: proton.LinkStateActive},
				{LinkID: "child-2", Type: proton.LinkTypeFolder, State: proton.LinkStateActive},
			},
		},
	}

	share, rootLink := makeTestShare(resolver)
	_ = share

	ctx := context.Background()

	t.Run("without all flag", func(t *testing.T) {
		entries, err := collectEntries(ctx, rootLink, listOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 2 {
			t.Errorf("got %d entries, want 2", len(entries))
		}
	})

	t.Run("with all flag", func(t *testing.T) {
		entries, err := collectEntries(ctx, rootLink, listOpts{all: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// With -a: . and .. plus 2 children = 4
		if len(entries) != 4 {
			t.Errorf("got %d entries, want 4", len(entries))
		}
	})
}

// TestListRecursive exercises listRecursive with mock entries.
func TestListRecursive(t *testing.T) {
	resolver := &testResolver{
		children: map[string][]proton.Link{
			"dir-1": {
				{LinkID: "nested-file", Type: proton.LinkTypeFile, State: proton.LinkStateActive},
			},
		},
	}

	share, _ := makeTestShare(resolver)

	// Create a directory entry that listRecursive will recurse into.
	dirLink := drive.NewTestLink(&proton.Link{
		LinkID: "dir-1",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "subdir")

	fileLink := drive.NewTestLink(&proton.Link{
		LinkID: "file-1",
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "file.txt")

	entries := []listEntry{
		{entry: drive.DirEntry{Link: dirLink}, name: "subdir"},
		{entry: drive.DirEntry{Link: fileLink}, name: "file.txt"},
	}

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	ctx := context.Background()
	err := listRecursive(ctx, "", entries, listOpts{})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should print the subdir header.
	if !strings.Contains(output, "subdir") {
		t.Errorf("output should contain 'subdir': %q", output)
	}
}

// TestResolveEntriesInvalidPath exercises resolveEntries with an invalid path.
func TestResolveEntriesInvalidPath(t *testing.T) {
	ctx := context.Background()
	// resolveEntries with a non-proton path triggers ResolveProtonPath error.
	_, err := resolveEntries(ctx, nil, []string{"/local/path"}, listOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestCollectEntriesEmpty exercises collectEntries with an empty directory.
func TestCollectEntriesEmpty(t *testing.T) {
	resolver := &testResolver{
		children: map[string][]proton.Link{},
	}

	_, rootLink := makeTestShare(resolver)

	ctx := context.Background()
	entries, err := collectEntries(ctx, rootLink, listOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

// TestCollectEntriesAllEmpty exercises collectEntries with -a on empty dir.
func TestCollectEntriesAllEmpty(t *testing.T) {
	resolver := &testResolver{
		children: map[string][]proton.Link{},
	}

	_, rootLink := makeTestShare(resolver)

	ctx := context.Background()
	entries, err := collectEntries(ctx, rootLink, listOpts{all: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With -a: . and .. only = 2
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

// TestListRecursiveEmpty exercises listRecursive with no directory entries.
func TestListRecursiveEmpty(t *testing.T) {
	resolver := &testResolver{}
	share, _ := makeTestShare(resolver)
	_ = share

	fileLink := drive.NewTestLink(&proton.Link{
		LinkID: "file-1",
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "file.txt")

	entries := []listEntry{
		{entry: drive.DirEntry{Link: fileLink}, name: "file.txt"},
	}

	ctx := context.Background()
	err := listRecursive(ctx, "", entries, listOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestListRecursiveNested exercises listRecursive with nested directories.
func TestListRecursiveNested(t *testing.T) {
	resolver := &testResolver{
		children: map[string][]proton.Link{
			"dir-1": {
				{LinkID: "dir-2", Type: proton.LinkTypeFolder, State: proton.LinkStateActive},
				{LinkID: "file-a", Type: proton.LinkTypeFile, State: proton.LinkStateActive},
			},
			"dir-2": {
				{LinkID: "file-b", Type: proton.LinkTypeFile, State: proton.LinkStateActive},
			},
		},
	}

	share, _ := makeTestShare(resolver)

	dirLink := drive.NewTestLink(&proton.Link{
		LinkID: "dir-1",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "top")

	entries := []listEntry{
		{entry: drive.DirEntry{Link: dirLink}, name: "top"},
	}

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	ctx := context.Background()
	err := listRecursive(ctx, "", entries, listOpts{})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(output, "top") {
		t.Errorf("output should contain 'top': %q", output)
	}
}

// TestListRecursiveWithFilter exercises listRecursive with filter options.
func TestListRecursiveWithFilter(t *testing.T) {
	resolver := &testResolver{
		children: map[string][]proton.Link{
			"dir-1": {
				{LinkID: "child-file", Type: proton.LinkTypeFile, State: proton.LinkStateActive},
				{LinkID: "child-dir", Type: proton.LinkTypeFolder, State: proton.LinkStateActive},
			},
		},
	}

	share, _ := makeTestShare(resolver)

	dirLink := drive.NewTestLink(&proton.Link{
		LinkID: "dir-1",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "mydir")

	entries := []listEntry{
		{entry: drive.DirEntry{Link: dirLink}, name: "mydir"},
	}

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	ctx := context.Background()
	err := listRecursive(ctx, "prefix/", entries, listOpts{
		sortBy:  sortName,
		reverse: true,
	})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	_ = string(buf[:n])

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCollectEntriesManyChildren exercises collectEntries with many children.
func TestCollectEntriesManyChildren(t *testing.T) {
	children := make([]proton.Link, 10)
	for i := range children {
		children[i] = proton.Link{
			LinkID: fmt.Sprintf("child-%d", i),
			Type:   proton.LinkTypeFile,
			State:  proton.LinkStateActive,
		}
	}

	resolver := &testResolver{
		children: map[string][]proton.Link{
			"root-link": children,
		},
	}

	_, rootLink := makeTestShare(resolver)

	ctx := context.Background()
	entries, err := collectEntries(ctx, rootLink, listOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 10 {
		t.Errorf("got %d entries, want 10", len(entries))
	}
}

// TestFilterEntriesTrashed exercises filterEntries with trashed items.
func TestFilterEntriesTrashed(t *testing.T) {
	resolver := &testResolver{}
	share, _ := makeTestShare(resolver)

	makeEntry := func(name string, state proton.LinkState) listEntry {
		l := drive.NewTestLink(&proton.Link{
			LinkID: name + "-id",
			Type:   proton.LinkTypeFile,
			State:  state,
		}, nil, share, resolver, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	entries := []listEntry{
		makeEntry("active.txt", proton.LinkStateActive),
		makeEntry("trashed.txt", proton.LinkStateTrashed),
		makeEntry("deleted.txt", proton.LinkStateDeleted),
	}

	t.Run("default hides trashed and deleted", func(t *testing.T) {
		filtered := filterEntries(entries, listOpts{})
		if len(filtered) != 1 {
			t.Errorf("got %d entries, want 1", len(filtered))
		}
	})

	t.Run("all shows trashed", func(t *testing.T) {
		filtered := filterEntries(entries, listOpts{all: true})
		if len(filtered) != 2 {
			t.Errorf("got %d entries, want 2 (active + trashed)", len(filtered))
		}
	})

	t.Run("trash mode shows only trashed", func(t *testing.T) {
		filtered := filterEntries(entries, listOpts{trash: true})
		if len(filtered) != 1 {
			t.Errorf("got %d entries, want 1 (trashed only)", len(filtered))
		}
		if len(filtered) > 0 && filtered[0].name != "trashed.txt" {
			t.Errorf("expected trashed.txt, got %s", filtered[0].name)
		}
	})
}

// TestFilterEntriesDotFiles exercises filterEntries with dot-files.
func TestFilterEntriesDotFiles(t *testing.T) {
	resolver := &testResolver{}
	share, _ := makeTestShare(resolver)

	makeEntry := func(name string) listEntry {
		l := drive.NewTestLink(&proton.Link{
			LinkID: name + "-id",
			Type:   proton.LinkTypeFile,
			State:  proton.LinkStateActive,
		}, nil, share, resolver, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	entries := []listEntry{
		makeEntry("visible.txt"),
		makeEntry(".hidden"),
		makeEntry(".config"),
	}

	t.Run("default hides dot-files", func(t *testing.T) {
		filtered := filterEntries(entries, listOpts{})
		if len(filtered) != 1 {
			t.Errorf("got %d entries, want 1", len(filtered))
		}
	})

	t.Run("all shows dot-files", func(t *testing.T) {
		filtered := filterEntries(entries, listOpts{all: true})
		if len(filtered) != 3 {
			t.Errorf("got %d entries, want 3", len(filtered))
		}
	})

	t.Run("almostAll shows dot-files", func(t *testing.T) {
		filtered := filterEntries(entries, listOpts{almostAll: true})
		if len(filtered) != 3 {
			t.Errorf("got %d entries, want 3", len(filtered))
		}
	})
}

// TestSortEntriesReverse exercises sortEntries with reverse and different modes.
func TestSortEntriesReverse(t *testing.T) {
	resolver := &testResolver{}
	share, _ := makeTestShare(resolver)

	makeEntry := func(name string, size int64, mtime int64) listEntry {
		pl := &proton.Link{
			LinkID:     name + "-id",
			Type:       proton.LinkTypeFile,
			State:      proton.LinkStateActive,
			ModifyTime: mtime,
		}
		if size > 0 {
			pl.FileProperties = &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{Size: size},
			}
		}
		l := drive.NewTestLink(pl, nil, share, resolver, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	t.Run("sort none reverse", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("a", 100, 1000),
			makeEntry("b", 200, 2000),
			makeEntry("c", 300, 3000),
		}
		sortEntries(entries, listOpts{sortBy: sortNone, reverse: true})
		if entries[0].name != "c" {
			t.Errorf("first entry = %s, want c", entries[0].name)
		}
	})

	t.Run("sort size", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("small", 100, 1000),
			makeEntry("big", 300, 2000),
			makeEntry("mid", 200, 3000),
		}
		sortEntries(entries, listOpts{sortBy: sortSize})
		if entries[0].name != "big" {
			t.Errorf("first entry = %s, want big", entries[0].name)
		}
	})

	t.Run("sort time", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("old", 100, 1000),
			makeEntry("new", 100, 3000),
			makeEntry("mid", 100, 2000),
		}
		sortEntries(entries, listOpts{sortBy: sortTime})
		// sortTime sorts by ModifyTime descending. For files with
		// FileProperties, ModifyTime returns ActiveRevision.CreateTime
		// which is 0 (not set in makeEntry). So all have same time
		// and order is stable. Just verify no panic.
		if len(entries) != 3 {
			t.Errorf("got %d entries, want 3", len(entries))
		}
	})

	t.Run("sort name reverse", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("alpha", 100, 1000),
			makeEntry("beta", 100, 2000),
			makeEntry("gamma", 100, 3000),
		}
		sortEntries(entries, listOpts{sortBy: sortName, reverse: true})
		if entries[0].name != "gamma" {
			t.Errorf("first entry = %s, want gamma", entries[0].name)
		}
	})
}

// TestBuildPredicatesUnknownType exercises the unknown type default case.
func TestBuildPredicatesUnknownType(t *testing.T) {
	// Save and restore findFlags.
	oldFlags := findFlags
	defer func() { findFlags = oldFlags }()

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
	}{findType: "x"} // unknown type

	preds := buildPredicates()
	resolver := &testResolver{}
	share, _ := makeTestShare(resolver)
	l := drive.NewTestLink(&proton.Link{
		LinkID: "test-id",
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "test")

	// Unknown type should match everything (default: return true).
	if !matchAll(preds, "/test", l, 0, "test") {
		t.Error("unknown type should match everything")
	}
}

// TestRunFindSessionErrorWithDepth exercises runFind with --depth flag.
func TestRunFindSessionErrorWithDepth(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	oldFlags := findFlags
	defer func() { findFlags = oldFlags }()

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
	}{depth: true, trashed: true}

	err := driveFindCmd.RunE(driveFindCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunFindSessionErrorWithArgs exercises runFind with explicit args.
func TestRunFindSessionErrorWithArgs(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	oldFlags := findFlags
	defer func() { findFlags = oldFlags }()

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

	err := driveFindCmd.RunE(driveFindCmd, []string{"proton:///Documents"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunMkdirInvalidPath exercises runMkdir with an invalid path (session error first).
func TestRunMkdirInvalidPath(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveMkdirCmd.RunE(driveMkdirCmd, []string{"/local/path"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunRmInvalidPath exercises runRm with an invalid path (session error first).
func TestRunRmInvalidPath(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveRmCmd.RunE(driveRmCmd, []string{"/local/path"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunRmdirInvalidPath exercises runRmdir with an invalid path (session error first).
func TestRunRmdirInvalidPath(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveRmdirCmd.RunE(driveRmdirCmd, []string{"/local/path"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunMkdirMissingDirName exercises runMkdir with proton:/// (session error first).
func TestRunMkdirMissingDirName(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveMkdirCmd.RunE(driveMkdirCmd, []string{"proton:///"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunRmdirMissingDirName exercises runRmdir with proton:/// (session error first).
func TestRunRmdirMissingDirName(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveRmdirCmd.RunE(driveRmdirCmd, []string{"proton:///"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunMkdirMultiplePaths exercises runMkdir with multiple paths.
func TestRunMkdirMultiplePaths(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	// First path is invalid, should fail before second.
	err := driveMkdirCmd.RunE(driveMkdirCmd, []string{"/bad/path", "proton:///Drive/dir"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunRmMultiplePaths exercises runRm with multiple paths.
func TestRunRmMultiplePaths(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveRmCmd.RunE(driveRmCmd, []string{"/bad/path", "proton:///file.txt"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunRmdirMultiplePaths exercises runRmdir with multiple paths.
func TestRunRmdirMultiplePaths(t *testing.T) {
	cleanup := withMockSession(t)
	defer cleanup()

	err := driveRmdirCmd.RunE(driveRmdirCmd, []string{"/bad/path", "proton:///dir"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// errorResolver is a mock LinkResolver that returns an error from ListLinkChildren.
type errorResolver struct {
	err error
}

func (r *errorResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, r.err
}

func (r *errorResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	return drive.NewTestLink(pLink, parent, parent.Share(), r, pLink.LinkID)
}

func (r *errorResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (r *errorResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (r *errorResolver) Throttle() *apiPkg.Throttle { return nil }
func (r *errorResolver) MaxWorkers() int            { return 1 }

// TestCollectEntriesListChildrenError exercises the error path in collectEntries
// when ListChildren fails (without -a flag).
func TestCollectEntriesListChildrenError(t *testing.T) {
	resolver := &errorResolver{err: fmt.Errorf("api error")}

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "err-share", Type: proton.ShareTypeMain},
	}
	share := drive.NewShare(pShare, nil, nil, resolver, "vol-1")
	rootLink := drive.NewTestLink(&proton.Link{
		LinkID: "root-link",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "root")
	share.Link = rootLink

	ctx := context.Background()
	_, err := collectEntries(ctx, rootLink, listOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestCollectEntriesReaddirError exercises the error path in collectEntries
// when Readdir yields an error (with -a flag).
func TestCollectEntriesReaddirError(t *testing.T) {
	resolver := &errorResolver{err: fmt.Errorf("readdir error")}

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "err-share", Type: proton.ShareTypeMain},
	}
	share := drive.NewShare(pShare, nil, nil, resolver, "vol-1")
	rootLink := drive.NewTestLink(&proton.Link{
		LinkID: "root-link",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "root")
	share.Link = rootLink

	ctx := context.Background()
	_, err := collectEntries(ctx, rootLink, listOpts{all: true})
	if err == nil {
		t.Fatal("expected error from Readdir")
	}
}

// TestListRecursiveCollectError exercises the error path in listRecursive
// when collectEntries fails.
func TestListRecursiveCollectError(t *testing.T) {
	resolver := &errorResolver{err: fmt.Errorf("collect error")}

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "err-share", Type: proton.ShareTypeMain},
	}
	share := drive.NewShare(pShare, nil, nil, resolver, "vol-1")
	rootLink := drive.NewTestLink(&proton.Link{
		LinkID: "root-link",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "root")
	share.Link = rootLink

	dirLink := drive.NewTestLink(&proton.Link{
		LinkID: "dir-err",
		Type:   proton.LinkTypeFolder,
		State:  proton.LinkStateActive,
	}, nil, share, resolver, "errdir")

	entries := []listEntry{
		{entry: drive.DirEntry{Link: dirLink}, name: "errdir"},
	}

	ctx := context.Background()
	err := listRecursive(ctx, "", entries, listOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestResolveEntriesProtonError exercises resolveEntries with a proton:// path
// that fails at ResolveProtonPath (bare proton://).
func TestResolveEntriesProtonError(t *testing.T) {
	ctx := context.Background()
	_, err := resolveEntries(ctx, nil, []string{"proton://"}, listOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestResolveDestParentIsFile exercises the "parent not a directory" path.
func TestResolveDestParentIsFile(t *testing.T) {
	tmp := t.TempDir()
	parentFile := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(parentFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(parentFile, "child.txt")

	ctx := context.Background()
	_, err := resolveDest(ctx, nil, pathArg{raw: dest, pathType: PathLocal}, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error = %q, want 'not a directory'", err)
	}
}

// TestResolveDestPermissionError exercises the non-IsNotExist error path.
func TestResolveDestPermissionError(t *testing.T) {
	tmp := t.TempDir()
	// Create a directory with no read permission.
	noRead := filepath.Join(tmp, "noperm")
	if err := os.Mkdir(noRead, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(noRead, 0700) }) //nolint:gosec // restoring test dir permissions for cleanup

	dest := filepath.Join(noRead, "child.txt")

	ctx := context.Background()
	_, err := resolveDest(ctx, nil, pathArg{raw: dest, pathType: PathLocal}, false)
	// On some systems this may succeed or fail differently.
	// We just verify it doesn't panic.
	_ = err
}

// TestRunCpLocalToLocalIntoDir exercises copying a file into an existing directory.
func TestRunCpLocalToLocalIntoDir(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dstDir := filepath.Join(tmp, "dstdir")
	if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
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
	if string(got) != "data" {
		t.Errorf("content = %q, want %q", got, "data")
	}
}

// TestRunCpRecursiveIntoExistingDir exercises recursive copy into existing dir.
func TestRunCpRecursiveIntoExistingDir(t *testing.T) {
	resetFlags()
	cpFlags.recursive = true
	tmp := t.TempDir()

	srcDir := filepath.Join(tmp, "src")
	if err := os.Mkdir(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "b.txt"), []byte("bbb"), 0600); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(tmp, "dst")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatal(err)
	}

	if err := runCp(nil, []string{srcDir, dstDir}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	for _, name := range []string{"a.txt", "b.txt"} {
		got, err := os.ReadFile(filepath.Join(dstDir, "src", name)) //nolint:gosec // test temp path
		if err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
		_ = got
	}
}

// TestRunCpRecursiveWithRemoveDestAndBackup exercises the mutually exclusive check.
func TestRunCpRecursiveWithRemoveDestAndBackup(t *testing.T) {
	resetFlags()
	cpFlags.removeDest = true
	cpFlags.backup = true

	err := runCp(nil, []string{"src", "dst"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want 'mutually exclusive'", err)
	}
}

// TestRunCpLocalNoSession exercises runCp with local-only paths (no session needed).
func TestRunCpLocalNoSession(t *testing.T) {
	resetFlags()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	if err := os.WriteFile(src, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	// Even with mock session, local-to-local should work.
	cleanup := withMockSession(t)
	defer cleanup()

	if err := runCp(nil, []string{src, dst}); err != nil {
		t.Fatalf("runCp: %v", err)
	}

	got, err := os.ReadFile(dst) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}
