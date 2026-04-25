package driveCmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

func TestBuildCopyJobErrors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, tmp string) (src, dst *resolvedEndpoint)
		wantErr string
	}{
		{
			name: "same local source and destination",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				f := filepath.Join(tmp, "file.txt")
				if err := os.WriteFile(f, []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
				info, _ := os.Stat(f)
				ep := &resolvedEndpoint{
					pathType:  PathLocal,
					raw:       f,
					localPath: f,
					localInfo: info,
				}
				return ep, &resolvedEndpoint{
					pathType:  PathLocal,
					raw:       f,
					localPath: f,
					localInfo: info,
				}
			},
			wantErr: "source and destination are the same",
		},
		{
			name: "different local paths succeed",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				src := filepath.Join(tmp, "src.txt")
				dst := filepath.Join(tmp, "dst.txt")
				if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
				srcInfo, _ := os.Stat(src)
				return &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       src,
						localPath: src,
						localInfo: srcInfo,
					}, &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       dst,
						localPath: dst,
						localInfo: nil,
					}
			},
			wantErr: "",
		},
		{
			name: "destination parent does not exist",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				src := filepath.Join(tmp, "src.txt")
				if err := os.WriteFile(src, []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
				srcInfo, _ := os.Stat(src)
				dst := filepath.Join(tmp, "no", "such", "dir", "dst.txt")
				return &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       src,
						localPath: src,
						localInfo: srcInfo,
					}, &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       dst,
						localPath: dst,
						localInfo: nil,
					}
			},
			wantErr: "no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			src, dst := tt.setup(t, tmp)
			ctx := context.Background()

			job, err := buildCopyJob(ctx, nil, src, dst)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if job == nil {
				t.Fatal("expected non-nil job")
			}
			if job.Src == nil {
				t.Error("expected non-nil Src reader")
			}
			if job.Dst == nil {
				t.Error("expected non-nil Dst writer")
			}
		})
	}
}

// TestLocalReader_BlockCount_Property verifies that BlockCount and
// BlockSize produce consistent results for arbitrary file sizes.
func TestLocalReader_BlockCount_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		size := int64(rapid.IntRange(0, drive.BlockSize*20).Draw(t, "size"))
		r := NewLocalReader("/dev/null", size)
		defer func() { _ = r.Close() }()

		wantBlocks := drive.BlockCount(size)
		if r.BlockCount() != wantBlocks {
			t.Fatalf("BlockCount() = %d, want %d for size %d", r.BlockCount(), wantBlocks, size)
		}

		var totalSize int64
		for i := 0; i < r.BlockCount(); i++ {
			bs := r.BlockSize(i)
			if bs <= 0 || bs > drive.BlockSize {
				t.Fatalf("BlockSize(%d) = %d, out of range", i, bs)
			}
			totalSize += bs
		}
		if totalSize != size {
			t.Fatalf("sum of BlockSize = %d, want %d", totalSize, size)
		}
	})
}

// TestTransferOpts_DefaultWorkers verifies the default worker count.
func TestTransferOpts_DefaultWorkers(t *testing.T) {
	want := defaultWorkers()

	opts := TransferOpts{}
	if got := opts.workers(); got != want {
		t.Fatalf("default workers = %d, want %d", got, want)
	}

	opts.Workers = 16
	if got := opts.workers(); got != 16 {
		t.Fatalf("custom workers = %d, want 16", got)
	}

	opts.Workers = -1
	if got := opts.workers(); got != want {
		t.Fatalf("negative workers = %d, want %d", got, want)
	}
}

// TestLocalReadWrite_RoundTrip_Property writes random data to a file,
// reads it back via LocalReader, and verifies the data matches.
func TestLocalReadWrite_RoundTrip_Property(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		size := int64(rapid.IntRange(1, drive.BlockSize+4096).Draw(t, "size"))
		data := make([]byte, size)
		for i := range data {
			data[i] = byte((i * 251) + int(size%127)) //nolint:gosec // deterministic test pattern
		}

		srcPath := filepath.Join(dir, rapid.StringMatching(`[a-z]{8}`).Draw(t, "name")+".bin")
		if err := os.WriteFile(srcPath, data, 0600); err != nil {
			t.Fatalf("write: %v", err)
		}

		r := NewLocalReader(srcPath, size)
		defer func() { _ = r.Close() }()

		if r.BlockCount() != drive.BlockCount(size) {
			t.Fatalf("BlockCount = %d, want %d", r.BlockCount(), drive.BlockCount(size))
		}

		var reassembled []byte
		for i := 0; i < r.BlockCount(); i++ {
			bs := r.BlockSize(i)
			buf := make([]byte, bs)
			n, err := r.ReadBlock(context.Background(), i, buf)
			if err != nil {
				t.Fatalf("ReadBlock(%d): %v", i, err)
			}
			reassembled = append(reassembled, buf[:n]...)
		}

		if !bytes.Equal(reassembled, data) {
			t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(reassembled), len(data))
		}
	})
}

// TestTransferOpts_Workers_TableDriven extends worker count tests.
func TestTransferOpts_Workers_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		want    int
	}{
		{"zero defaults", 0, defaultWorkers()},
		{"negative defaults", -1, defaultWorkers()},
		{"negative large", -100, defaultWorkers()},
		{"one", 1, 1},
		{"custom", 16, 16},
		{"large", 1000, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := TransferOpts{Workers: tt.workers}
			if got := opts.workers(); got != tt.want {
				t.Fatalf("workers() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestDefaultWorkers verifies the auto-detection logic.
func TestDefaultWorkers(t *testing.T) {
	n := defaultWorkers()
	if n < 2 {
		t.Fatalf("defaultWorkers() = %d, want >= 2", n)
	}
	if n > MaxAutoWorkers {
		t.Fatalf("defaultWorkers() = %d, want <= %d", n, MaxAutoWorkers)
	}
}

// TestBlockMap tests the blockMap claim logic.
func TestBlockMap(t *testing.T) {
	tests := []struct {
		name       string
		blockCount int
		claims     int
		wantLast   int
	}{
		{"single block", 1, 1, 0},
		{"single block exhausted", 1, 2, -1},
		{"multi block", 5, 3, 2},
		{"all claimed", 3, 4, -1},
		{"zero blocks", 0, 1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := int64(tt.blockCount) * drive.BlockSize
			if tt.blockCount == 0 {
				size = 0
			}
			r := NewLocalReader("/dev/null", size)
			defer func() { _ = r.Close() }()
			job := &CopyJob{Src: r}
			bm := newBlockMap(job)

			var last int
			for i := 0; i < tt.claims; i++ {
				last = bm.claim()
			}
			if last != tt.wantLast {
				t.Fatalf("after %d claims: last = %d, want %d", tt.claims, last, tt.wantLast)
			}
		})
	}
}

// TestLocalReader_Describe returns the path.
func TestLocalReader_Describe(t *testing.T) {
	r := NewLocalReader("/dev/null", 100)
	defer func() { _ = r.Close() }()
	if got := r.Describe(); got != "/dev/null" {
		t.Fatalf("Describe() = %q, want %q", got, "/dev/null")
	}
}

// TestLocalWriter_Describe returns the path.
func TestLocalWriter_Describe(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "out.bin")
	if err := os.WriteFile(f, nil, 0600); err != nil {
		t.Fatal(err)
	}
	w := NewLocalWriter(f)
	defer func() { _ = w.Close() }()
	if got := w.Describe(); got != f {
		t.Fatalf("Describe() = %q, want %q", got, f)
	}
}

// TestLocalReader_Close is a no-op on template (nil fd).
func TestLocalReader_Close(t *testing.T) {
	r := NewLocalReader("/dev/null", 0)
	if err := r.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

// TestLocalWriter_Close is a no-op on template (nil fd).
func TestLocalWriter_Close(t *testing.T) {
	w := NewLocalWriter("/dev/null")
	if err := w.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

// TestLocalReader_BlockSize_BeyondEnd verifies BlockSize returns 0 for
// indices beyond the file.
func TestLocalReader_BlockSize_BeyondEnd(t *testing.T) {
	r := NewLocalReader("/dev/null", 100)
	defer func() { _ = r.Close() }()
	if got := r.BlockSize(1); got != 0 {
		t.Fatalf("BlockSize(1) = %d, want 0 for 100-byte file", got)
	}
}

// TestCloneableReader verifies LocalReader implements CloneableReader.
func TestCloneableReader(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "src.bin")
	if err := os.WriteFile(f, []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}

	r := NewLocalReader(f, 11)
	var cr CloneableReader = r // compile-time check

	clone, err := cr.CloneReader()
	if err != nil {
		t.Fatalf("CloneReader: %v", err)
	}
	defer func() { _ = clone.Close() }()

	buf := make([]byte, 11)
	n, err := clone.ReadBlock(context.Background(), 0, buf)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if string(buf[:n]) != "hello world" {
		t.Fatalf("got %q, want %q", buf[:n], "hello world")
	}
}

// TestCloneableWriter verifies LocalWriter implements CloneableWriter.
func TestCloneableWriter(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "dst.bin")
	if err := os.WriteFile(f, make([]byte, 16), 0600); err != nil {
		t.Fatal(err)
	}

	w := NewLocalWriter(f)
	var cw CloneableWriter = w // compile-time check

	clone, err := cw.CloneWriter()
	if err != nil {
		t.Fatalf("CloneWriter: %v", err)
	}
	defer func() { _ = clone.Close() }()

	if err := clone.WriteBlock(context.Background(), 0, []byte("test data")); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}

	got, err := os.ReadFile(f) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, []byte("test data")) {
		t.Fatalf("got %q, want prefix %q", got, "test data")
	}
}
